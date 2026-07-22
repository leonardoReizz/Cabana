.DEFAULT_GOAL := help

DATABASE_URL    ?= postgres://cabana:cabana@localhost:5433/cabana?sslmode=disable
BIN             := bin/cabana
CLUSTER         ?= cabana-dev
KUBECONFIG_PATH ?= $(HOME)/.kube/config

export CABANA_DATABASE_URL   := $(DATABASE_URL)
export CABANA_DEV_CORS_ORIGIN := http://localhost:5173

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[1m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: db-up
db-up: ## Start the development Postgres
	docker compose -f docker-compose.dev.yml up -d --wait

.PHONY: db-down
db-down: ## Stop the development Postgres (keeps data)
	docker compose -f docker-compose.dev.yml down

.PHONY: db-reset
db-reset: ## Destroy and recreate the development Postgres
	docker compose -f docker-compose.dev.yml down -v
	$(MAKE) db-up

.PHONY: cluster-up
cluster-up: ## Create the k3d dev cluster and install the in-cluster registry
	k3d cluster create $(CLUSTER) \
		--registry-config deploy/cluster/registries.yaml \
		--port "8000:80@loadbalancer" \
		--wait
	kubectl --context k3d-$(CLUSTER) apply -f deploy/cluster/registry.yaml
	kubectl --context k3d-$(CLUSTER) -n cabana-system \
		rollout status deploy/cabana-registry --timeout=120s
	@echo "cluster ready — apps will be reachable through http://localhost:8000"

.PHONY: cluster-down
cluster-down: ## Delete the k3d dev cluster
	k3d cluster delete $(CLUSTER)

.PHONY: api
api: ## Run the API against the development Postgres and the k3d cluster
	CABANA_BOOTSTRAP_PASSWORD=cabana \
	CABANA_KUBECONFIG=$(KUBECONFIG_PATH) \
	go run ./cmd/cabana

.PHONY: api-stub
api-stub: ## Run the API with the stub deployer (no cluster needed)
	CABANA_BOOTSTRAP_PASSWORD=cabana CABANA_DEPLOYER=stub go run ./cmd/cabana

.PHONY: dashboard
dashboard: ## Build the dashboard into the Go embed path
	cd dashboard && pnpm install --frozen-lockfile && pnpm build

.PHONY: dev
dev: ## Run the Vite dev server (expects `make api` in another shell)
	cd dashboard && pnpm install && pnpm dev

.PHONY: build
build: dashboard ## Build the single self-contained cabana binary
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/cabana
	@echo "built $(BIN)"

.PHONY: test
test: ## Run the Go test suite
	go test ./...

.PHONY: check
check: ## Vet and build everything without producing artifacts
	go vet ./...
	go build ./...
	cd dashboard && pnpm typecheck

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin
	git clean -fdX internal/web/dist
