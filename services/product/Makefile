include .env
export

LOCAL_BIN := $(CURDIR)/bin

GOLANGCI_LINT_VERSION := v2.13.1
MIGRATE_VERSION := v4.19.1
GRPCUI_VERSION := v1.5.3

GOLANGCI_LINT := $(LOCAL_BIN)/golangci-lint
MIGRATE := $(LOCAL_BIN)/migrate
GRPCUI := $(LOCAL_BIN)/grpcui

MIGRATIONS_DIR := migrations/pg

tools: export GOBIN := $(LOCAL_BIN)
tools:
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(LOCAL_BIN) $(GOLANGCI_LINT_VERSION)
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)
	go install github.com/fullstorydev/grpcui/cmd/grpcui@$(GRPCUI_VERSION)
.PHONY: tools

lint:
	$(GOLANGCI_LINT) run
.PHONY: lint

lint-fix:
	$(GOLANGCI_LINT) run --fix
.PHONY: lint-fix

migrate-create:
	@printf "Enter migration name: "; read name; \
    [ -n "$$name" ] || { echo "migration name is required"; exit 1; }; \
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS_DIR) -seq $$name
.PHONY: migrate-create

migrate-up:
	$(MIGRATE) -database '$(PG_DSN)' -path $(MIGRATIONS_DIR) up
.PHONY: migrate-up

migrate-down:
	$(MIGRATE) -database '$(PG_DSN)' -path $(MIGRATIONS_DIR) down
.PHONY: migrate-up

grpcui:
	$(GRPCUI) -plaintext 'localhost:$(GRPC_SERVER_PORT)'
.PHONY: grpcui

run:
	go run cmd/main.go
.PHONY: run

tidy:
	go mod tidy
.PHONY: tidy