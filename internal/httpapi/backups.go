package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/leonardoreis/cabana/internal/catalog"
	"github.com/leonardoreis/cabana/internal/deploy"
	"github.com/leonardoreis/cabana/internal/domain"
	"github.com/leonardoreis/cabana/internal/store"
)

// handleGetBackupConfig returns the destination without its secret key — the
// dashboard shows whether one is set and what it is, never the credential back.
func (s *Server) handleGetBackupConfig(w http.ResponseWriter, r *http.Request) {
	cfg, ok, err := s.store.BackupConfig(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not read backup config")
		return
	}
	cfg.SecretKey = "" // Write-only: never leaves the server.
	cfg.Configured = ok
	writeJSON(w, http.StatusOK, cfg)
}

type backupConfigRequest struct {
	Endpoint       string `json:"endpoint"`
	Region         string `json:"region"`
	Bucket         string `json:"bucket"`
	AccessKey      string `json:"access_key"`
	SecretKey      string `json:"secret_key"`
	RetentionCount int32  `json:"retention_count"`
}

func (s *Server) handlePutBackupConfig(w http.ResponseWriter, r *http.Request) {
	var req backupConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	if req.Endpoint == "" || req.Bucket == "" || req.AccessKey == "" {
		writeErr(w, http.StatusBadRequest, "invalid_config",
			"endpoint, bucket and access key are required")
		return
	}
	// An https:// endpoint is not required — a MinIO on the same cluster is
	// plain http — but a scheme is, or the aws CLI cannot parse it.
	if !strings.HasPrefix(req.Endpoint, "http://") && !strings.HasPrefix(req.Endpoint, "https://") {
		writeErr(w, http.StatusBadRequest, "invalid_config",
			"endpoint must start with http:// or https://")
		return
	}
	if req.Region == "" {
		req.Region = "auto"
	}

	// An empty secret on update keeps the stored one, so re-saving the form
	// without re-typing the secret does not wipe it.
	if req.SecretKey == "" {
		existing, ok, err := s.store.BackupConfig(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal", "could not read backup config")
			return
		}
		if !ok {
			writeErr(w, http.StatusBadRequest, "invalid_config", "secret key is required")
			return
		}
		req.SecretKey = existing.SecretKey
	}

	if req.RetentionCount < 0 {
		writeErr(w, http.StatusBadRequest, "invalid_config", "retention cannot be negative")
		return
	}

	cfg := domain.BackupConfig{
		Endpoint: req.Endpoint, Region: req.Region, Bucket: req.Bucket,
		AccessKey: req.AccessKey, SecretKey: req.SecretKey,
		RetentionCount: req.RetentionCount,
	}
	if err := s.store.SaveBackupConfig(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not save backup config")
		return
	}
	cfg.SecretKey = ""
	cfg.Configured = true
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lookupService(w, r)
	if !ok {
		return
	}
	backups, err := s.store.ListBackups(r.Context(), svc.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not list backups")
		return
	}
	tmpl, _ := s.catalog.Get(svc.Template)
	writeJSON(w, http.StatusOK, map[string]any{
		"backups":    backups,
		"backupable": tmpl.Backupable(),
	})
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lookupService(w, r)
	if !ok {
		return
	}
	tmpl, s3, ready := s.backupPrereqs(w, r, svc)
	if !ready {
		return
	}

	if err := s.startBackup(context.WithoutCancel(r.Context()), svc, tmpl, s3); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not record backup")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// startBackup records a backup and runs it in the background. Shared by the
// manual endpoint and the scheduler, so both take the identical, tested path.
func (s *Server) startBackup(ctx context.Context, svc domain.Service, tmpl catalog.Template, s3 deploy.S3Config) error {
	// One object per backup, namespaced by service so the bucket stays legible.
	objectKey := fmt.Sprintf("%s/%s.dump", svc.Name, domain.NewID())
	backup, err := s.store.CreateBackup(ctx, svc, objectKey)
	if err != nil {
		return err
	}
	go s.runBackup(ctx, svc, tmpl, s3, backup)
	return nil
}

func (s *Server) runBackup(ctx context.Context, svc domain.Service, tmpl catalog.Template, s3 deploy.S3Config, backup domain.Backup) {
	log := slog.With("service", svc.Name, "backup", backup.ID)

	size, err := s.deployer.BackupService(ctx, svc, tmpl, s3, backup.ObjectKey)
	if err != nil {
		log.Error("backup failed", "error", err)
		if e := s.store.FinishBackup(ctx, backup.ID, domain.BackupFailed, 0, err.Error()); e != nil {
			log.Error("record backup failure", "error", e)
		}
		return
	}
	if err := s.store.FinishBackup(ctx, backup.ID, domain.BackupSucceeded, size, ""); err != nil {
		log.Error("record backup success", "error", err)
	}
	log.Info("backup succeeded", "bytes", size)

	s.pruneRetention(ctx, svc, s3)
}

// pruneRetention removes a service's backups beyond the keep count, from S3 and
// then from the record. Best-effort: a prune failure never fails the backup
// that triggered it, only leaves an old copy behind for next time.
func (s *Server) pruneRetention(ctx context.Context, svc domain.Service, s3 deploy.S3Config) {
	cfg, ok, err := s.store.BackupConfig(ctx)
	if err != nil || !ok || cfg.RetentionCount <= 0 {
		return
	}
	stale, err := s.store.StaleBackups(ctx, svc.ID, cfg.RetentionCount)
	if err != nil || len(stale) == 0 {
		return
	}

	keys := make([]string, len(stale))
	for i, b := range stale {
		keys[i] = b.ObjectKey
	}
	if err := s.deployer.PruneObjects(ctx, svc, s3, keys); err != nil {
		slog.Warn("prune old backups", "service", svc.Name, "error", err)
		return
	}
	for _, b := range stale {
		if err := s.store.DeleteBackup(ctx, b.ID); err != nil {
			slog.Warn("delete pruned backup record", "backup", b.ID, "error", err)
		}
	}
	slog.Info("pruned old backups", "service", svc.Name, "count", len(stale))
}

type setScheduleRequest struct {
	Schedule string `json:"schedule"`
	Hour     int32  `json:"hour"`
}

func (s *Server) handleSetBackupSchedule(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lookupService(w, r)
	if !ok {
		return
	}
	var req setScheduleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := domain.ValidateBackupSchedule(req.Schedule, req.Hour); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_schedule", err.Error())
		return
	}
	// A schedule with no destination would fail silently every night.
	if req.Schedule != domain.BackupOff {
		if _, configured, err := s.store.BackupConfig(r.Context()); err != nil {
			writeErr(w, http.StatusInternalServerError, "internal", "could not read backup config")
			return
		} else if !configured {
			writeErr(w, http.StatusConflict, "no_destination", "set a backup destination first")
			return
		}
	}
	if err := s.store.SetBackupSchedule(r.Context(), svc.ID, req.Schedule, req.Hour); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not save schedule")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedule": req.Schedule, "hour": req.Hour})
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lookupService(w, r)
	if !ok {
		return
	}
	backup, err := s.store.BackupByID(r.Context(), r.PathValue("backupID"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "backup does not exist")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", "could not load backup")
		return
	}
	if backup.ServiceID != svc.ID {
		writeErr(w, http.StatusNotFound, "not_found", "backup belongs to another service")
		return
	}
	// Restoring a failed or in-progress backup would replay an object that is
	// absent or half-written.
	if backup.Status != domain.BackupSucceeded {
		writeErr(w, http.StatusConflict, "not_restorable", "only a succeeded backup can be restored")
		return
	}

	tmpl, s3, ready := s.backupPrereqs(w, r, svc)
	if !ready {
		return
	}

	if err := s.deployer.RestoreService(r.Context(), svc, tmpl, s3, backup.ObjectKey); err != nil {
		writeErr(w, http.StatusInternalServerError, "restore_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restored": backup.ID})
}

// backupPrereqs resolves the template and destination shared by backup and
// restore, writing the appropriate 4xx and returning ready=false when either is
// missing.
func (s *Server) backupPrereqs(w http.ResponseWriter, r *http.Request, svc domain.Service) (catalog.Template, deploy.S3Config, bool) {
	tmpl, ok := s.catalog.Get(svc.Template)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "unknown_template",
			"this service was installed from a template that no longer exists")
		return catalog.Template{}, deploy.S3Config{}, false
	}
	if !tmpl.Backupable() {
		writeErr(w, http.StatusBadRequest, "not_backupable",
			svc.Template+" does not support backup")
		return catalog.Template{}, deploy.S3Config{}, false
	}

	cfg, configured, err := s.store.BackupConfig(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not read backup config")
		return catalog.Template{}, deploy.S3Config{}, false
	}
	if !configured {
		writeErr(w, http.StatusConflict, "no_destination",
			"set a backup destination in settings first")
		return catalog.Template{}, deploy.S3Config{}, false
	}

	return tmpl, deploy.S3Config{
		Endpoint: cfg.Endpoint, Region: cfg.Region, Bucket: cfg.Bucket,
		AccessKey: cfg.AccessKey, SecretKey: cfg.SecretKey,
	}, true
}

// ---- cabana's own state ----

func (s *Server) stateDB() (deploy.StateDB, bool) {
	if s.cfg.StateDBAddr == "" {
		return deploy.StateDB{}, false
	}
	host, port, found := strings.Cut(s.cfg.StateDBAddr, ":")
	if !found {
		port = "5432"
	}
	return deploy.StateDB{
		Host: host, Port: port, User: s.cfg.StateDBUser,
		Password: s.cfg.StateDBPassword, Name: s.cfg.StateDBName,
		Image: s.cfg.StateDBImage,
	}, true
}

func (s *Server) handleListStateBackups(w http.ResponseWriter, r *http.Request) {
	backups, err := s.store.ListStateBackups(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not list state backups")
		return
	}
	_, available := s.stateDB()
	writeJSON(w, http.StatusOK, map[string]any{"backups": backups, "available": available})
}

func (s *Server) handleCreateStateBackup(w http.ResponseWriter, r *http.Request) {
	db, ok := s.stateDB()
	if !ok {
		writeErr(w, http.StatusConflict, "state_backup_unavailable",
			"state backup is not configured on this install")
		return
	}
	cfg, configured, err := s.store.BackupConfig(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not read backup config")
		return
	}
	if !configured {
		writeErr(w, http.StatusConflict, "no_destination", "set a backup destination first")
		return
	}
	s3 := deploy.S3Config{
		Endpoint: cfg.Endpoint, Region: cfg.Region, Bucket: cfg.Bucket,
		AccessKey: cfg.AccessKey, SecretKey: cfg.SecretKey,
	}

	objectKey := fmt.Sprintf("cabana-state/%s.dump", domain.NewID())
	backup, err := s.store.CreateStateBackup(r.Context(), objectKey)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not record state backup")
		return
	}

	go s.runStateBackup(context.WithoutCancel(r.Context()), db, s3, backup)
	writeJSON(w, http.StatusAccepted, backup)
}

func (s *Server) runStateBackup(ctx context.Context, db deploy.StateDB, s3 deploy.S3Config, backup domain.StateBackup) {
	log := slog.With("state_backup", backup.ID)
	size, err := s.deployer.BackupState(ctx, db, s3, backup.ObjectKey)
	if err != nil {
		log.Error("state backup failed", "error", err)
		if e := s.store.FinishStateBackup(ctx, backup.ID, domain.BackupFailed, 0, err.Error()); e != nil {
			log.Error("record state backup failure", "error", e)
		}
		return
	}
	if err := s.store.FinishStateBackup(ctx, backup.ID, domain.BackupSucceeded, size, ""); err != nil {
		log.Error("record state backup success", "error", err)
	}
	log.Info("state backup succeeded", "bytes", size)
}

// RunBackupScheduler triggers due scheduled backups until ctx is cancelled. It
// runs the same path as a manual backup, so a scheduled one is recorded and
// pruned identically. Due-ness is computed from the last stored backup, so a
// cabana restart never double-runs or permanently misses a window.
func (s *Server) RunBackupScheduler(ctx context.Context) {
	// Run once at start as well as on each tick, so a window that fell during
	// downtime is caught on the next boot rather than skipped.
	s.runDueBackups(ctx)

	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDueBackups(ctx)
		}
	}
}

func (s *Server) runDueBackups(ctx context.Context) {
	scheduled, err := s.store.ScheduledServices(ctx)
	if err != nil {
		slog.Warn("scheduler: list services", "error", err)
		return
	}
	cfg, configured, err := s.store.BackupConfig(ctx)
	if err != nil || !configured {
		return
	}
	s3 := deploy.S3Config{
		Endpoint: cfg.Endpoint, Region: cfg.Region, Bucket: cfg.Bucket,
		AccessKey: cfg.AccessKey, SecretKey: cfg.SecretKey,
	}

	now := time.Now().UTC()
	for _, ss := range scheduled {
		if !backupDue(ss, now) {
			continue
		}
		tmpl, ok := s.catalog.Get(ss.Service.Template)
		if !ok || !tmpl.Backupable() {
			continue
		}
		slog.Info("scheduled backup due", "service", ss.Service.Name)
		if err := s.startBackup(ctx, ss.Service, tmpl, s3); err != nil {
			slog.Warn("scheduled backup", "service", ss.Service.Name, "error", err)
		}
	}
}

// backupDue decides whether a scheduled service should back up now. The interval
// checks (20h, 6d) are shorter than the period so a slightly late tick still
// fires, and long enough that the same window never triggers twice.
func backupDue(ss store.ScheduledService, now time.Time) bool {
	if int(ss.Service.BackupHour) != now.Hour() {
		return false
	}
	fresh := func(within time.Duration) bool {
		return ss.LastBackup != nil && now.Sub(*ss.LastBackup) < within
	}
	switch ss.Service.BackupSchedule {
	case domain.BackupDaily:
		return !fresh(20 * time.Hour)
	case domain.BackupWeekly:
		return now.Weekday() == time.Sunday && !fresh(6*24*time.Hour)
	}
	return false
}
