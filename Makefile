# Quire — development targets.
# Run `make help` for the annotated list.

GO              ?= go
BIN             := $(CURDIR)/bin
COMPOSE         ?= docker compose
COMPOSE_FILE    := deploy/docker/compose.yaml
DEV_CERTS       := $(CURDIR)/deploy/docker/certs
DEV_DOMAINS     := quire-a.example quire-b.example
KIND_CLUSTER    ?= quire

# The image the manifests refer to. The tag is the description of the working
# tree rather than `latest`, so that a pod can be traced back to a commit and so
# that two deployments of two builds are two tags — which `latest` makes
# impossible exactly when it matters.
IMAGE_REGISTRY  ?= ghcr.io/anthonyvsmuller
IMAGE_NAME      ?= quired
IMAGE_TAG       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE           := $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
DATABASE_URL    ?= postgres://quire:quire@localhost:5432/quire?sslmode=disable
MIGRATIONS      := $(CURDIR)/migrations

# Pinned tool versions. `make tools` installs them into ./bin.
GOLANGCI_LINT_VERSION ?= v2.13.1
SQLC_VERSION          ?= v1.31.1
BUF_VERSION           ?= v1.72.0
MIGRATE_VERSION       ?= v4.19.1
GHZ_VERSION           ?= v0.121.0
# buf drives these two; they are the plugins that turn the contract into Go.
PROTOC_GEN_GO_VERSION      ?= v1.36.12
PROTOC_GEN_GO_GRPC_VERSION ?= v1.6.0

GOLANGCI_LINT      := $(BIN)/golangci-lint
SQLC               := $(BIN)/sqlc
BUF                := $(BIN)/buf
MIGRATE            := $(BIN)/migrate
GHZ                := $(BIN)/ghz
PROTOC_GEN_GO      := $(BIN)/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(BIN)/protoc-gen-go-grpc

export PATH := $(BIN):$(PATH)

.DEFAULT_GOAL := help

## help: list the available targets
.PHONY: help
help:
	@grep -hE '^## ' $(MAKEFILE_LIST) | sed -E 's/^## ([a-z0-9-]+): /\1\t/' | awk -F '\t' '{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# --- build -------------------------------------------------------------------

## build: compile the node server and the reference client into ./bin
.PHONY: build
build:
	$(GO) build -trimpath -o $(BIN)/quired ./cmd/quired
	$(GO) build -trimpath -o $(BIN)/quirectl ./cmd/quirectl

## image: build the cluster image of the node, tagged as the manifests expect
#
# The cluster stage and not the compose one: no shell, no package manager, and
# nothing to bind a privileged port with, because in a cluster the address a
# peer resolves belongs to the gateway.
#
# --load because the default buildx driver keeps its result in the build cache,
# and an image `kind load` cannot find in the local daemon is an image the
# cluster never sees.
.PHONY: image
image:
	docker build --target cluster --load \
		--build-arg VERSION="$(IMAGE_TAG)" \
		--build-arg REVISION="$$(git rev-parse HEAD 2>/dev/null || echo unknown)" \
		-f deploy/docker/Dockerfile -t $(IMAGE) .

## image-name: print the tag the manifests refer to
.PHONY: image-name
image-name:
	@echo $(IMAGE)

## run: run the node server against the local environment
.PHONY: run
run:
	$(GO) run ./cmd/quired

## tidy: sync go.mod and go.sum
.PHONY: tidy
tidy:
	$(GO) mod tidy

# --- quality -----------------------------------------------------------------

## fmt: format the go sources
.PHONY: fmt
fmt:
	$(GO) fmt ./...

## lint: run golangci-lint
.PHONY: lint
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run ./...

## lint-fix: run golangci-lint applying the fixable findings
.PHONY: lint-fix
lint-fix: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run --fix ./...

## vet: run go vet
.PHONY: vet
vet:
	$(GO) vet ./...

# --- tests -------------------------------------------------------------------

## test: unit tests, with the race detector
.PHONY: test
test:
	$(GO) test -race -shuffle=on ./...

# The suite needs a PostgreSQL and an object store, and it owns both: it drops
# every schema the node declares before applying them, and it empties the
# bucket between tests. These variables therefore point at throwaway ones, never
# at DATABASE_URL or at a real bucket, and `test-up` brings both up on ports
# nothing else uses.
TEST_DATABASE_URL       ?= postgres://quire:quire@127.0.0.1:55433/quire?sslmode=disable
TEST_STORAGE_ENDPOINT   ?= 127.0.0.1:55900
TEST_STORAGE_ACCESS_KEY ?= quire
TEST_STORAGE_SECRET_KEY ?= quire-secret
TEST_STORAGE_BUCKET     ?= quire-test-contents
TEST_DB_CONTAINER       := quire-test-db
TEST_STORAGE_CONTAINER  := quire-test-storage

## test-integration: integration tests against the throwaway postgres and object store
.PHONY: test-integration
test-integration:
	QUIRE_TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
	QUIRE_TEST_STORAGE_ENDPOINT="$(TEST_STORAGE_ENDPOINT)" \
	QUIRE_TEST_STORAGE_ACCESS_KEY_ID="$(TEST_STORAGE_ACCESS_KEY)" \
	QUIRE_TEST_STORAGE_SECRET_ACCESS_KEY="$(TEST_STORAGE_SECRET_KEY)" \
	QUIRE_TEST_STORAGE_BUCKET="$(TEST_STORAGE_BUCKET)" \
		$(GO) test -race -tags=integration -timeout=15m ./test/integration/...

## test-up: start the throwaway postgres and object store the integration tests need
.PHONY: test-up
test-up: test-db-up test-storage-up

## test-down: stop them
.PHONY: test-down
test-down: test-db-down test-storage-down

## test-db-up: start the throwaway postgres the integration tests run against
.PHONY: test-db-up
test-db-up:
	docker run --rm -d --name $(TEST_DB_CONTAINER) -p 127.0.0.1:55433:5432 \
		-e POSTGRES_USER=quire -e POSTGRES_PASSWORD=quire -e POSTGRES_DB=quire postgres:17-alpine
	@until docker exec $(TEST_DB_CONTAINER) pg_isready -U quire >/dev/null 2>&1; do sleep 1; done
	@echo "$(TEST_DB_CONTAINER) is ready on 55433"

## test-db-down: stop it
.PHONY: test-db-down
test-db-down:
	docker rm -f $(TEST_DB_CONTAINER)

## test-storage-up: start the throwaway minio the integration tests run against
.PHONY: test-storage-up
test-storage-up:
	docker run --rm -d --name $(TEST_STORAGE_CONTAINER) -p 127.0.0.1:55900:9000 \
		-e MINIO_ROOT_USER=$(TEST_STORAGE_ACCESS_KEY) \
		-e MINIO_ROOT_PASSWORD=$(TEST_STORAGE_SECRET_KEY) \
		quay.io/minio/minio:latest server /data
	@until curl -sf http://$(TEST_STORAGE_ENDPOINT)/minio/health/live >/dev/null 2>&1; do sleep 1; done
	@echo "$(TEST_STORAGE_CONTAINER) is ready on 55900"

## test-storage-down: stop it
.PHONY: test-storage-down
test-storage-down:
	docker rm -f $(TEST_STORAGE_CONTAINER)

# The end-to-end suite drives the federation `make dev-up` starts: the nodes on
# the ports compose published, the certificates `make dev-certs` generated, and
# the databases behind them — which one test needs for the state the contract
# has no call to establish, per C22 in docs/tcc-corrections.md.
TEST_NODE_A              ?= 127.0.0.1:19090
TEST_NODE_B              ?= 127.0.0.1:29090
TEST_NODE_A_HTTP         ?= http://127.0.0.1:18080
TEST_NODE_B_HTTP         ?= http://127.0.0.1:28080
TEST_NODE_A_CA           ?= $(DEV_CERTS)/quire-a.example.crt.pem
TEST_NODE_B_CA           ?= $(DEV_CERTS)/quire-b.example.crt.pem
TEST_NODE_A_DATABASE_URL ?= postgres://quire:quire@127.0.0.1:15432/quire?sslmode=disable
TEST_NODE_B_DATABASE_URL ?= postgres://quire:quire@127.0.0.1:25432/quire?sslmode=disable

## test-e2e: end-to-end tests against the two federated nodes
.PHONY: test-e2e
test-e2e:
	QUIRE_TEST_NODE_A="$(TEST_NODE_A)" \
	QUIRE_TEST_NODE_B="$(TEST_NODE_B)" \
	QUIRE_TEST_NODE_A_HTTP="$(TEST_NODE_A_HTTP)" \
	QUIRE_TEST_NODE_B_HTTP="$(TEST_NODE_B_HTTP)" \
	QUIRE_TEST_NODE_A_CA="$(TEST_NODE_A_CA)" \
	QUIRE_TEST_NODE_B_CA="$(TEST_NODE_B_CA)" \
	QUIRE_TEST_NODE_A_DATABASE_URL="$(TEST_NODE_A_DATABASE_URL)" \
	QUIRE_TEST_NODE_B_DATABASE_URL="$(TEST_NODE_B_DATABASE_URL)" \
		$(GO) test -tags=e2e -timeout=20m -count=1 ./test/e2e/...

## test-kind: end-to-end tests inside the kind cluster
.PHONY: test-kind
test-kind:
	$(GO) test -tags=e2e,kind -timeout=30m ./test/e2e/...

## cover: unit tests with a coverage profile in ./bin/coverage.out
.PHONY: cover
cover:
	@mkdir -p $(BIN)
	$(GO) test -race -coverprofile=$(BIN)/coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=$(BIN)/coverage.out | tail -n 1

## bench: grpc latency benchmark for the sync service (RNF06)
.PHONY: bench
bench: $(GHZ)
	./scripts/bench.sh

# --- code generation ---------------------------------------------------------

## generate: regenerate everything derived from proto and sql
.PHONY: generate
generate: proto sqlc

## proto: generate the go stubs from proto/quire/v1
.PHONY: proto
proto: $(BUF) $(PROTOC_GEN_GO) $(PROTOC_GEN_GO_GRPC)
	$(BUF) generate

## proto-lint: lint the protobuf definitions and check their formatting
.PHONY: proto-lint
proto-lint: $(BUF)
	$(BUF) lint
	$(BUF) format --diff --exit-code

## proto-fmt: rewrite the protobuf definitions in buf's canonical format
.PHONY: proto-fmt
proto-fmt: $(BUF)
	$(BUF) format --write

## sqlc: generate the query code from the sql sources
.PHONY: sqlc
sqlc: $(SQLC)
	$(SQLC) generate

# --- database ----------------------------------------------------------------

## migrate-up: apply every pending migration
.PHONY: migrate-up
migrate-up: $(MIGRATE)
	$(MIGRATE) -path $(MIGRATIONS) -database "$(DATABASE_URL)" up

## migrate-down: roll back the last migration
.PHONY: migrate-down
migrate-down: $(MIGRATE)
	$(MIGRATE) -path $(MIGRATIONS) -database "$(DATABASE_URL)" down 1

## migrate-create: scaffold a migration pair, NAME=<name>
.PHONY: migrate-create
migrate-create: $(MIGRATE)
	@test -n "$(NAME)" || { echo "usage: make migrate-create NAME=<name>"; exit 1; }
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS) -seq -digits 6 $(NAME)

# --- local environment -------------------------------------------------------

## dev-certs: generate the development certificates and signing keys of the local federation
.PHONY: dev-certs
dev-certs:
	./scripts/dev-certs.sh $(DEV_CERTS) $(DEV_DOMAINS)

## dev-up: start the two federated nodes with docker compose
#
# The signing keys are handed to compose through the environment rather than
# written into the file, because a key in the file is a key in the repository.
# The certificates travel the other way, as a mounted directory, because the
# pin a peer checks is computed from the file the node presents.
.PHONY: dev-up
dev-up: dev-certs
	QUIRE_A_SIGNING_KEY="$$(cat $(DEV_CERTS)/quire-a.example.signing.pem)" \
	QUIRE_B_SIGNING_KEY="$$(cat $(DEV_CERTS)/quire-b.example.signing.pem)" \
	$(COMPOSE) -f $(COMPOSE_FILE) up -d --build

## dev-down: stop the local federation and drop its volumes
.PHONY: dev-down
dev-down:
	$(COMPOSE) -f $(COMPOSE_FILE) down -v --remove-orphans

## dev-logs: follow the logs of the local federation
.PHONY: dev-logs
dev-logs:
	$(COMPOSE) -f $(COMPOSE_FILE) logs -f

## kind-up: create the kind cluster with istio and cert-manager
.PHONY: kind-up
kind-up:
	./scripts/kind-up.sh $(KIND_CLUSTER)

## kind-down: delete the kind cluster
.PHONY: kind-down
kind-down:
	kind delete cluster --name $(KIND_CLUSTER)

# --- tooling -----------------------------------------------------------------

## tools: install the pinned development tools into ./bin
.PHONY: tools
tools: $(GOLANGCI_LINT) $(SQLC) $(BUF) $(MIGRATE) $(GHZ) $(PROTOC_GEN_GO) $(PROTOC_GEN_GO_GRPC)

$(GOLANGCI_LINT):
	GOBIN=$(BIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(SQLC):
	GOBIN=$(BIN) $(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

$(BUF):
	GOBIN=$(BIN) $(GO) install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)

$(MIGRATE):
	GOBIN=$(BIN) $(GO) install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

$(GHZ):
	GOBIN=$(BIN) $(GO) install github.com/bojand/ghz/cmd/ghz@$(GHZ_VERSION)

$(PROTOC_GEN_GO):
	GOBIN=$(BIN) $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)

$(PROTOC_GEN_GO_GRPC):
	GOBIN=$(BIN) $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

## clean: remove the build artifacts and the installed tools
.PHONY: clean
clean:
	rm -rf $(BIN)
