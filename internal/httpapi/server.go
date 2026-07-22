package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/leonardoreis/cabana/internal/catalog"
	"github.com/leonardoreis/cabana/internal/config"
	"github.com/leonardoreis/cabana/internal/deploy"
	"github.com/leonardoreis/cabana/internal/store"
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	deployer deploy.Deployer
	catalog  *catalog.Catalog
	mux      *http.ServeMux
}

func New(cfg config.Config, st *store.Store, deployer deploy.Deployer, cat *catalog.Catalog) *Server {
	s := &Server{cfg: cfg, store: st, deployer: deployer, catalog: cat, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)

	s.mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/v1/auth/logout", s.authed(s.handleLogout))
	s.mux.HandleFunc("GET /api/v1/auth/me", s.authed(s.handleMe))

	s.mux.HandleFunc("GET /api/v1/apps", s.authed(s.handleListApps))
	s.mux.HandleFunc("POST /api/v1/apps", s.authed(s.handleCreateApp))
	s.mux.HandleFunc("GET /api/v1/apps/{id}", s.authed(s.handleGetApp))
	s.mux.HandleFunc("DELETE /api/v1/apps/{id}", s.authed(s.handleDeleteApp))

	s.mux.HandleFunc("PUT /api/v1/apps/{id}/settings", s.authed(s.handleUpdateSettings))
	s.mux.HandleFunc("GET /api/v1/apps/{id}/instances", s.authed(s.handleInstances))

	s.mux.HandleFunc("GET /api/v1/apps/{id}/build-args", s.authed(s.handleGetBuildArgs))
	s.mux.HandleFunc("PUT /api/v1/apps/{id}/build-args", s.authed(s.handlePutBuildArgs))

	s.mux.HandleFunc("GET /api/v1/apps/{id}/env", s.authed(s.handleGetEnv))
	s.mux.HandleFunc("PUT /api/v1/apps/{id}/env", s.authed(s.handlePutEnv))

	s.mux.HandleFunc("GET /api/v1/apps/{id}/deploys", s.authed(s.handleListDeploys))
	s.mux.HandleFunc("POST /api/v1/apps/{id}/deploys", s.authed(s.handleCreateDeploy))
	s.mux.HandleFunc("GET /api/v1/apps/{id}/deploys/{deployID}/log", s.authed(s.handleDeployLog))

	s.mux.HandleFunc("GET /api/v1/apps/{id}/logs", s.authed(s.handleLogs))

	s.mux.HandleFunc("GET /api/v1/apps/{id}/links", s.authed(s.handleListLinks))
	s.mux.HandleFunc("POST /api/v1/apps/{id}/links", s.authed(s.handleCreateLink))
	s.mux.HandleFunc("DELETE /api/v1/apps/{id}/links/{serviceID}", s.authed(s.handleDeleteLink))

	s.mux.HandleFunc("GET /api/v1/catalog", s.authed(s.handleCatalog))
	s.mux.HandleFunc("GET /api/v1/services", s.authed(s.handleListServices))
	s.mux.HandleFunc("POST /api/v1/services", s.authed(s.handleCreateService))
	s.mux.HandleFunc("GET /api/v1/services/{id}", s.authed(s.handleGetService))
	s.mux.HandleFunc("DELETE /api/v1/services/{id}", s.authed(s.handleDeleteService))
	s.mux.HandleFunc("GET /api/v1/services/{id}/credentials", s.authed(s.handleServiceCredentials))
	s.mux.HandleFunc("GET /api/v1/services/{id}/logs", s.authed(s.handleServiceLogs))

	s.mux.HandleFunc("GET /api/v1/settings/backup", s.authed(s.handleGetBackupConfig))
	s.mux.HandleFunc("PUT /api/v1/settings/backup", s.authed(s.handlePutBackupConfig))
	s.mux.HandleFunc("GET /api/v1/services/{id}/backups", s.authed(s.handleListBackups))
	s.mux.HandleFunc("POST /api/v1/services/{id}/backups", s.authed(s.handleCreateBackup))
	s.mux.HandleFunc("POST /api/v1/services/{id}/backups/{backupID}/restore", s.authed(s.handleRestoreBackup))
	s.mux.HandleFunc("PUT /api/v1/services/{id}/backup-schedule", s.authed(s.handleSetBackupSchedule))

	s.mux.HandleFunc("GET /api/v1/settings/state-backups", s.authed(s.handleListStateBackups))
	s.mux.HandleFunc("POST /api/v1/settings/state-backups", s.authed(s.handleCreateStateBackup))
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.withCORS(s.withLogging(s.mux)).ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("http",
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(start))
	})
}

// withCORS only opens up when CABANA_DEV_CORS_ORIGIN is set, which is the Vite
// dev server. In production the dashboard is same-origin and this is a no-op.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.DevCORSOrigin != "" && r.Header.Get("Origin") == s.cfg.DevCORSOrigin {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", s.cfg.DevCORSOrigin)
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap exposes the underlying writer to http.ResponseController, which the
// SSE log handler needs in order to flush.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	// SSE via EventSource cannot set headers, so the log stream accepts the
	// token as a query param instead.
	return r.URL.Query().Get("token")
}
