LOCALBIN := $(CURDIR)/bin

GOLANGCI_LINT_VERSION := v2.13.1

GOLANGCI_LINT := $(LOCALBIN)/golangci-lint

tools:
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(LOCALBIN) $(GOLANGCI_LINT_VERSION)
.PHONY: tools

lint:
	$(GOLANGCI_LINT) run
.PHONY: lint

lint-fix:
	$(GOLANGCI_LINT) run --fix
.PHONY: lint-fix

tidy:
	go mod tidy
.PHONY: tidy