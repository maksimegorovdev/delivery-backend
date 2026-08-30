include .env
export

LOCAL_BIN := $(CURDIR)/bin

GOLANGCI_LINT_VERSION := v2.13.1
GOLANGCI_LINT := $(LOCAL_BIN)/golangci-lint

tools:
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(LOCAL_BIN) $(GOLANGCI_LINT_VERSION)
.PHONY: tools

lint:
	$(GOLANGCI_LINT) run
.PHONY: lint

lint-fix:
	$(GOLANGCI_LINT) run --fix
.PHONY: lint-fix

run:
	go run cmd/main.go
.PHONY: run

tidy:
	go mod tidy
.PHONY: tidy