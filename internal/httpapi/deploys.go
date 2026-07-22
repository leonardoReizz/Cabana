package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/leonardoreis/cabana/internal/deploy"
	"github.com/leonardoreis/cabana/internal/domain"
	"github.com/leonardoreis/cabana/internal/store"
)

func (s *Server) handleListDeploys(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	deploys, err := s.store.ListDeploys(r.Context(), app.ID, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not list deploys")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deploys": deploys})
}

type createDeployRequest struct {
	// RollbackTo names a previous deploy to restore. Empty means build from the
	// repository as usual.
	RollbackTo string `json:"rollback_to"`
}

func (s *Server) handleCreateDeploy(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}

	var req createDeployRequest
	// An empty body is a normal deploy, so a missing body is not an error.
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		return
	}

	var image string
	var rollbackFrom *string
	if req.RollbackTo != "" {
		source, err := s.store.DeployByID(r.Context(), req.RollbackTo)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "not_found", "that deploy does not exist")
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal", "could not load deploy")
			return
		}
		if source.AppID != app.ID {
			writeErr(w, http.StatusNotFound, "not_found", "that deploy belongs to another app")
			return
		}
		// Only a succeeded deploy has an image in the registry. Rolling back to
		// a failed one would apply a reference that was never pushed, and the
		// app would sit in ImagePullBackOff.
		if source.Status != domain.DeployStatusSucceeded {
			writeErr(w, http.StatusConflict, "not_rollbackable",
				"only a succeeded deploy can be rolled back to")
			return
		}
		if source.Image == "" {
			writeErr(w, http.StatusConflict, "not_rollbackable",
				"this deploy predates image tracking, so its image is unknown")
			return
		}
		image = source.Image
		rollbackFrom = &source.ID
	}

	d, err := s.store.CreateDeploy(r.Context(), app.ID, app.GitRef, image, rollbackFrom)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not queue deploy")
		return
	}

	// Includes the connection strings of every linked service, rendered fresh.
	env, err := s.resolveEnv(r.Context(), app.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not resolve env")
		return
	}

	buildArgs, err := s.store.AppBuildArgs(r.Context(), app.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not read build args")
		return
	}

	// The request returns as soon as the deploy row exists; the client follows
	// progress over the log stream. Detached from the request context so a
	// closed browser tab does not cancel a running build.
	rollback := ""
	if rollbackFrom != nil {
		rollback = *rollbackFrom
	}
	go s.runDeploy(context.WithoutCancel(r.Context()), app, d, env, buildArgs, rollback)

	writeJSON(w, http.StatusAccepted, d)
}

func (s *Server) runDeploy(
	ctx context.Context,
	app domain.App,
	d domain.Deploy,
	env, buildArgs map[string]string,
	rollbackFrom string,
) {
	log := slog.With("app", app.Name, "deploy", d.ID)

	setStatus := func(ds domain.DeployStatus, as domain.AppStatus, msg string) {
		if err := s.store.UpdateDeployStatus(ctx, d.ID, ds, msg); err != nil {
			log.Error("update deploy status", "error", err)
		}
		if err := s.store.SetAppStatus(ctx, app.ID, as); err != nil {
			log.Error("update app status", "error", err)
		}
	}

	setStatus(domain.DeployStatusBuilding, domain.AppStatusBuilding, "")

	// A normal build's image name is derived from the deploy ID, so it can only
	// be recorded once the deploy row exists.
	if d.Image == "" {
		d.Image = s.deployer.ImageRef(app, d.ID)
		if err := s.store.SetDeployImage(ctx, d.ID, d.Image); err != nil {
			log.Error("record deploy image", "error", err)
		}
	}

	req := deploy.Request{
		App: app, Deploy: d, Env: env,
		BuildArgs: buildArgs, RollbackFrom: rollbackFrom,
	}
	if err := s.deployer.Deploy(ctx, req); err != nil {
		log.Error("deploy failed", "error", err)
		setStatus(domain.DeployStatusFailed, domain.AppStatusFailed, err.Error())
		return
	}

	// Persist the resolved address before flipping to running, so the dashboard
	// never shows a running app it cannot give the user a link to.
	if hostname := s.deployer.Hostname(app); hostname != app.Hostname {
		if err := s.store.SetAppHostname(ctx, app.ID, hostname); err != nil {
			log.Error("persist hostname", "error", err)
		}
	}

	setStatus(domain.DeployStatusSucceeded, domain.AppStatusRunning, "")
	log.Info("deploy succeeded")
}

// handleLogs streams app logs as Server-Sent Events. SSE rather than WebSocket
// because the stream is one-directional and SSE reconnects on its own.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	streamSSE(w, r, func(ctx context.Context, out chan<- deploy.LogLine) error {
		return s.deployer.StreamLogs(ctx, app, out)
	})
}

// streamSSE runs a log producer and writes its lines as Server-Sent Events.
// Shared by apps and services so both behave identically on the wire.
func streamSSE(
	w http.ResponseWriter,
	r *http.Request,
	produce func(ctx context.Context, out chan<- deploy.LogLine) error,
) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Defeats proxy buffering, which otherwise holds the stream until it fills.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	lines := make(chan deploy.LogLine, 64)

	go func() {
		defer close(lines)
		if err := produce(r.Context(), lines); err != nil {
			slog.Debug("log stream ended", "path", r.URL.Path, "error", err)
		}
	}()

	for line := range lines {
		payload, err := json.Marshal(line)
		if err != nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return // client went away
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

// handleDeployLog returns a stored build log. Unlike the live stream this is
// available long after the deploy finished, which is the point: the build you
// want to read is usually the one that already failed.
func (s *Server) handleDeployLog(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}

	deployID := r.PathValue("deployID")
	d, err := s.store.DeployByID(r.Context(), deployID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "deploy does not exist")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", "could not load deploy")
		return
	}
	// Guards against reading one app's build log through another app's URL.
	if d.AppID != app.ID {
		writeErr(w, http.StatusNotFound, "not_found", "deploy does not belong to this app")
		return
	}

	content, truncated, err := s.store.DeployLog(r.Context(), deployID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// A queued deploy, or one that failed before the build started, has
			// no log rather than an error.
			writeJSON(w, http.StatusOK, map[string]any{
				"log": "", "truncated": false, "status": d.Status,
			})
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", "could not read build log")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"log": content, "truncated": truncated, "status": d.Status,
	})
}
