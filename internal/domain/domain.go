package domain

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppStatus is the lifecycle of a user application as cabana understands it.
// It is deliberately coarse: the granular truth lives in Kubernetes, this is
// what the dashboard renders as a badge.
type AppStatus string

const (
	AppStatusNew       AppStatus = "new"
	AppStatusBuilding  AppStatus = "building"
	AppStatusDeploying AppStatus = "deploying"
	AppStatusRunning   AppStatus = "running"
	AppStatusFailed    AppStatus = "failed"
	AppStatusStopped   AppStatus = "stopped"
)

type App struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	RepoURL  string    `json:"repo_url"`
	GitRef   string    `json:"git_ref"`
	Status   AppStatus `json:"status"`
	Hostname string    `json:"hostname"`
	// URL is the hostname with the scheme that actually applies, filled in by
	// the API rather than stored: it depends on whether TLS is configured now,
	// not on what was true when the app was created.
	URL      string `json:"url,omitempty"`
	Replicas int32  `json:"replicas"`
	Health   Health `json:"health"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HealthType selects which kind of probe Kubernetes runs.
type HealthType string

const (
	// HealthTCP only asks whether something accepts a connection. It is the
	// default because cabana cannot assume an app has an HTTP health endpoint.
	HealthTCP HealthType = "tcp"
	// HealthHTTP additionally requires a 2xx, which catches an app that is
	// listening but broken.
	HealthHTTP HealthType = "http"
)

type Health struct {
	Type HealthType `json:"type"`
	Path string     `json:"path"`
	// InitialDelaySeconds is the grace period before the first check. An app
	// with slow startup needs this raised, or Kubernetes restarts it before it
	// ever becomes ready.
	InitialDelaySeconds int32 `json:"initial_delay_seconds"`
	// PeriodSeconds is the gap between checks. It sets how quickly a failure is
	// noticed: with the default three consecutive failures before a replica is
	// pulled, a 10s period means roughly 30s to react.
	PeriodSeconds int32 `json:"period_seconds"`
}

// ProbeTimeout is how long a single check may take.
//
// Kubernetes defaults this to one second, which is aggressive for an HTTP
// endpoint that touches a database — under load it times out, readiness flaps,
// and the replica leaves the load balancer for no real fault. Five seconds is
// more forgiving, and it never exceeds the period, which would let checks
// overlap.
func (h Health) ProbeTimeout() int32 {
	const maxTimeout = 5
	if h.PeriodSeconds > 0 && h.PeriodSeconds < maxTimeout {
		return h.PeriodSeconds
	}
	return maxTimeout
}

// MaxReplicas is a guard against a typo turning into a node-filling number of
// pods on a single-machine install.
const MaxReplicas = 20

func ValidateReplicas(n int32) error {
	if n < 1 {
		return errors.New("replicas must be at least 1")
	}
	if n > MaxReplicas {
		return fmt.Errorf("replicas must be at most %d", MaxReplicas)
	}
	return nil
}

func ValidateHealth(h Health) error {
	switch h.Type {
	case HealthTCP:
	case HealthHTTP:
		if h.Path == "" || h.Path[0] != '/' {
			return errors.New("health path must start with /")
		}
		if len(h.Path) > 255 {
			return errors.New("health path is too long")
		}
	default:
		return fmt.Errorf("health type must be %q or %q", HealthTCP, HealthHTTP)
	}
	if h.InitialDelaySeconds < 0 || h.InitialDelaySeconds > 600 {
		return errors.New("initial delay must be between 0 and 600 seconds")
	}
	if h.PeriodSeconds < 1 || h.PeriodSeconds > 300 {
		return errors.New("check interval must be between 1 and 300 seconds")
	}
	return nil
}

// InstanceStatus is the health of one running replica, as the dashboard shows it.
type InstanceStatus string

const (
	InstanceRunning   InstanceStatus = "running"
	InstanceStarting  InstanceStatus = "starting"
	InstancePending   InstanceStatus = "pending"
	InstanceCrashLoop InstanceStatus = "crashloop"
	InstanceOOM       InstanceStatus = "oom"
	InstanceFailed    InstanceStatus = "failed"
)

// Instance is one replica. This is what turns the app page into a health panel:
// not just "running", but which replica, restarted how often, and why.
type Instance struct {
	Name     string         `json:"name"`
	Status   InstanceStatus `json:"status"`
	Ready    bool           `json:"ready"`
	Restarts int32          `json:"restarts"`
	// Reason carries the Kubernetes explanation (CrashLoopBackOff, OOMKilled)
	// when there is one.
	Reason    string     `json:"reason,omitempty"`
	Message   string     `json:"message,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
}

type DeployStatus string

const (
	DeployStatusQueued    DeployStatus = "queued"
	DeployStatusBuilding  DeployStatus = "building"
	DeployStatusDeploying DeployStatus = "deploying"
	DeployStatusSucceeded DeployStatus = "succeeded"
	DeployStatusFailed    DeployStatus = "failed"
)

type Deploy struct {
	ID     string       `json:"id"`
	AppID  string       `json:"app_id"`
	Status DeployStatus `json:"status"`
	GitRef string       `json:"git_ref"`
	// Image is what this deploy produced, recorded so a rollback refers to
	// exactly what ran.
	Image string `json:"image,omitempty"`
	// RolledBackFrom names the deploy this one restored, when it is a rollback.
	RolledBackFrom *string    `json:"rolled_back_from,omitempty"`
	CommitSHA      string     `json:"commit_sha,omitempty"`
	Message        string     `json:"message,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// NewID returns a short, URL-safe, lowercase identifier. Not a UUID on purpose:
// these end up in hostnames and kubernetes object names, where 26 chars of
// base32 is friendlier than 36 chars of hyphenated hex.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("cabana: entropy source failed: " + err.Error())
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

var ErrInvalidName = errors.New("name must be 3-30 chars of lowercase letters, digits or hyphens, and start with a letter")

// ValidateAppName enforces DNS-label rules up front, because the name becomes a
// kubernetes object name and a hostname label. Rejecting here beats a confusing
// failure three layers down at deploy time.
func ValidateAppName(name string) error {
	if len(name) < 3 || len(name) > 30 {
		return ErrInvalidName
	}
	if name[0] < 'a' || name[0] > 'z' {
		return ErrInvalidName
	}
	for _, r := range name {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if !isLower && !isDigit && r != '-' {
			return ErrInvalidName
		}
	}
	if strings.HasSuffix(name, "-") {
		return ErrInvalidName
	}
	return nil
}

// ServiceStatus mirrors AppStatus: coarse, and only as detailed as the
// dashboard needs to render a badge.
type ServiceStatus string

const (
	ServiceStatusProvisioning ServiceStatus = "provisioning"
	ServiceStatusRunning      ServiceStatus = "running"
	ServiceStatusFailed       ServiceStatus = "failed"
)

// Service is one installed instance of a catalogue template.
type Service struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Template string        `json:"template"`
	Status   ServiceStatus `json:"status"`
	// Image is the exact reference running, e.g. "redis:7.2-alpine".
	Image string `json:"image"`
	// BackupSchedule is "off", "daily" or "weekly"; BackupHour is the UTC hour
	// (0-23) it runs around.
	BackupSchedule string `json:"backup_schedule"`
	BackupHour     int32  `json:"backup_hour"`
	// Host and Port are the cluster-internal address linked apps connect to.
	Host      string    `json:"host"`
	Port      int32     `json:"port"`
	Message   string    `json:"message,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ServiceLink is a service wired into an app.
type ServiceLink struct {
	ServiceID   string    `json:"service_id"`
	ServiceName string    `json:"service_name"`
	Template    string    `json:"template"`
	EnvVar      string    `json:"env_var"`
	CreatedAt   time.Time `json:"created_at"`
}

// ValidateServiceName applies the same DNS-label rules as app names: a service
// name becomes a Kubernetes object name and an in-cluster hostname.
func ValidateServiceName(name string) error {
	return ValidateAppName(name)
}

// BackupConfig is the S3-compatible destination backups are shipped to. One per
// install, S3-compatible so it works with R2, B2, MinIO and AWS alike.
type BackupConfig struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	// SecretKey is write-only over the API: accepted on save, never returned.
	SecretKey string `json:"secret_key,omitempty"`
	// RetentionCount is how many succeeded backups to keep per service; 0 keeps
	// all of them.
	RetentionCount int32 `json:"retention_count"`
	Configured     bool  `json:"configured"`
}

type BackupStatus string

const (
	BackupRunning   BackupStatus = "running"
	BackupSucceeded BackupStatus = "succeeded"
	BackupFailed    BackupStatus = "failed"
)

// Backup is one point-in-time copy of a service's data in the S3 destination.
type Backup struct {
	ID          string       `json:"id"`
	ServiceID   string       `json:"service_id"`
	ServiceName string       `json:"service_name"`
	Template    string       `json:"template"`
	Status      BackupStatus `json:"status"`
	ObjectKey   string       `json:"object_key"`
	SizeBytes   int64        `json:"size_bytes"`
	Error       string       `json:"error,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	FinishedAt  *time.Time   `json:"finished_at,omitempty"`
}

// Backup schedules.
const (
	BackupOff    = "off"
	BackupDaily  = "daily"
	BackupWeekly = "weekly"
)

func ValidateBackupSchedule(schedule string, hour int32) error {
	switch schedule {
	case BackupOff, BackupDaily, BackupWeekly:
	default:
		return fmt.Errorf("schedule must be off, daily or weekly")
	}
	if hour < 0 || hour > 23 {
		return errors.New("hour must be between 0 and 23")
	}
	return nil
}

// StateBackup is one copy of cabana's own database in the S3 destination.
type StateBackup struct {
	ID         string       `json:"id"`
	Status     BackupStatus `json:"status"`
	ObjectKey  string       `json:"object_key"`
	SizeBytes  int64        `json:"size_bytes"`
	Error      string       `json:"error,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	FinishedAt *time.Time   `json:"finished_at,omitempty"`
}
