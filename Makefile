# Quire — development targets.
# Run `make help` for the annotated list.

GO              ?= go
BIN             := $(CURDIR)/bin
COMPOSE         ?= docker compose
COMPOSE_FILE    := deploy/docker/compose.yaml
KIND_CLUSTER    ?= quire
DATABASE_URL    ?= postgres://quire:quire@localhost:5432/quire?sslmode=disable
MIGRATIONS      := $(CURDIR)/migrations

# Pinned tool versions. `make tools` installs them into ./bin.
GOLANGCI_LINT_VERSION ?= v2.13.1
SQLC_VERSION          ?= v1.31.1
BUF_VERSION           ?= v1.72.0
MIGRATE_VERSION       ?= v4.19.1
GHZ_VERSION           ?= v0.121.0
# buf drives these two; they are the plugins that turn the contract into Go.
PROTOC_GEN_GO_VERSION      ?= v1.36.10
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

## build: compile quired and quirectl into ./bin
.PHONY: build
build:
	$(GO) build -trimpath -o $(BIN)/quired ./cmd/quired
	$(GO) build -trimpath -o $(BIN)/quirectl ./cmd/quirectl

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

## test-integration: integration tests (testcontainers: postgres and minio)
.PHONY: test-integration
test-integration:
	$(GO) test -race -tags=integration -timeout=15m ./test/integration/...

## test-e2e: end-to-end tests against the two federated nodes
.PHONY: test-e2e
test-e2e:
	$(GO) test -tags=e2e -timeout=20m ./test/e2e/...

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

## dev-up: start the two federated nodes with docker compose
.PHONY: dev-up
dev-up:
	$(COMPOSE) -f $(COMPOSE_FILE) up -d --build

## dev-down: stop the local federation and drop its volumes
.PHONY: dev-down
dev-down:
	$(COMPOSE) -f $(COMPOSE_FILE) down -v

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
