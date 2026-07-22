package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/leonardoreis/cabana/internal/catalog"
	"github.com/leonardoreis/cabana/internal/deploy"
	"github.com/leonardoreis/cabana/internal/domain"
	"github.com/leonardoreis/cabana/internal/store"
)

// handleCatalog lists what can be installed. Unauthenticated data, but behind
// auth like everything else — there is no reason for an anonymous caller to
// enumerate a private install.
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Category    string `json:"category"`
		Image       string `json:"image"`
		Port        int32  `json:"port"`
		Storage     string `json:"storage"`
		EnvVar      string `json:"env_var"`
		// Offered as the input's placeholder, so a user who does not care gets
		// a version that is known to work.
		DefaultVersion string `json:"default_version"`
	}

	templates := s.catalog.All()
	entries := make([]entry, 0, len(templates))
	for _, t := range templates {
		entries = append(entries, entry{
			Name: t.Name, DisplayName: t.DisplayName, Description: t.Description,
			Category: t.Category, Image: t.Image, Port: t.Port,
			Storage: t.Storage, EnvVar: t.Connection.EnvVar,
			DefaultVersion: t.DefaultVersion(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": entries})
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	services, err := s.store.ListServices(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not list services")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": services})
}

type createServiceRequest struct {
	Name     string `json:"name"`
	Template string `json:"template"`
	// Version is the image tag. Empty means the template's pinned default.
	Version string `json:"version"`
}

func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	var req createServiceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	req.Name = strings.TrimSpace(strings.ToLower(req.Name))
	if err := domain.ValidateServiceName(req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_name", err.Error())
		return
	}

	tmpl, ok := s.catalog.Get(req.Template)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown_template",
			"no catalogue template named "+req.Template)
		return
	}

	// Only the tag is user-supplied — the repository always comes from the
	// template, so this can never be turned into "install any image you like".
	image, err := tmpl.ImageFor(strings.TrimSpace(req.Version))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_version", err.Error())
		return
	}

	// Generated before the row is written so the credentials and the record are
	// created together or not at all.
	secrets, err := tmpl.GenerateSecrets()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not generate credentials")
		return
	}

	// Dependencies are resolved here, where the catalogue lives, so the deployer
	// receives concrete templates and never needs the catalogue itself.
	deps, err := s.resolveDependencies(tmpl)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	svc := domain.Service{
		Name:     req.Name,
		Template: tmpl.Name,
		Status:   domain.ServiceStatusProvisioning,
		Port:     tmpl.Port,
		Image:    image,
	}
	// A StatefulSet service advertises its own stable pod address; clients must
	// use that, not the load-balanced service host.
	if tmpl.IsStatefulSet() {
		svc.Host = deploy.ServicePodHost(svc)
	} else {
		svc.Host = deploy.ServiceHost(svc)
	}

	created, err := s.store.CreateService(r.Context(), svc, secrets)
	if err != nil {
		if isUniqueViolation(err) {
			writeErr(w, http.StatusConflict, "name_taken", "a service with that name already exists")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", "could not create service")
		return
	}

	// Provisioning pulls an image and waits for a rollout, which is far too slow
	// to hold a request open for. The dashboard polls the status instead.
	go s.provisionService(context.WithoutCancel(r.Context()), created, tmpl, secrets, deps)

	writeJSON(w, http.StatusAccepted, created)
}

// resolveDependencies turns a template's requirements into concrete, credentialed
// dependencies the deployer can provision.
func (s *Server) resolveDependencies(tmpl catalog.Template) ([]deploy.ServiceDependency, error) {
	deps := make([]deploy.ServiceDependency, 0, len(tmpl.Requires))
	for _, req := range tmpl.Requires {
		depTmpl, ok := s.catalog.Get(req.Template)
		if !ok {
			// The catalogue validates this at boot, so reaching here means a
			// template changed under a running process.
			return nil, fmt.Errorf("required template %q is missing", req.Template)
		}
		depSecrets, err := depTmpl.GenerateSecrets()
		if err != nil {
			return nil, fmt.Errorf("could not generate credentials for %s", req.Name)
		}
		deps = append(deps, deploy.ServiceDependency{
			Name:     req.Name,
			Template: depTmpl,
			Secrets:  depSecrets,
			Inject:   req.Inject,
		})
	}
	return deps, nil
}

func (s *Server) provisionService(
	ctx context.Context,
	svc domain.Service,
	tmpl catalog.Template,
	secrets map[string]string,
	deps []deploy.ServiceDependency,
) {
	log := slog.With("service", svc.Name, "template", tmpl.Name)

	if err := s.deployer.ProvisionService(ctx, svc, tmpl, secrets, deps); err != nil {
		log.Error("provision failed", "error", err)
		if err := s.store.SetServiceStatus(ctx, svc.ID, domain.ServiceStatusFailed, err.Error()); err != nil {
			log.Error("record failure", "error", err)
		}
		return
	}

	if err := s.store.SetServiceStatus(ctx, svc.ID, domain.ServiceStatusRunning, ""); err != nil {
		log.Error("record success", "error", err)
	}
	log.Info("service ready", "host", svc.Host)
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lookupService(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

func (s *Server) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lookupService(w, r)
	if !ok {
		return
	}

	// Refuse while apps still depend on it: silently breaking a running app's
	// database connection is not a thing to do on one click.
	links, err := s.store.ServiceLinks(r.Context(), svc.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not check links")
		return
	}
	if len(links) > 0 {
		writeErr(w, http.StatusConflict, "service_in_use",
			"unlink it from "+strings.Join(links, ", ")+" first")
		return
	}

	if err := s.deployer.DestroyService(r.Context(), svc); err != nil {
		writeErr(w, http.StatusInternalServerError, "destroy_failed", err.Error())
		return
	}
	if err := s.store.DeleteService(r.Context(), svc.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not delete service")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleServiceCredentials returns the generated secrets and a ready-to-paste
// connection string. This is the only place credentials are ever exposed.
func (s *Server) handleServiceCredentials(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lookupService(w, r)
	if !ok {
		return
	}
	tmpl, ok := s.catalog.Get(svc.Template)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "unknown_template",
			"this service was installed from a template that no longer exists")
		return
	}

	secrets, err := s.store.ServiceSecrets(r.Context(), svc.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not read credentials")
		return
	}

	conn, err := tmpl.RenderConnection(catalog.ConnectionVars{
		Host: svc.Host, PodHost: svc.Host, Port: svc.Port, Password: secrets["password"],
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not render connection string")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"env_var":           tmpl.Connection.EnvVar,
		"connection_string": conn,
		"host":              svc.Host,
		"port":              svc.Port,
		"secrets":           secrets,
	})
}

// handleServiceLogs streams provisioning narration and container output, the
// same way an app's log panel works.
func (s *Server) handleServiceLogs(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lookupService(w, r)
	if !ok {
		return
	}
	streamSSE(w, r, func(ctx context.Context, out chan<- deploy.LogLine) error {
		return s.deployer.StreamServiceLogs(ctx, svc, out)
	})
}

func (s *Server) lookupService(w http.ResponseWriter, r *http.Request) (domain.Service, bool) {
	svc, err := s.store.ServiceByID(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "service does not exist")
			return domain.Service{}, false
		}
		writeErr(w, http.StatusInternalServerError, "internal", "could not load service")
		return domain.Service{}, false
	}
	return svc, true
}

// ---- links ----

func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	links, err := s.store.AppLinks(r.Context(), app.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not list links")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

type createLinkRequest struct {
	ServiceID string `json:"service_id"`
}

func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	var req createLinkRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	svc, err := s.store.ServiceByID(r.Context(), req.ServiceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "service does not exist")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", "could not load service")
		return
	}
	tmpl, ok := s.catalog.Get(svc.Template)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "unknown_template",
			"this service was installed from a template that no longer exists")
		return
	}

	if err := s.store.LinkService(r.Context(), app.ID, svc.ID, tmpl.Connection.EnvVar); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not link service")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"service_id":   svc.ID,
		"service_name": svc.Name,
		"env_var":      tmpl.Connection.EnvVar,
		// The variable only reaches the container on the next rollout, and
		// saying so beats a user wondering why their app cannot see it.
		"applies_on": "next deploy",
	})
}

func (s *Server) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	err := s.store.UnlinkService(r.Context(), app.ID, r.PathValue("serviceID"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "this service is not linked to this app")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", "could not unlink service")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveEnv merges an app's own variables with the connection strings of every
// linked service.
//
// Linked variables are rendered here, at deploy time, rather than copied into
// app_env when the link is made. That keeps one source of truth: unlinking
// removes the variable, and a user editing their environment cannot delete or
// corrupt a managed connection string.
func (s *Server) resolveEnv(ctx context.Context, appID string) (map[string]string, error) {
	env, err := s.store.AppEnv(ctx, appID)
	if err != nil {
		return nil, err
	}

	linked, err := s.store.LinkedServiceEnv(ctx, appID)
	if err != nil {
		return nil, err
	}
	for _, ls := range linked {
		tmpl, ok := s.catalog.Get(ls.Template)
		if !ok {
			slog.Warn("linked service uses an unknown template",
				"service", ls.Name, "template", ls.Template)
			continue
		}
		conn, err := tmpl.RenderConnection(catalog.ConnectionVars{
			Host: ls.Host, PodHost: ls.Host, Port: ls.Port, Password: ls.Secrets["password"],
		})
		if err != nil {
			return nil, err
		}
		// Managed variables win over a user's own value of the same name: the
		// link is the more explicit statement of intent, and a stale hand-typed
		// DATABASE_URL silently overriding it would be very hard to debug.
		env[ls.EnvVar] = conn
	}
	return env, nil
}
