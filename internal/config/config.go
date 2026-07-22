package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config is resolved once at boot. Every knob is an env var so the installer can
// write a single systemd EnvironmentFile and never template a config file.
type Config struct {
	Addr        string
	DatabaseURL string

	// BootstrapPassword seeds the admin user on first boot. When empty, cabana
	// generates one and prints it — that is what the installer echoes to the user.
	BootstrapPassword string

	// BaseDomain is the wildcard domain apps are exposed under, e.g. "apps.example.com"
	// produces "myapp.apps.example.com". Empty falls back to sslip.io at deploy time.
	BaseDomain string

	// DevCORSOrigin allows the Vite dev server to call the API from another port.
	// Empty in production, where the dashboard is served from the same origin.
	DevCORSOrigin string

	// Deployer selects the backend that actually runs apps: "k3s" or "stub".
	// The stub exists so the dashboard can run without a cluster.
	Deployer string

	// Kubeconfig is only consulted when cabana runs outside the cluster, which
	// is the local development path. In production the ServiceAccount wins.
	Kubeconfig string

	// Registry is the in-cluster registry apps are pushed to and pulled from.
	// It must match the mirror configured in the node's registries.yaml.
	Registry string

	// BuildkitImage is pinned rather than floating on :latest, so a build that
	// worked yesterday still works today.
	BuildkitImage string

	// NodeIP backs the sslip.io hostname fallback when no domain is configured.
	NodeIP string

	// TLSIssuer selects the cert-manager ClusterIssuer apps get certificates
	// from: "letsencrypt", "letsencrypt-staging", "selfsigned", or empty for
	// plain HTTP.
	TLSIssuer string

	// ACMEEmail is where Let's Encrypt sends expiry warnings. Required by ACME,
	// so an empty one makes the Let's Encrypt issuers unusable.
	ACMEEmail string

	// StateDBAddr is the in-cluster host:port of cabana's own database, used by
	// the state-backup Job. It differs from DatabaseURL's host: cabana reaches
	// the database over a host-local NodePort a pod cannot use. Empty disables
	// state backup.
	StateDBAddr string
	// StateDBImage carries pg_dump for the state-backup Job.
	StateDBImage string

	// Parsed from DatabaseURL, so the state-backup Job authenticates with the
	// same credentials cabana itself uses.
	StateDBUser     string
	StateDBPassword string
	StateDBName     string
}

// TLSEnabled reports whether apps should be issued certificates.
//
// A real domain is required, not just an issuer: HTTP-01 validation needs the
// hostname to resolve publicly to this machine, and the sslip.io fallback —
// while it does resolve — is a shared domain whose Let's Encrypt rate limits
// are exhausted by everyone else using it. Requesting certificates for it
// produces failures that look like cabana's fault.
func (c Config) TLSEnabled() bool {
	return c.TLSIssuer != "" && c.BaseDomain != ""
}

func Load() (Config, error) {
	c := Config{
		// 7373 rather than a rounder number: 7000 is macOS AirPlay Receiver and
		// 8080 collides with half the apps a user might already be running.
		Addr:              env("CABANA_ADDR", ":7373"),
		DatabaseURL:       env("CABANA_DATABASE_URL", ""),
		BootstrapPassword: env("CABANA_BOOTSTRAP_PASSWORD", ""),
		BaseDomain:        strings.TrimPrefix(env("CABANA_BASE_DOMAIN", ""), "*."),
		DevCORSOrigin:     env("CABANA_DEV_CORS_ORIGIN", ""),
		Deployer:          env("CABANA_DEPLOYER", "k3s"),
		Kubeconfig:        env("CABANA_KUBECONFIG", ""),
		// The fully-qualified service name, not a short one: the BuildKit pod
		// pushes from a different namespace, where a bare name would not resolve.
		Registry: env("CABANA_REGISTRY",
			"cabana-registry.cabana-system.svc.cluster.local:5000"),
		BuildkitImage: env("CABANA_BUILDKIT_IMAGE", "moby/buildkit:v0.19.0-rootless"),
		NodeIP:        env("CABANA_NODE_IP", ""),
		TLSIssuer:     env("CABANA_TLS_ISSUER", ""),
		ACMEEmail:     env("CABANA_ACME_EMAIL", ""),
		StateDBAddr:   env("CABANA_STATE_DB_ADDR", ""),
		StateDBImage:  env("CABANA_STATE_DB_IMAGE", "postgres:17-alpine"),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("CABANA_DATABASE_URL is required")
	}

	// The state-backup Job authenticates with the same credentials cabana uses,
	// parsed from the DSN. Opt-in via StateDBAddr, so a parse miss here only
	// matters if state backup is actually enabled.
	if u, err := url.Parse(c.DatabaseURL); err == nil {
		c.StateDBUser = u.User.Username()
		c.StateDBPassword, _ = u.User.Password()
		c.StateDBName = strings.TrimPrefix(u.Path, "/")
	}
	if c.Deployer != "k3s" && c.Deployer != "stub" {
		return c, fmt.Errorf("CABANA_DEPLOYER must be \"k3s\" or \"stub\", got %q", c.Deployer)
	}

	switch c.TLSIssuer {
	case "", "selfsigned", "letsencrypt", "letsencrypt-staging":
	default:
		return c, fmt.Errorf(
			"CABANA_TLS_ISSUER must be empty, \"selfsigned\", \"letsencrypt\" or "+
				"\"letsencrypt-staging\", got %q", c.TLSIssuer)
	}
	// Caught at boot rather than at the first deploy, where it would surface as
	// a certificate that never issues.
	if strings.HasPrefix(c.TLSIssuer, "letsencrypt") && c.ACMEEmail == "" {
		return c, fmt.Errorf("CABANA_ACME_EMAIL is required when CABANA_TLS_ISSUER is %q", c.TLSIssuer)
	}
	if c.TLSIssuer != "" && c.BaseDomain == "" {
		return c, fmt.Errorf(
			"CABANA_TLS_ISSUER needs CABANA_BASE_DOMAIN: certificates cannot be " +
				"issued for the sslip.io fallback")
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
