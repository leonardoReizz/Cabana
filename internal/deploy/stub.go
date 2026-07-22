package deploy

import (
	"context"
	"fmt"
	"time"

	"github.com/leonardoreis/cabana/internal/catalog"
	"github.com/leonardoreis/cabana/internal/domain"
)

// Stub is a Deployer that fakes a build without touching a cluster. It exists so
// the dashboard can be built and demoed against the real API contract before the
// K3s deployer lands, and so tests never need a cluster.
type Stub struct{}

func NewStub() *Stub { return &Stub{} }

var stubSteps = []string{
	"cloning repository",
	"detecting Dockerfile",
	"building image",
	"pushing to local registry",
	"applying kubernetes manifests",
	"waiting for rollout",
}

func (s *Stub) Deploy(ctx context.Context, req Request) error {
	for _, step := range stubSteps {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
		_ = step
	}
	return nil
}

func (s *Stub) Destroy(ctx context.Context, app domain.App) error { return nil }

func (s *Stub) Hostname(app domain.App) string {
	if app.Hostname != "" {
		return app.Hostname
	}
	return app.Name + ".stub.local"
}

func (s *Stub) StreamLogs(ctx context.Context, app domain.App, out chan<- LogLine) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	out <- LogLine{Timestamp: time.Now(), Source: "cabana",
		Text: fmt.Sprintf("attached to %s (stub deployer — no real workload yet)", app.Name)}

	for i := 1; ; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case t := <-ticker.C:
			out <- LogLine{Timestamp: t, Source: "app",
				Text: fmt.Sprintf("heartbeat %d from %s", i, app.Name)}
		}
	}
}

func (s *Stub) ProvisionService(
	ctx context.Context,
	svc domain.Service,
	tmpl catalog.Template,
	secrets map[string]string,
	deps []ServiceDependency,
) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		return nil
	}
}

func (s *Stub) DestroyService(ctx context.Context, svc domain.Service) error { return nil }

func (s *Stub) StreamServiceLogs(ctx context.Context, svc domain.Service, out chan<- LogLine) error {
	out <- LogLine{Timestamp: time.Now(), Source: "cabana",
		Text: fmt.Sprintf("attached to %s (stub deployer — no real service)", svc.Name)}
	<-ctx.Done()
	return ctx.Err()
}

func (s *Stub) Instances(ctx context.Context, app domain.App) ([]domain.Instance, error) {
	return []domain.Instance{{
		Name: app.Name + "-stub", Status: domain.InstanceRunning, Ready: true,
	}}, nil
}

func (s *Stub) ImageRef(app domain.App, deployID string) string {
	return "stub/" + app.Name + ":" + deployID
}

func (s *Stub) AppURL(app domain.App) string {
	host := s.Hostname(app)
	if host == "" {
		return ""
	}
	return "http://" + host
}

func (s *Stub) BackupService(ctx context.Context, svc domain.Service, tmpl catalog.Template, s3 S3Config, objectKey string) (int64, error) {
	return 1024, nil
}

func (s *Stub) RestoreService(ctx context.Context, svc domain.Service, tmpl catalog.Template, s3 S3Config, objectKey string) error {
	return nil
}

func (s *Stub) PruneObjects(ctx context.Context, svc domain.Service, s3 S3Config, keys []string) error {
	return nil
}

func (s *Stub) BackupState(ctx context.Context, db StateDB, s3 S3Config, objectKey string) (int64, error) {
	return 2048, nil
}
