include .env
export

LOCAL_BIN := $(CURDIR)/bin

GOLANGCI_LINT_VERSION := v2.13.1
SWAG_VERSION := v1.16.6

GOLANGCI_LINT := $(LOCAL_BIN)/golangci-lint
SWAG := $(LOCAL_BIN)/swag

tools: export GOBIN := $(LOCAL_BIN)
tools:
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(LOCAL_BIN) $(GOLANGCI_LINT_VERSION)
	go install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)
.PHONY: tools

lint:
	$(GOLANGCI_LINT) run
.PHONY: lint

lint-fix:
	$(GOLANGCI_LINT) run --fix
.PHONY: lint-fix

swag:
	$(SWAG) init -g internal/transport/http/router.go --parseDependency --parseInternal
.PHONY: swag

swag-format:
	$(SWAG) fmt
.PHONY: swag-format

run:
	go run cmd/main.go
.PHONY: run

tidy:
	go mod tidy
.PHONY: tidy