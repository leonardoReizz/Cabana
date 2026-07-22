# cabana

Your own platform, on your own machine. Point it at a Git repository and it
builds, runs and exposes your app — no terminal after the installer.

Status: **v0.1 feature-complete.** A clean Linux host goes from one command to a
running, routed app without touching a terminal again. TLS for custom domains
is the last piece (see [Roadmap](#roadmap-position)).

## Install

```sh
curl -sfL https://get.cabana.dev | sudo bash
```

Provisions K3s, an in-cluster image registry, Postgres and cabana itself, then
prints the dashboard URL and the initial password. Takes a few minutes on a
fresh host.

To serve apps on your own domain, point a wildcard `*.example.com` at the host
and pass it in:

```sh
curl -sfL https://get.cabana.dev | sudo CABANA_BASE_DOMAIN=example.com bash
```

For automatic HTTPS, add an issuer and an email:

```sh
curl -sfL https://get.cabana.dev | sudo \
  CABANA_BASE_DOMAIN=example.com \
  CABANA_TLS_ISSUER=letsencrypt \
  CABANA_ACME_EMAIL=you@example.com bash
```

cabana installs cert-manager, requests a certificate per app and renews them.
Use `letsencrypt-staging` first when trying a new domain: Let's Encrypt allows
only five certificates per domain per week, and the staging issuer costs
nothing to get wrong.

Without a domain, apps get `sslip.io` hostnames that work immediately over plain
HTTP — certificates are not issued for sslip.io because its shared rate limit is
routinely exhausted. Re-running the installer is safe: it upgrades in place and
keeps your data and password.

## Architecture

One Go binary. The dashboard is compiled by Vite and embedded into it, so the
installer downloads a single file and there is nothing to run alongside it.

```
cabana (single binary)
├── /api/v1/*   REST API   ── the contract; the v0.5 CLI consumes this same API
├── /*          dashboard  ── Vite + React, embedded via go:embed
└── deploy.Deployer        ── K3s implementation, or a stub for cluster-less dev
```

Postgres holds all state. In production it runs inside the K3s cluster the
installer creates; in development it is a container from `docker-compose.dev.yml`.

### How a deploy works

Nothing is built on the host. cabana never clones a repository, never runs
`docker build`, and never handles a working copy — it only creates Kubernetes
objects and reads back their logs.

```
POST /apps/{id}/deploys
   │
   ├─▶ Namespace  cabana-app-<name>         one per app, so Destroy is one call
   ├─▶ Secret     app-env                   env vars, with PORT pinned to 8080
   │
   ├─▶ Job        build-<deploy-id>         rootless BuildKit
   │                 │  fetches the git context itself
   │                 │  builds the Dockerfile
   │                 ▼  pushes to the in-cluster registry
   │              cabana-registry.cabana-system.svc.cluster.local:5000
   │                 ▲
   ├─▶ Deployment │  pulls via containerd, through the registries.yaml mirror
   ├─▶ Service    │  ClusterIP :80 → :8080
   └─▶ Ingress    ─┘ Traefik, host = <app>.<domain> or the sslip.io fallback
                     plus a cert-manager Certificate when TLS is configured
```

The registry is reachable two ways on purpose. The BuildKit pod pushes over
cluster DNS; containerd runs on the host and cannot resolve cluster DNS, so
`deploy/cluster/registries.yaml` mirrors that same name to the NodePort where
the registry also listens. Both halves refer to one registry and one image tag.

Images are tagged with the deploy ID rather than a moving tag, which is what
makes the v0.3 rollback possible: every build stays addressable.

Apps are expected to listen on **port 8080**. cabana injects `$PORT=8080` and
overrides any user-supplied value, since the Service targets that port — an app
honouring a different one would build, run, and be unreachable.

## Development

Requires Go 1.25+, Node 22+, pnpm, Docker, k3d and kubectl.

```sh
make cluster-up # create the k3d cluster + in-cluster registry
make db-up      # start the development Postgres on :5433
make api        # run the API on :7373 (bootstrap password: cabana)
make dev        # in a second shell: Vite dev server on :5173
```

Deployed apps are reachable through the k3d load balancer on port 8000, e.g.
`http://myapp.127-0-0-1.sslip.io:8000`.

`make api-stub` runs against the stub deployer instead, when you want the
dashboard without a cluster.

The Vite dev server proxies `/api` to the API, so development is same-origin and
there is no CORS to configure.

To build the real artifact:

```sh
make build      # → bin/cabana, dashboard embedded, ~10MB, no runtime deps
make test       # Go test suite
make check      # vet + build + dashboard typecheck
```

## Configuration

Every knob is an environment variable, so the installer writes one
`EnvironmentFile` and never templates a config.

| Variable | Default | Meaning |
| --- | --- | --- |
| `CABANA_ADDR` | `:7373` | Listen address |
| `CABANA_DATABASE_URL` | *(required)* | Postgres connection string |
| `CABANA_BOOTSTRAP_PASSWORD` | *(generated)* | Admin password on first boot; printed if generated |
| `CABANA_BASE_DOMAIN` | *(empty)* | Wildcard domain for apps, e.g. `apps.example.com`. Falls back to sslip.io |
| `CABANA_DEV_CORS_ORIGIN` | *(empty)* | Allowed origin in development. Unset in production |
| `CABANA_DEPLOYER` | `k3s` | `k3s` or `stub`. The stub fakes builds so the dashboard runs without a cluster |
| `CABANA_KUBECONFIG` | *(empty)* | Only consulted outside the cluster. In production the ServiceAccount wins |
| `CABANA_REGISTRY` | `cabana-registry.cabana-system.svc.cluster.local:5000` | Must match the mirror in `registries.yaml` |
| `CABANA_BUILDKIT_IMAGE` | `moby/buildkit:v0.19.0-rootless` | Pinned, not floating on `:latest` |
| `CABANA_NODE_IP` | *(empty)* | Backs the sslip.io hostname fallback |
| `CABANA_STATE_DB_ADDR` | *(empty)* | In-cluster `host:port` of cabana's own DB; enables state backup |
| `CABANA_TLS_ISSUER` | *(empty)* | `letsencrypt`, `letsencrypt-staging` or `selfsigned`. Requires `CABANA_BASE_DOMAIN` |
| `CABANA_ACME_EMAIL` | *(empty)* | Required by Let's Encrypt for expiry notices |

On first boot cabana creates the `admin` user and prints the credentials once.

## API

Base path `/api/v1`. Auth is a bearer token from `POST /auth/login`. Errors are
uniform: `{"error": "code", "message": "human readable"}`.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness, unauthenticated |
| `POST` | `/auth/login` | Exchange credentials for a token (7-day TTL) |
| `POST` | `/auth/logout` | Revoke the current token |
| `GET` | `/auth/me` | Current user |
| `GET` | `/apps` | List apps |
| `POST` | `/apps` | Create an app, optionally fully configured (env, build args, services, scale) |
| `GET` | `/apps/{id}` | Fetch one app |
| `DELETE` | `/apps/{id}` | Destroy the workload and delete the app |
| `PUT` | `/apps/{id}/settings` | Replica count and health check |
| `GET` | `/apps/{id}/instances` | Live per-replica health |
| `GET` | `/apps/{id}/build-args` | Read build-time variables |
| `PUT` | `/apps/{id}/build-args` | Replace build-time variables |
| `GET` | `/apps/{id}/env` | Read environment variables |
| `PUT` | `/apps/{id}/env` | Replace the full environment set |
| `GET` | `/apps/{id}/deploys` | Deploy history, newest first |
| `POST` | `/apps/{id}/deploys` | Queue a deploy, or a rollback with `rollback_to`; returns `202` |
| `GET` | `/apps/{id}/deploys/{deployID}/log` | Stored build output for one deploy |
| `GET` | `/apps/{id}/logs` | Live logs as Server-Sent Events |
| `GET` | `/apps/{id}/links` | Services linked to an app |
| `POST` | `/apps/{id}/links` | Link a service; injects its connection string |
| `DELETE` | `/apps/{id}/links/{serviceID}` | Unlink a service |
| `GET` | `/catalog` | Installable service templates |
| `GET` | `/services` | Installed services |
| `POST` | `/services` | Install a service (optional `version` tag); returns `202` immediately |
| `GET` | `/services/{id}` | Fetch one service |
| `DELETE` | `/services/{id}` | Destroy a service and its data |
| `GET` | `/services/{id}/credentials` | Generated credentials and connection string |
| `GET` | `/services/{id}/logs` | Provisioning and container logs as Server-Sent Events |
| `GET` | `/settings/backup` | Backup destination (without the secret key) |
| `PUT` | `/settings/backup` | Set the S3-compatible backup destination |
| `GET` | `/services/{id}/backups` | List a service's backups |
| `POST` | `/services/{id}/backups` | Back a service up; returns `202` |
| `POST` | `/services/{id}/backups/{backupID}/restore` | Restore a backup (destructive) |
| `PUT` | `/services/{id}/backup-schedule` | Set daily/weekly automatic backups |
| `GET` | `/settings/state-backups` | List backups of cabana's own database |
| `POST` | `/settings/state-backups` | Back up cabana's own database |

The log stream also accepts `?token=` because `EventSource` cannot set headers.

```sh
TOKEN=$(curl -s -X POST localhost:7373/api/v1/auth/login \
  -d '{"username":"admin","password":"cabana"}' | jq -r .token)

curl -s localhost:7373/api/v1/apps -H "Authorization: Bearer $TOKEN"
```

## Design system

The dashboard has no UI library. Styling is CSS Modules over a two-layer token
file in `dashboard/src/styles/tokens.css`:

- **Primitives** — the raw ramps (`--cbn-ember-500`, `--cbn-sand-800`). Never
  referenced by a component.
- **Semantic tokens** — what components use (`--surface`, `--text-muted`,
  `--accent`). Re-theming means remapping this layer alone.

The palette is warm on purpose: a platform you install on your own machine is a
shelter, not an enterprise console. Light and dark are both first-class, and the
theme toggle overrides the OS preference in either direction.

## Disaster recovery

cabana backs up two things: each database service's data, and its own state —
the database of apps, services, links and credentials. Together they are enough
to rebuild a lost server.

Per-service backups and the cabana-state backup both go to the S3 destination
set in Settings. Services can be scheduled (daily or weekly); the state backup
is taken from the button in Settings or on a schedule you run yourself.

To recover onto a fresh machine:

```sh
# 1. Install cabana with the same domain and point it at the same bucket.
curl -sfL https://get.cabana.dev | sudo CABANA_BASE_DOMAIN=example.com bash

# 2. Download the latest cabana-state dump from your bucket, then restore it
#    into the new install's database and restart cabana.
aws s3 cp s3://your-bucket/cabana-state/<id>.dump ./state.dump
k3s kubectl -n cabana-system exec -i deploy/cabana-postgres --   pg_restore -U cabana -d cabana --clean --if-exists < state.dump
systemctl restart cabana

# 3. cabana now lists every service again. Re-provision each from the dashboard,
#    then restore it from its own latest backup.
```

Restoring cabana's state is deliberately manual — it overwrites the entire
record of the platform, so there is no one-click button for it.

## Roadmap position

v0.1 is done when a clean VPS can go from `curl | bash` to a running app without
touching a terminal again. What remains:

- [x] **K3s deployer** — BuildKit Job builds from the git context and pushes to
      the in-cluster registry; Deployment/Service/Ingress applied; build and app
      logs streamed live.
- [x] **sslip.io fallback** — apps get a working hostname with no domain configured.
- [x] **Installer** — [install.sh](install.sh) provisions K3s, the registry,
      Postgres and cabana, then verifies it can log in before printing the
      password. Idempotent: re-running upgrades in place.
- [x] **TLS** — cert-manager installed by the installer, a certificate per app,
      automatic renewal. Requires a real domain.
- [ ] **Tarball upload** — the fallback deploy path for repos that are not public.

### Installed layout

cabana runs as a systemd unit on the host rather than inside the cluster, so
v0.1 needs no published container image. Everything else lives in K3s.

| Path | Contents |
| --- | --- |
| `/usr/local/bin/cabana` | The binary |
| `/etc/cabana/cabana.env` | Configuration, mode 600 |
| `/etc/cabana/kubeconfig` | ServiceAccount credential scoped by `rbac.yaml`, **not** cluster-admin |
| `/etc/rancher/k3s/registries.yaml` | The containerd mirror, written before K3s starts |

See [ROADMAP.MD](ROADMAP.MD) for v0.2 onwards.
