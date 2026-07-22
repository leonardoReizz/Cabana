#!/usr/bin/env bash
#
# cabana installer.
#
#   curl -sfL https://get.cabana.dev | bash
#
# Provisions K3s, an in-cluster image registry, Postgres and cabana itself,
# then prints the dashboard URL and the initial password.
#
# Environment:
#   CABANA_BASE_DOMAIN    wildcard domain for apps, e.g. apps.example.com.
#                         Without it, apps get sslip.io hostnames.
#   CABANA_TLS_ISSUER     letsencrypt | letsencrypt-staging | selfsigned.
#                         Needs CABANA_BASE_DOMAIN. Off by default.
#   CABANA_ACME_EMAIL     required by Let's Encrypt for expiry notices.
#   CABANA_VERSION        release to install (default: latest)
#   CABANA_BINARY_URL     override the download entirely (for testing)
#   CABANA_MANIFEST_BASE  where to fetch cluster manifests from. May be a local
#                         directory, which is how this script is tested.
#   CABANA_SKIP_K3S       set to 1 when K3s is already installed and configured

set -euo pipefail

REPO="leonardoreis/cabana"
VERSION="${CABANA_VERSION:-latest}"
MANIFEST_BASE="${CABANA_MANIFEST_BASE:-https://raw.githubusercontent.com/${REPO}/main/deploy/cluster}"

CONFIG_DIR=/etc/cabana
DATA_DIR=/var/lib/cabana
BIN_PATH=/usr/local/bin/cabana
K3S_CONFIG_DIR=/etc/rancher/k3s
KUBECTL=(k3s kubectl)

REGISTRY_HOST="cabana-registry.cabana-system.svc.cluster.local:5000"
PG_NODEPORT=30432
CERT_MANAGER_VERSION="v1.16.2"

# ---------------------------------------------------------------- output ----

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
info() { printf '  \033[2m›\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*" >&2; }
die() {
	printf '\n  \033[31m✗\033[0m %s\n\n' "$*" >&2
	exit 1
}

# ------------------------------------------------------------- preflight ----

preflight() {
	[ "$(id -u)" -eq 0 ] || die "cabana must be installed as root. Try: curl -sfL … | sudo bash"
	[ "$(uname -s)" = "Linux" ] || die "cabana installs on Linux. On macOS or Windows, run it in a Linux VM."

	case "$(uname -m)" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) die "unsupported architecture: $(uname -m). cabana ships amd64 and arm64 builds." ;;
	esac

	for cmd in curl systemctl openssl; do
		command -v "$cmd" >/dev/null 2>&1 || die "required command not found: $cmd"
	done

	# A port conflict fails much more clearly here than halfway through the
	# install. Each check is skipped when the component that owns that port is
	# already installed, because re-running the installer — to upgrade, or to
	# set CABANA_BASE_DOMAIN — is expected and must not trip over itself.
	port_in_use() {
		ss -lnt 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${1}\$"
	}

	if ! command -v k3s >/dev/null 2>&1 && port_in_use 6443; then
		die "port 6443 is already in use — K3s needs it"
	fi
	if [ ! -f /etc/systemd/system/cabana.service ] && port_in_use 7373; then
		die "port 7373 is already in use — cabana needs it"
	fi
}

# ------------------------------------------------------------------ k3s ----

configure_registry_mirror() {
	# This must be written BEFORE K3s starts. containerd reads registries.yaml
	# at boot only, so writing it afterwards would silently do nothing until a
	# restart — and every image pull in between would fail.
	mkdir -p "$K3S_CONFIG_DIR"
	cat >"$K3S_CONFIG_DIR/registries.yaml" <<EOF
mirrors:
  "${REGISTRY_HOST}":
    endpoint:
      - "http://127.0.0.1:30500"
EOF
	info "containerd will mirror ${REGISTRY_HOST} to the in-cluster registry"
}

install_k3s() {
	if [ "${CABANA_SKIP_K3S:-0}" = "1" ]; then
		info "skipping K3s install (CABANA_SKIP_K3S=1)"
		return
	fi
	if command -v k3s >/dev/null 2>&1; then
		info "K3s is already installed — leaving it alone"
		return
	fi

	info "installing K3s"
	# K3s' defaults are exactly what cabana wants: Traefik serves the Ingress
	# objects the deployer creates, and servicelb binds it to host ports 80/443.
	# Disabling either leaves apps running but unreachable.
	curl -sfL https://get.k3s.io | sh - >/dev/null 2>&1 ||
		die "K3s install failed. Re-run with: curl -sfL https://get.k3s.io | sh -"
}

wait_for_node() {
	info "waiting for the cluster to come up"
	local deadline=$((SECONDS + 180))
	until "${KUBECTL[@]}" get nodes 2>/dev/null | grep -q ' Ready '; do
		[ "$SECONDS" -lt "$deadline" ] || die "the cluster did not become ready within 3 minutes"
		sleep 3
	done
}

# ------------------------------------------------------------ manifests ----

apply_manifest() {
	local name="$1"
	if [ -d "$MANIFEST_BASE" ]; then
		"${KUBECTL[@]}" apply -f "${MANIFEST_BASE}/${name}" >/dev/null
	else
		curl -sfL "${MANIFEST_BASE}/${name}" | "${KUBECTL[@]}" apply -f - >/dev/null
	fi
}

# fetch_manifest returns a manifest's contents rather than applying it, for the
# ones that need substitution before they are valid.
fetch_manifest() {
	local name="$1"
	if [ -d "$MANIFEST_BASE" ]; then
		cat "${MANIFEST_BASE}/${name}"
	else
		curl -sfL "${MANIFEST_BASE}/${name}"
	fi
}

install_cluster_components() {
	info "installing the in-cluster registry"
	apply_manifest registry.yaml

	info "installing RBAC for cabana"
	apply_manifest rbac.yaml

	# The password is generated here rather than baked into the manifest, so no
	# two installs share a database credential.
	if ! "${KUBECTL[@]}" -n cabana-system get secret cabana-postgres >/dev/null 2>&1; then
		PG_PASSWORD="$(openssl rand -hex 24)"
		"${KUBECTL[@]}" -n cabana-system create secret generic cabana-postgres \
			--from-literal=password="$PG_PASSWORD" >/dev/null
	else
		info "reusing the existing Postgres credentials"
		PG_PASSWORD="$("${KUBECTL[@]}" -n cabana-system get secret cabana-postgres \
			-o jsonpath='{.data.password}' | base64 -d)"
	fi

	info "installing Postgres"
	apply_manifest postgres.yaml

	# Traefik is what turns a created Ingress into a reachable app, so an install
	# that finishes without it looks healthy and serves nothing.
	#
	# K3s installs it through a HelmChart the helm-controller reconciles after
	# the API server is up, so the Deployment does not exist yet. `rollout
	# status` fails immediately on a missing object rather than waiting for it,
	# hence waiting for it to appear first.
	info "waiting for the ingress controller"
	local deadline=$((SECONDS + 300))
	until "${KUBECTL[@]}" -n kube-system get deploy traefik >/dev/null 2>&1; do
		[ "$SECONDS" -lt "$deadline" ] ||
			die "Traefik never appeared — apps would be unreachable. Check: k3s kubectl -n kube-system get pods"
		sleep 5
	done
	"${KUBECTL[@]}" -n kube-system rollout status deploy/traefik --timeout=180s >/dev/null ||
		die "Traefik did not start — apps would be unreachable. Check: k3s kubectl -n kube-system get pods"

	install_cert_manager

	info "waiting for the registry and database"
	"${KUBECTL[@]}" -n cabana-system rollout status deploy/cabana-registry --timeout=180s >/dev/null ||
		die "the registry did not start — check: k3s kubectl -n cabana-system describe deploy/cabana-registry"
	"${KUBECTL[@]}" -n cabana-system rollout status deploy/cabana-postgres --timeout=180s >/dev/null ||
		die "Postgres did not start — check: k3s kubectl -n cabana-system describe deploy/cabana-postgres"
}

# Certificates are optional: an install with no domain runs on plain HTTP and
# skips all of this rather than failing.
install_cert_manager() {
	if [ -z "${CABANA_TLS_ISSUER:-}" ]; then
		return
	fi
	if [ -z "${CABANA_BASE_DOMAIN:-}" ]; then
		die "CABANA_TLS_ISSUER needs CABANA_BASE_DOMAIN — certificates cannot be issued for sslip.io"
	fi
	case "$CABANA_TLS_ISSUER" in
	letsencrypt | letsencrypt-staging)
		[ -n "${CABANA_ACME_EMAIL:-}" ] ||
			die "CABANA_ACME_EMAIL is required for $CABANA_TLS_ISSUER"
		;;
	selfsigned) ;;
	*) die "CABANA_TLS_ISSUER must be letsencrypt, letsencrypt-staging or selfsigned" ;;
	esac

	info "installing cert-manager ${CERT_MANAGER_VERSION}"
	curl -sfL "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml" |
		"${KUBECTL[@]}" apply -f - >/dev/null ||
		die "could not install cert-manager"

	# All three must be up before a ClusterIssuer is accepted: the webhook
	# validates it, and applying one too early fails with a connection error
	# that reads like a network problem.
	for deploy in cert-manager cert-manager-webhook cert-manager-cainjector; do
		"${KUBECTL[@]}" -n cert-manager rollout status "deploy/$deploy" --timeout=180s >/dev/null ||
			die "cert-manager component $deploy did not start"
	done

	info "creating certificate issuers"
	local issuers
	issuers="$(fetch_manifest issuers.yaml)"
	# The email is the only substitution; everything else is static.
	printf '%s' "${issuers//__ACME_EMAIL__/${CABANA_ACME_EMAIL:-noreply@example.com}}" |
		"${KUBECTL[@]}" apply -f - >/dev/null || die "could not create issuers"

	# The webhook can accept the object before the issuer is actually ready.
	# Waiting here means a failed ACME account registration — a typo in the
	# email, no outbound network — is reported now rather than as certificates
	# that silently never appear.
	if [ "$CABANA_TLS_ISSUER" != "selfsigned" ]; then
		"${KUBECTL[@]}" wait --for=condition=Ready \
			"clusterissuer/cabana-${CABANA_TLS_ISSUER}" --timeout=120s >/dev/null 2>&1 ||
			warn "the ${CABANA_TLS_ISSUER} issuer is not ready yet — check: k3s kubectl describe clusterissuer cabana-${CABANA_TLS_ISSUER}"
	fi
}

# ------------------------------------------------------------ kubeconfig ----

write_kubeconfig() {
	# cabana runs on the host but must not hold the cluster-admin credential.
	# A long-lived ServiceAccount token scoped by the ClusterRole in rbac.yaml
	# is what it gets instead.
	"${KUBECTL[@]}" apply -f - >/dev/null <<'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: cabana-token
  namespace: cabana-system
  annotations:
    kubernetes.io/service-account.name: cabana
type: kubernetes.io/service-account-token
EOF

	local token ca deadline=$((SECONDS + 60))
	# The token controller populates the Secret asynchronously.
	until token="$("${KUBECTL[@]}" -n cabana-system get secret cabana-token \
		-o jsonpath='{.data.token}' 2>/dev/null)" && [ -n "$token" ]; do
		[ "$SECONDS" -lt "$deadline" ] || die "the cabana ServiceAccount token was never issued"
		sleep 2
	done

	token="$(printf '%s' "$token" | base64 -d)"
	ca="$("${KUBECTL[@]}" -n cabana-system get secret cabana-token -o jsonpath='{.data.ca\.crt}')"

	mkdir -p "$CONFIG_DIR"
	cat >"$CONFIG_DIR/kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: cabana
    cluster:
      server: https://127.0.0.1:6443
      certificate-authority-data: ${ca}
contexts:
  - name: cabana
    context:
      cluster: cabana
      user: cabana
      namespace: cabana-system
current-context: cabana
users:
  - name: cabana
    user:
      token: ${token}
EOF
	chmod 600 "$CONFIG_DIR/kubeconfig"
	info "cabana will talk to the cluster as a scoped ServiceAccount, not admin"
}

# --------------------------------------------------------------- binary ----

install_binary() {
	local url="${CABANA_BINARY_URL:-}"
	if [ -z "$url" ]; then
		if [ "$VERSION" = "latest" ]; then
			url="https://github.com/${REPO}/releases/latest/download/cabana-linux-${ARCH}"
		else
			url="https://github.com/${REPO}/releases/download/${VERSION}/cabana-linux-${ARCH}"
		fi
	fi

	info "downloading cabana (${ARCH})"
	# Download to a temp path and move into place, so a failed download never
	# leaves a truncated binary at $BIN_PATH.
	local tmp
	tmp="$(mktemp)"
	if [ -f "$url" ]; then
		cp "$url" "$tmp" # local file, used by the test harness
	else
		curl -sfL "$url" -o "$tmp" || die "could not download cabana from ${url}"
	fi
	chmod +x "$tmp"
	mv "$tmp" "$BIN_PATH"
}

# --------------------------------------------------------------- service ----

write_service() {
	mkdir -p "$CONFIG_DIR" "$DATA_DIR"

	# Carried over from a previous install when present, so re-running does not
	# invalidate the password the user already wrote down. verify_login decides
	# whether whatever we end up with is actually the live one.
	if [ -f "$CONFIG_DIR/cabana.env" ] && grep -q BOOTSTRAP_PASSWORD "$CONFIG_DIR/cabana.env"; then
		BOOTSTRAP_PASSWORD="$(grep BOOTSTRAP_PASSWORD "$CONFIG_DIR/cabana.env" | cut -d= -f2-)"
	else
		BOOTSTRAP_PASSWORD="$(openssl rand -base64 18 | tr -d '/+=' | cut -c1-20)"
	fi

	NODE_IP="$(ip route get 1.1.1.1 2>/dev/null | awk '{print $7; exit}')"
	[ -n "$NODE_IP" ] || NODE_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"

	cat >"$CONFIG_DIR/cabana.env" <<EOF
CABANA_ADDR=:7373
CABANA_DATABASE_URL=postgres://cabana:${PG_PASSWORD}@127.0.0.1:${PG_NODEPORT}/cabana?sslmode=disable
CABANA_BOOTSTRAP_PASSWORD=${BOOTSTRAP_PASSWORD}
CABANA_KUBECONFIG=${CONFIG_DIR}/kubeconfig
CABANA_REGISTRY=${REGISTRY_HOST}
CABANA_BASE_DOMAIN=${CABANA_BASE_DOMAIN:-}
CABANA_NODE_IP=${NODE_IP}
CABANA_TLS_ISSUER=${CABANA_TLS_ISSUER:-}
CABANA_ACME_EMAIL=${CABANA_ACME_EMAIL:-}
CABANA_STATE_DB_ADDR=cabana-postgres.cabana-system.svc.cluster.local:5432
EOF
	chmod 600 "$CONFIG_DIR/cabana.env"

	cat >/etc/systemd/system/cabana.service <<EOF
[Unit]
Description=cabana
Documentation=https://github.com/${REPO}
After=network-online.target k3s.service
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=${CONFIG_DIR}/cabana.env
ExecStart=${BIN_PATH}
Restart=always
RestartSec=5
StateDirectory=cabana

# cabana needs no write access to the filesystem beyond its state directory.
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
NoNewPrivileges=true
ReadWritePaths=${DATA_DIR}

[Install]
WantedBy=multi-user.target
EOF

	systemctl daemon-reload
	systemctl enable cabana >/dev/null 2>&1
	systemctl restart cabana
}

wait_for_cabana() {
	info "starting cabana"
	local deadline=$((SECONDS + 90))
	until curl -sf http://127.0.0.1:7373/healthz >/dev/null 2>&1; do
		if ! systemctl is-active --quiet cabana; then
			journalctl -u cabana -n 20 --no-pager >&2 || true
			die "cabana failed to start — logs above"
		fi
		[ "$SECONDS" -lt "$deadline" ] || die "cabana did not become healthy within 90 seconds"
		sleep 2
	done
}

# verify_login doubles as a smoke test of the whole auth path and as the only
# reliable way to know whether the password we are about to print actually
# works. cabana only applies CABANA_BOOTSTRAP_PASSWORD when the database has no
# users, so on a reinstall against an existing database the generated password
# is silently ignored — printing it anyway would send the user in circles.
verify_login() {
	local body status
	body=$(printf '{"username":"admin","password":"%s"}' "$BOOTSTRAP_PASSWORD")
	status=$(curl -s -o /dev/null -w '%{http_code}' \
		-X POST http://127.0.0.1:7373/api/v1/auth/login -d "$body" || echo 000)

	case "$status" in
	200) PASSWORD_WORKS=1 ;;
	401) PASSWORD_WORKS=0 ;;
	*) die "cabana is running but its API returned ${status} on login" ;;
	esac
}

# ------------------------------------------------------------------ main ----

main() {
	bold ""
	bold "  cabana installer"
	bold ""

	preflight
	configure_registry_mirror
	install_k3s
	wait_for_node
	install_cluster_components
	write_kubeconfig
	install_binary
	write_service
	wait_for_cabana
	verify_login

	printf '\n'
	bold "  ┌─ cabana is ready ──────────────────────────────"
	printf '  \033[1m│\033[0m  url       http://%s:7373\n' "$NODE_IP"
	printf '  \033[1m│\033[0m  username  admin\n'
	if [ "$PASSWORD_WORKS" = "1" ]; then
		printf '  \033[1m│\033[0m  password  %s\n' "$BOOTSTRAP_PASSWORD"
	else
		printf '  \033[1m│\033[0m  password  (unchanged — this database already had an admin)\n'
	fi
	bold "  └────────────────────────────────────────────────"
	printf '\n'

	if [ "$PASSWORD_WORKS" != "1" ]; then
		warn "cabana kept the password from the original install. If it is lost,"
		warn "reset it by deleting the admin user and restarting cabana:"
		warn "  k3s kubectl -n cabana-system exec deploy/cabana-postgres -- \\"
		warn "    psql -U cabana -c 'DELETE FROM users'  &&  systemctl restart cabana"
		printf '\n'
	fi

	if [ -z "${CABANA_BASE_DOMAIN:-}" ]; then
		warn "no CABANA_BASE_DOMAIN set — apps will be served on sslip.io hostnames."
		warn "Point a wildcard *.yourdomain.com at ${NODE_IP} and re-run with"
		warn "CABANA_BASE_DOMAIN=yourdomain.com to use your own."
		printf '\n'
	elif [ -z "${CABANA_TLS_ISSUER:-}" ]; then
		warn "no CABANA_TLS_ISSUER set — apps are served over plain HTTP."
		warn "Re-run with CABANA_TLS_ISSUER=letsencrypt and CABANA_ACME_EMAIL=you@example.com"
		warn "to get certificates automatically."
		printf '\n'
	fi
}

main "$@"
