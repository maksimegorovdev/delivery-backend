LOCAL_BIN := $(CURDIR)/bin

BUF := $(LOCAL_BIN)/buf
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
PROTOVALIDATE_VERSION := v1.2.2

BUF_VERSION := v1.72.0

tools: export GOBIN := $(LOCAL_BIN)
tools:
	go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
.PHONY: tools

buf-export:
	$(BUF) export \
                  https://github.com/bufbuild/protovalidate.git#subdir=proto/protovalidate,tag=$(PROTOVALIDATE_VERSION) \
                  --path buf/validate \
                  -o third_party/protovalidate
.PHONY: buf-export

generate: export PATH := $(LOCAL_BIN):$(PATH)
generate:
	$(BUF) generate
.PHONY: generate

lint:
	$(BUF) lint
.PHONY: lint

format:
	$(BUF) format -w
.PHONY: format

breaking:
	$(BUF) breaking --against '.git#branch=main'
.PHONY: breaking

tidy:
	go mod tidy
.PHONY: tidy