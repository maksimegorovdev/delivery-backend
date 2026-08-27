LOCAL_BIN := $(CURDIR)/bin

GOLANGCI_LINT_VERSION := v2.13.1
GOLANGCI_LINT := $(LOCAL_BIN)/golangci-lint

MIGRATE_VERSION := v4.19.1
MIGRATE := $(LOCAL_BIN)/migrate
MIGRATIONS_DIR := migrations/pg

tools: export GOBIN := $(LOCAL_BIN)
tools:
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(LOCAL_BIN) $(GOLANGCI_LINT_VERSION)
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)
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
	$(MIGRATE) -database '$(PG_URL)' -path $(MIGRATIONS_DIR) up
.PHONY: migrate-up

migrate-down:
	$(MIGRATE) -database '$(PG_URL)' -path $(MIGRATIONS_DIR) down
.PHONY: migrate-up

run:
	go run cmd/main.go
.PHONY: run

tidy:
	go mod tidy
.PHONY: tidy