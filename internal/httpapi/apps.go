package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/leonardoreis/cabana/internal/domain"
	"github.com/leonardoreis/cabana/internal/store"
)

// createAppRequest accepts a fully configured app, not just its identity.
//
// Everything past the first three fields is optional, so the simple case stays
// simple — but a guided setup can create an app that is ready to deploy in one
// request instead of four, with no window where a half-configured app exists.
type createAppRequest struct {
	Name    string `json:"name"`
	RepoURL string `json:"repo_url"`
	GitRef  string `json:"git_ref"`

	BuildArgs  map[string]string `json:"build_args"`
	Env        map[string]string `json:"env"`
	ServiceIDs []string          `json:"service_ids"`
	Replicas   *int32            `json:"replicas"`
	Health     *healthRequest    `json:"health"`
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.ListApps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not list apps")
		return
	}
	for i := range apps {
		apps[i].URL = s.deployer.AppURL(apps[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var req createAppRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	req.Name = strings.TrimSpace(strings.ToLower(req.Name))
	if err := domain.ValidateAppName(req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_name", err.Error())
		return
	}
	if err := validateRepoURL(req.RepoURL); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_repo_url", err.Error())
		return
	}
	if req.GitRef == "" {
		req.GitRef = "main"
	}

	for k := range req.BuildArgs {
		if err := validateEnvKey(k); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_build_arg", err.Error())
			return
		}
	}
	for k := range req.Env {
		if err := validateEnvKey(k); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_env_key", err.Error())
			return
		}
	}

	replicas := int32(1)
	if req.Replicas != nil {
		replicas = *req.Replicas
	}
	if err := domain.ValidateReplicas(replicas); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_replicas", err.Error())
		return
	}

	health := domain.Health{
		Type: domain.HealthTCP, Path: "/",
		InitialDelaySeconds: 3, PeriodSeconds: 10,
	}
	if req.Health != nil {
		health = req.Health.toDomain()
	}
	if err := domain.ValidateHealth(health); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_health", err.Error())
		return
	}

	// Services are resolved before the app is created, so an unknown one fails
	// without leaving an app behind.
	links := make(map[string]string, len(req.ServiceIDs))
	for _, id := range req.ServiceIDs {
		svc, err := s.store.ServiceByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusBadRequest, "unknown_service", "no service with id "+id)
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal", "could not load service")
			return
		}
		tmpl, ok := s.catalog.Get(svc.Template)
		if !ok {
			writeErr(w, http.StatusInternalServerError, "unknown_template",
				"service "+svc.Name+" uses a template that no longer exists")
			return
		}
		links[svc.ID] = tmpl.Connection.EnvVar
	}

	app := domain.App{
		Name:     req.Name,
		RepoURL:  req.RepoURL,
		GitRef:   req.GitRef,
		Status:   domain.AppStatusNew,
		Hostname: s.hostnameFor(req.Name),
		Replicas: replicas,
		Health:   health,
	}
	created, err := s.store.CreateApp(r.Context(), app)
	if err != nil {
		if isUniqueViolation(err) {
			writeErr(w, http.StatusConflict, "name_taken", "an app with that name already exists")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", "could not create app")
		return
	}

	// The app already exists past this point, so a failure here is reported but
	// does not roll back — the user lands on a created app with part of its
	// configuration and can finish it, rather than losing the whole form.
	if len(req.BuildArgs) > 0 {
		if err := s.store.ReplaceAppBuildArgs(r.Context(), created.ID, req.BuildArgs); err != nil {
			writeErr(w, http.StatusInternalServerError, "partial_create",
				"the app was created but its build args were not saved")
			return
		}
	}
	if len(req.Env) > 0 {
		if err := s.store.ReplaceAppEnv(r.Context(), created.ID, req.Env); err != nil {
			writeErr(w, http.StatusInternalServerError, "partial_create",
				"the app was created but its environment was not saved")
			return
		}
	}
	for serviceID, envVar := range links {
		if err := s.store.LinkService(r.Context(), created.ID, serviceID, envVar); err != nil {
			writeErr(w, http.StatusInternalServerError, "partial_create",
				"the app was created but a service link was not saved")
			return
		}
	}

	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleGetBuildArgs(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	args, err := s.store.AppBuildArgs(r.Context(), app.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not read build args")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"build_args": args})
}

type putBuildArgsRequest struct {
	BuildArgs map[string]string `json:"build_args"`
}

func (s *Server) handlePutBuildArgs(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	var req putBuildArgsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	for k := range req.BuildArgs {
		if err := validateEnvKey(k); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_build_arg", err.Error())
			return
		}
	}
	if err := s.store.ReplaceAppBuildArgs(r.Context(), app.ID, req.BuildArgs); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not save build args")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"build_args": req.BuildArgs})
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	app.URL = s.deployer.AppURL(app)
	writeJSON(w, http.StatusOK, app)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	// Tear down the workload before dropping the record, so a failure here
	// leaves the app visible in the dashboard rather than orphaned in the cluster.
	if err := s.deployer.Destroy(r.Context(), app); err != nil {
		writeErr(w, http.StatusInternalServerError, "destroy_failed", err.Error())
		return
	}
	if err := s.store.DeleteApp(r.Context(), app.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not delete app")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetEnv(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	env, err := s.store.AppEnv(r.Context(), app.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not read env")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"env": env})
}

type putEnvRequest struct {
	Env map[string]string `json:"env"`
}

func (s *Server) handlePutEnv(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	var req putEnvRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	for k := range req.Env {
		if err := validateEnvKey(k); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_env_key", err.Error())
			return
		}
	}
	if err := s.store.ReplaceAppEnv(r.Context(), app.ID, req.Env); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not save env")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"env": req.Env})
}

// lookupApp resolves the {id} path value, writing the 404/500 itself and
// reporting whether the caller should continue.
func (s *Server) lookupApp(w http.ResponseWriter, r *http.Request) (domain.App, bool) {
	app, err := s.store.AppByID(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "app does not exist")
			return domain.App{}, false
		}
		writeErr(w, http.StatusInternalServerError, "internal", "could not load app")
		return domain.App{}, false
	}
	return app, true
}

// hostnameFor builds the public hostname. With no wildcard domain configured the
// caller gets an empty string, and the deployer falls back to sslip.io once it
// knows the node IP.
func (s *Server) hostnameFor(name string) string {
	if s.cfg.BaseDomain == "" {
		return ""
	}
	return name + "." + s.cfg.BaseDomain
}

func validateRepoURL(raw string) error {
	if raw == "" {
		return errors.New("repo_url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("repo_url is not a valid URL: %w", err)
	}
	// v0.1 clones public repos only; ssh:// and git:// arrive with deploy keys in v0.6.
	if u.Scheme != "https" {
		return errors.New("repo_url must be an https:// URL (private repos land in v0.6)")
	}
	if u.Host == "" {
		return errors.New("repo_url must include a host")
	}
	return nil
}

func validateEnvKey(k string) error {
	if k == "" {
		return errors.New("env keys cannot be empty")
	}
	for i, r := range k {
		isUpper := r >= 'A' && r <= 'Z'
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if isDigit && i == 0 {
			return fmt.Errorf("env key %q cannot start with a digit", k)
		}
		if !isUpper && !isLower && !isDigit && r != '_' {
			return fmt.Errorf("env key %q may only contain letters, digits and underscores", k)
		}
	}
	return nil
}

// isUniqueViolation detects Postgres SQLSTATE 23505 without importing pgconn
// into every call site.
func isUniqueViolation(err error) bool {
	var sqlState interface{ SQLState() string }
	return errors.As(err, &sqlState) && sqlState.SQLState() == "23505"
}

type updateSettingsRequest struct {
	Replicas int32         `json:"replicas"`
	Health   healthRequest `json:"health"`
}

// healthRequest uses pointers for the numeric fields so an omitted field is
// distinguishable from an explicit zero. Without that, a caller sending
// period_seconds: 0 would have it silently rewritten to the default instead of
// being told it is out of range.
type healthRequest struct {
	Type                string `json:"type"`
	Path                string `json:"path"`
	InitialDelaySeconds *int32 `json:"initial_delay_seconds"`
	PeriodSeconds       *int32 `json:"period_seconds"`
}

// toDomain applies defaults for omitted fields only.
func (h healthRequest) toDomain() domain.Health {
	out := domain.Health{
		Type:                domain.HealthType(h.Type),
		Path:                h.Path,
		InitialDelaySeconds: 3,
		PeriodSeconds:       10,
	}
	if h.Path == "" {
		out.Path = "/"
	}
	if h.InitialDelaySeconds != nil {
		out.InitialDelaySeconds = *h.InitialDelaySeconds
	}
	if h.PeriodSeconds != nil {
		out.PeriodSeconds = *h.PeriodSeconds
	}
	return out
}

// handleUpdateSettings changes replicas and health checks. Like every other
// change to how an app runs, it takes effect on the next deploy rather than
// mutating a live workload behind the user's back.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}

	var req updateSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := domain.ValidateReplicas(req.Replicas); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_replicas", err.Error())
		return
	}
	health := req.Health.toDomain()
	if err := domain.ValidateHealth(health); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_health", err.Error())
		return
	}

	if err := s.store.UpdateAppSettings(r.Context(), app.ID, req.Replicas, health); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not save settings")
		return
	}

	updated, err := s.store.AppByID(r.Context(), app.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not reload app")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleInstances reports the live state of each replica.
func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	instances, err := s.deployer.Instances(r.Context(), app)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instances": instances,
		"desired":   app.Replicas,
	})
}
