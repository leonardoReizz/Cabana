package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/leonardoreis/cabana/internal/catalog"
	"github.com/leonardoreis/cabana/internal/config"
	"github.com/leonardoreis/cabana/internal/deploy"
	"github.com/leonardoreis/cabana/internal/httpapi"
	"github.com/leonardoreis/cabana/internal/k8s"
	"github.com/leonardoreis/cabana/internal/logstream"
	"github.com/leonardoreis/cabana/internal/store"
	"github.com/leonardoreis/cabana/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := bootstrapAdmin(ctx, st, cfg); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}

	go reapSessions(ctx, st)

	deployer, err := newDeployer(cfg, st)
	if err != nil {
		return err
	}

	// Loaded at boot so a malformed template fails the process rather than a
	// user's click on Install.
	cat, err := catalog.Load()
	if err != nil {
		return fmt.Errorf("load service catalogue: %w", err)
	}
	slog.Info("service catalogue loaded", "templates", len(cat.All()))

	api := httpapi.New(cfg, st, deployer, cat)

	// Scheduled backups run in the background for the life of the process.
	go api.RunBackupScheduler(ctx)

	// The dashboard is served from the same binary and the same origin, so the
	// installer has one thing to run and there is no CORS in production.
	root := web.Handler(api)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the SSE log stream is long-lived by design.
		IdleTimeout: 120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("cabana listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func newDeployer(cfg config.Config, st *store.Store) (deploy.Deployer, error) {
	if cfg.Deployer == "stub" {
		slog.Warn("using the stub deployer — apps will not actually run")
		return deploy.NewStub(), nil
	}

	client, err := k8s.Connect(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("connect to kubernetes: %w", err)
	}
	slog.Info("using the k3s deployer",
		"registry", cfg.Registry, "buildkit", cfg.BuildkitImage,
		"tls", issuerName(cfg))

	return deploy.NewK3s(client, logstream.New(), deploy.K3sOptions{
		Registry:      cfg.Registry,
		BuildkitImage: cfg.BuildkitImage,
		NodeIP:        cfg.NodeIP,
		TLSIssuer:     issuerName(cfg),
		Recorder:      st,
	}), nil
}

// issuerName maps the configured issuer to the ClusterIssuer the installer
// creates. Empty when TLS is off, which is what turns certificate requests off
// in the deployer.
func issuerName(cfg config.Config) string {
	if !cfg.TLSEnabled() {
		return ""
	}
	return "cabana-" + cfg.TLSIssuer
}

// bootstrapAdmin creates the single admin user on first boot and prints the
// credentials. This is the handoff between the installer and the browser: the
// password is shown exactly once, here.
func bootstrapAdmin(ctx context.Context, st *store.Store, cfg config.Config) error {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	password := cfg.BootstrapPassword
	generated := false
	if password == "" {
		password, err = randomPassword()
		if err != nil {
			return err
		}
		generated = true
	}

	hash, err := httpapi.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(ctx, "admin", hash); err != nil {
		return err
	}

	shown := password
	if !generated {
		shown = "(the value of CABANA_BOOTSTRAP_PASSWORD)"
	}
	fmt.Fprintf(os.Stderr, "\n"+
		"  ┌─ cabana is ready ─────────────────────────────\n"+
		"  │  url       http://localhost%s\n"+
		"  │  username  admin\n"+
		"  │  password  %s\n"+
		"  └───────────────────────────────────────────────\n\n", cfg.Addr, shown)
	return nil
}

func randomPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// reapSessions clears expired sessions periodically so the table does not grow
// without bound on a long-lived install.
func reapSessions(ctx context.Context, st *store.Store) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := st.DeleteExpiredSessions(ctx); err != nil {
				slog.Warn("reap sessions", "error", err)
			} else if n > 0 {
				slog.Info("reaped expired sessions", "count", n)
			}
		}
	}
}
