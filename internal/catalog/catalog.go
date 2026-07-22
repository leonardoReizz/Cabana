// Package catalog holds the service templates cabana can install.
//
// Templates are the YAML files in templates/, embedded at build time. Adding a
// service to the catalogue is adding a file there — no Go code changes, and no
// network access at runtime. That is what makes the catalogue open to
// contribution without opening the codebase.
package catalog

import (
	"bytes"
	"crypto/rand"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

//go:embed templates/*.yaml
var templatesFS embed.FS

// Secret is a credential generated once, when a service is installed.
type Secret struct {
	Key string `yaml:"key"`
	// Generate names the strategy. Only "password" exists today; the field
	// exists so adding, say, an RSA keypair does not change the file format.
	Generate string `yaml:"generate"`
}

type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
	// FromSecret references a Secret.Key rather than carrying a literal, so a
	// template file never contains a credential.
	FromSecret string `yaml:"from_secret"`
}

type Readiness struct {
	Exec     []string `yaml:"exec"`
	HTTPPath string   `yaml:"http_path"`
	// Port the check runs against. Defaults to the primary one, but a service
	// whose health endpoint lives elsewhere needs it: NATS serves /healthz on
	// its monitoring port and Mailpit /readyz on its web port, neither of which
	// is the port clients connect to. Probing the wrong port makes a healthy
	// service look dead.
	Port int32 `yaml:"port"`
}

// Connection describes what a linked app receives.
type Connection struct {
	EnvVar   string `yaml:"env_var"`
	Template string `yaml:"template"`
}

// Port is an additional exposed port beyond the primary one.
type Port struct {
	Name string `yaml:"name"`
	Port int32  `yaml:"port"`
}

// Requirement is another catalogue service this one needs to run — Metabase
// needs a Postgres, and without one it falls back to a throwaway embedded
// database. cabana provisions the requirement alongside the service, in the
// same namespace, and wires its connection details into the service's env.
type Requirement struct {
	// Name is a local alias. The requirement's workload is named
	// "<service>-<name>", so a Metabase named "reports" gets a "reports-db".
	Name string `yaml:"name"`
	// Template is the catalogue template to provision as the dependency.
	Template string `yaml:"template"`
	// Inject maps pieces of the dependency's connection into the service's env.
	Inject []Inject `yaml:"inject"`
}

// Inject sets one env var on the depending service from the dependency's
// connection details.
type Inject struct {
	// Env is the variable to set on the depending service's container.
	Env string `yaml:"env"`
	// From selects what to inject: "connection" (the full rendered string),
	// "host", "port" or "password".
	From string `yaml:"from"`
}

// Workload kinds.
const (
	WorkloadDeployment  = "deployment"
	WorkloadStatefulSet = "statefulset"
)

type Template struct {
	Name        string `yaml:"name"`
	DisplayName string `yaml:"display_name"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"`

	Image string `yaml:"image"`
	// Port is the primary one: what connection strings point at.
	Port int32 `yaml:"port"`
	// ExtraPorts are also exposed on the Service — a web console, a second
	// protocol. Mailpit listens for SMTP and serves a UI; MinIO has an API and
	// a console. Without this the second port is simply unreachable.
	ExtraPorts []Port `yaml:"extra_ports"`

	// Workload is "deployment" (the default) or "statefulset". A StatefulSet
	// gives a stable pod identity and DNS name, which anything that advertises
	// its own address needs — Kafka tells clients where to reconnect, and that
	// address has to survive a restart.
	Workload string `yaml:"workload"`

	Secrets []Secret `yaml:"secrets"`
	Env     []EnvVar `yaml:"env"`
	Command []string `yaml:"command"`

	// Requires lists other catalogue services this one depends on. Kept to one
	// level: a dependency may not itself have requirements, which bounds
	// provisioning to a predictable two-workload shape.
	Requires []Requirement `yaml:"requires"`

	// Storage and MountPath are optional. Some services hold nothing worth
	// keeping — a cache, a UI, a test mail catcher — and forcing a volume on
	// them wastes disk and turns a restart into a slower operation than it
	// needs to be.
	Storage   string    `yaml:"storage"`
	MountPath string    `yaml:"mount_path"`
	Readiness Readiness `yaml:"readiness"`

	Connection Connection `yaml:"connection"`

	// Backup declares how to dump and restore the service. A logical dump
	// (pg_dump, mysqldump) rather than a raw volume copy, because that is the
	// only kind that reliably restores into a running database.
	Backup Backup `yaml:"backup"`
}

// Backup holds the commands that dump and restore a service. Both run in the
// service's own image — which already carries the right client tools — and both
// see $BACKUP_HOST (the service's address) and $BACKUP_FILE (the dump path).
type Backup struct {
	Dump    []string `yaml:"dump"`
	Restore []string `yaml:"restore"`
}

// Backupable reports whether the template knows how to back itself up.
func (t Template) Backupable() bool {
	return len(t.Backup.Dump) > 0 && len(t.Backup.Restore) > 0
}

// ReadinessPort is the port the health check runs against.
func (t Template) ReadinessPort() int32 {
	if t.Readiness.Port != 0 {
		return t.Readiness.Port
	}
	return t.Port
}

// Persistent reports whether the service keeps data across restarts.
func (t Template) Persistent() bool {
	return t.Storage != "" && t.MountPath != ""
}

// IsStatefulSet reports whether the service runs as a StatefulSet.
func (t Template) IsStatefulSet() bool {
	return t.Workload == WorkloadStatefulSet
}

// ConnectionVars are the values available to a Connection.Template.
type ConnectionVars struct {
	Host     string
	Port     int32
	Password string
	// PodHost is the stable per-pod DNS name of a StatefulSet service. Kafka
	// advertises this address to clients, so its connection string must point
	// at it rather than at the load-balanced service host.
	PodHost string
}

// RenderConnection produces the connection string injected into a linked app.
func (t Template) RenderConnection(vars ConnectionVars) (string, error) {
	tmpl, err := template.New("conn").Parse(t.Connection.Template)
	if err != nil {
		return "", fmt.Errorf("parse connection template for %s: %w", t.Name, err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, vars); err != nil {
		return "", fmt.Errorf("render connection for %s: %w", t.Name, err)
	}
	return out.String(), nil
}

// GenerateSecrets produces one value per declared secret.
func (t Template) GenerateSecrets() (map[string]string, error) {
	out := make(map[string]string, len(t.Secrets))
	for _, s := range t.Secrets {
		switch s.Generate {
		case "password", "":
			v, err := randomPassword()
			if err != nil {
				return nil, err
			}
			out[s.Key] = v
		default:
			return nil, fmt.Errorf("template %s: unknown generator %q", t.Name, s.Generate)
		}
	}
	return out, nil
}

// passwordAlphabet excludes punctuation on purpose: these values end up inside
// connection URLs, where anything requiring percent-encoding is a latent bug in
// whichever client forgets to escape it.
const passwordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomPassword() (string, error) {
	const length = 28
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = passwordAlphabet[int(b[i])%len(passwordAlphabet)]
	}
	return string(b), nil
}

// Catalog is the parsed, validated set of templates.
type Catalog struct {
	byName    map[string]Template
	templates []Template
}

// All returns every template, ordered by category then display name so the
// dashboard's grid is stable between reloads.
func (c *Catalog) All() []Template { return c.templates }

func (c *Catalog) Get(name string) (Template, bool) {
	t, ok := c.byName[name]
	return t, ok
}

// Load parses and validates every embedded template. It fails loudly at boot
// rather than when a user clicks Install: a malformed template is a packaging
// error, and discovering it in production is too late.
func Load() (*Catalog, error) {
	entries, err := fs.ReadDir(templatesFS, "templates")
	if err != nil {
		return nil, err
	}

	c := &Catalog{byName: make(map[string]Template, len(entries))}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		raw, err := templatesFS.ReadFile("templates/" + entry.Name())
		if err != nil {
			return nil, err
		}

		var t Template
		if err := decodeStrict(raw, &t); err != nil {
			return nil, fmt.Errorf("parse %s: %w", entry.Name(), err)
		}
		if err := t.validate(); err != nil {
			return nil, fmt.Errorf("template %s: %w", entry.Name(), err)
		}
		if _, dup := c.byName[t.Name]; dup {
			return nil, fmt.Errorf("duplicate template name %q", t.Name)
		}

		c.byName[t.Name] = t
		c.templates = append(c.templates, t)
	}

	// Requirements reference other templates, so they can only be checked once
	// the whole catalogue is loaded — a per-template pass would reject a valid
	// forward reference.
	for _, t := range c.templates {
		for _, req := range t.Requires {
			dep, ok := c.byName[req.Template]
			if !ok {
				return nil, fmt.Errorf(
					"template %s requires %q, which is not in the catalogue", t.Name, req.Template)
			}
			// One level only: a dependency that itself had dependencies would
			// turn provisioning into an unbounded graph walk.
			if len(dep.Requires) > 0 {
				return nil, fmt.Errorf(
					"template %s requires %s, which itself has requirements — only one level is allowed",
					t.Name, req.Template)
			}
		}
	}

	sort.Slice(c.templates, func(i, j int) bool {
		if c.templates[i].Category != c.templates[j].Category {
			return c.templates[i].Category < c.templates[j].Category
		}
		return c.templates[i].DisplayName < c.templates[j].DisplayName
	})
	return c, nil
}

// InjectValue picks the piece of a dependency's connection an Inject asks for.
func (in Inject) InjectValue(vars ConnectionVars, connection string) string {
	switch in.From {
	case "host":
		return vars.Host
	case "port":
		return fmt.Sprint(vars.Port)
	case "password":
		return vars.Password
	default: // "connection"
		return connection
	}
}

func (t Template) validate() error {
	if t.Name == "" || t.DisplayName == "" {
		return fmt.Errorf("name and display_name are required")
	}
	if t.Image == "" {
		return fmt.Errorf("image is required")
	}
	if t.Port == 0 {
		return fmt.Errorf("port is required")
	}
	// Storage is optional, but half-specified storage is a mistake rather than
	// a choice: a mount path with no size, or a size with nowhere to mount it,
	// silently produces a service that does not persist.
	if (t.Storage == "") != (t.MountPath == "") {
		return fmt.Errorf("storage and mount_path must be set together, or neither")
	}
	for _, p := range t.ExtraPorts {
		if p.Name == "" || p.Port == 0 {
			return fmt.Errorf("every extra port needs a name and a port")
		}
		if p.Port == t.Port {
			return fmt.Errorf("extra port %s duplicates the primary port", p.Name)
		}
	}
	// A probe port that is not actually exposed would fail forever.
	if rp := t.Readiness.Port; rp != 0 && rp != t.Port {
		exposed := false
		for _, p := range t.ExtraPorts {
			if p.Port == rp {
				exposed = true
			}
		}
		if !exposed {
			return fmt.Errorf("readiness port %d is not exposed", rp)
		}
	}
	if t.Connection.EnvVar == "" || t.Connection.Template == "" {
		return fmt.Errorf("connection.env_var and connection.template are required")
	}

	switch t.Workload {
	case "", WorkloadDeployment, WorkloadStatefulSet:
	default:
		return fmt.Errorf("workload must be %q or %q", WorkloadDeployment, WorkloadStatefulSet)
	}

	for _, req := range t.Requires {
		if req.Name == "" || req.Template == "" {
			return fmt.Errorf("every requirement needs a name and a template")
		}
		for _, in := range req.Inject {
			if in.Env == "" {
				return fmt.Errorf("requirement %s has an inject with no env", req.Name)
			}
			switch in.From {
			case "connection", "host", "port", "password":
			default:
				return fmt.Errorf(
					"requirement %s inject %s: from must be connection, host, port or password",
					req.Name, in.Env)
			}
		}
	}

	declared := make(map[string]bool, len(t.Secrets))
	for _, s := range t.Secrets {
		declared[s.Key] = true
	}
	// A from_secret pointing at nothing would produce an empty password and a
	// service that silently accepts no authentication.
	for _, e := range t.Env {
		if e.FromSecret != "" && !declared[e.FromSecret] {
			return fmt.Errorf("env %s references undeclared secret %q", e.Name, e.FromSecret)
		}
		if e.FromSecret == "" && e.Value == "" {
			return fmt.Errorf("env %s has neither value nor from_secret", e.Name)
		}
	}

	// Rendering with a dummy value proves the template parses and references
	// only fields that exist.
	if _, err := t.RenderConnection(ConnectionVars{Host: "h", Port: t.Port, Password: "p"}); err != nil {
		return err
	}
	return nil
}

// decodeStrict rejects unknown fields, so a typo in a contributed template is a
// loud parse error rather than a setting that silently does nothing.
func decodeStrict(raw []byte, t *Template) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	return dec.Decode(t)
}

// Repository is the image without its tag, e.g. "redis" from "redis:7.4-alpine".
//
// Only the tag is ever user-supplied; the repository always comes from the
// template. That is the whole security boundary of the version field — a user
// can pick which Redis, never which image.
func (t Template) Repository() string {
	repo, _ := splitImage(t.Image)
	return repo
}

// DefaultVersion is the tag the template pins, offered as the placeholder so a
// user who does not care gets a version that is known to work.
func (t Template) DefaultVersion() string {
	_, tag := splitImage(t.Image)
	return tag
}

// ImageFor resolves the image to run. An empty version means the template's own.
func (t Template) ImageFor(version string) (string, error) {
	if version == "" {
		return t.Image, nil
	}
	if err := ValidateVersion(version); err != nil {
		return "", err
	}
	return t.Repository() + ":" + version, nil
}

// splitImage separates repository from tag, tolerating a registry host with a
// port — "registry:5000/redis:7" must not split on the first colon.
func splitImage(image string) (repo, tag string) {
	slash := strings.LastIndex(image, "/")
	colon := strings.LastIndex(image, ":")
	if colon <= slash {
		return image, "latest"
	}
	return image[:colon], image[colon+1:]
}

// ValidateVersion enforces Docker's tag grammar. Rejecting here means a typo
// becomes an immediate, readable error instead of a pod stuck in
// ImagePullBackOff several minutes later.
func ValidateVersion(version string) error {
	if version == "" {
		return nil
	}
	if len(version) > 128 {
		return fmt.Errorf("version is too long (max 128 characters)")
	}
	first := version[0]
	if !isAlphanumeric(first) && first != '_' {
		return fmt.Errorf("version must start with a letter, digit or underscore")
	}
	for i := 0; i < len(version); i++ {
		c := version[i]
		if !isAlphanumeric(c) && c != '.' && c != '-' && c != '_' {
			return fmt.Errorf(
				"version may only contain letters, digits, dots, hyphens and underscores")
		}
	}
	return nil
}

func isAlphanumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
