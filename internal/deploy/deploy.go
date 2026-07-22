// Package deploy defines the boundary between the cabana API and whatever
// actually runs user code. v0.1 ships a stub implementation; the K3s one slots
// in behind the same interface without touching the HTTP layer.
package deploy

import (
	"context"
	"time"

	"github.com/leonardoreis/cabana/internal/catalog"
	"github.com/leonardoreis/cabana/internal/domain"
)

// ServiceDependency is a catalogue service provisioned alongside another, in
// the same namespace, with its connection details wired into the depending
// service's environment. It exists in the cluster only — cabana's database
// knows just the main service, and namespace deletion cleans the dependency up.
type ServiceDependency struct {
	Name     string
	Template catalog.Template
	Secrets  map[string]string
	Inject   []catalog.Inject
}

type Request struct {
	App    domain.App
	Deploy domain.Deploy
	Env    map[string]string
	// BuildArgs are passed to the Dockerfile at build time. Distinct from Env,
	// which the container reads at run time.
	BuildArgs map[string]string
	// RollbackFrom names the deploy being restored. When set, the deployer
	// reuses Deploy.Image instead of building.
	RollbackFrom string
}

// BuildLogRecorder persists a deploy's build output once the build finishes.
// The deployer streams the same lines live, but the stream is in-memory and
// gone the moment it rotates — this is what makes yesterday's failed build
// still readable today.
type BuildLogRecorder interface {
	SaveDeployLog(ctx context.Context, deployID, content string) error
}

type LogLine struct {
	Timestamp time.Time `json:"ts"`
	Source    string    `json:"source"` // "build" | "app" | "cabana"
	Text      string    `json:"text"`
}

type Deployer interface {
	// Deploy builds the app from its git repo and rolls it out. It blocks until
	// the rollout settles or fails.
	Deploy(ctx context.Context, req Request) error

	// Destroy removes every resource belonging to the app. It must be safe to
	// call for an app that was never deployed.
	Destroy(ctx context.Context, app domain.App) error

	// StreamLogs pushes log lines onto out until ctx is cancelled. It owns
	// neither closing out nor draining it — the caller does both.
	StreamLogs(ctx context.Context, app domain.App, out chan<- LogLine) error

	// Hostname resolves where an app is actually reachable, which may differ
	// from App.Hostname when a fallback applies. The API persists the result so
	// the dashboard can show a working URL — without it a user has no way to
	// find their own app short of reaching for kubectl.
	Hostname(app domain.App) string

	// AppURL is Hostname with the scheme in force, so the dashboard links to
	// https:// only when certificates are actually being issued.
	AppURL(app domain.App) string

	// ProvisionService installs a catalogue template and blocks until it is
	// ready to accept connections. deps are bundled dependencies provisioned
	// alongside it, their connection details injected into its environment.
	ProvisionService(ctx context.Context, svc domain.Service, tmpl catalog.Template, secrets map[string]string, deps []ServiceDependency) error

	// DestroyService removes a service and its data. Safe to call for one that
	// was never successfully provisioned.
	DestroyService(ctx context.Context, svc domain.Service) error

	// BackupService dumps a service to S3 and returns the stored size in bytes.
	BackupService(ctx context.Context, svc domain.Service, tmpl catalog.Template, s3 S3Config, objectKey string) (int64, error)

	// RestoreService replays a dump from S3 into the running service. Destructive.
	RestoreService(ctx context.Context, svc domain.Service, tmpl catalog.Template, s3 S3Config, objectKey string) error

	// PruneObjects removes aged-out backup objects from S3.
	PruneObjects(ctx context.Context, svc domain.Service, s3 S3Config, keys []string) error

	// BackupState dumps cabana's own database to S3 and returns the stored size.
	BackupState(ctx context.Context, db StateDB, s3 S3Config, objectKey string) (int64, error)

	// StreamServiceLogs pushes a service's provisioning narration and container
	// output onto out until ctx is cancelled.
	StreamServiceLogs(ctx context.Context, svc domain.Service, out chan<- LogLine) error

	// ImageRef is the image a normal build of this deploy will produce. Exposed
	// so the API can record it, which is what later makes that deploy a
	// rollback target.
	ImageRef(app domain.App, deployID string) string

	// Instances reports the live state of every replica: what is running, what
	// is crashlooping, what was OOM-killed, and how often each has restarted.
	Instances(ctx context.Context, app domain.App) ([]domain.Instance, error)
}
