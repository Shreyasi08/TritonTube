GO ?= go
GOFMT ?= gofmt
PROTOC ?= protoc
BIN := bin
TOOLS := $(CURDIR)/.tools/bin

.PHONY: proto build fmt-check vet check test test-race test-e2e test-compose test-etcd etcd-up etcd-down up down demo clean
proto:
	GOBIN=$(TOOLS) $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	GOBIN=$(TOOLS) $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1
	PATH="$(TOOLS):$$PATH" $(PROTOC) --go_out=. --go_opt=module=github.com/Shreyasi08/TritonTube --go-grpc_out=. --go-grpc_opt=module=github.com/Shreyasi08/TritonTube proto/storage/v1/storage.proto
build:
	mkdir -p $(BIN)
	$(GO) build -trimpath -o $(BIN)/web ./cmd/web
	$(GO) build -trimpath -o $(BIN)/storage ./cmd/storage
	$(GO) build -trimpath -o $(BIN)/admin ./cmd/admin
fmt-check:
	@test -z "$$($(GOFMT) -l cmd internal proto tests web)" || { $(GOFMT) -l cmd internal proto tests web; exit 1; }
vet:
	$(GO) vet ./...
check: fmt-check vet test test-race build
test:
	$(GO) test -count=1 ./...
test-e2e:
	GO="$(GO)" ./scripts/test-e2e.sh
test-race:
	$(GO) test -race -count=1 ./...
test-compose:
	TRITONTUBE_COMPOSE_TEST=1 $(GO) test -count=1 -v ./tests -run '^TestDockerComposeLifecycle$$' -timeout 10m
test-etcd:
	TRITONTUBE_ETCD_TEST=1 $(GO) test -race -count=1 -v ./tests -run '^TestEtcdThreeMemberFailover$$' -timeout 12m
etcd-up:
	docker compose -p tritontube-etcd -f docker-compose.yml -f docker-compose.etcd.yml --profile etcd up --build -d --wait
etcd-down:
	docker compose -p tritontube-etcd -f docker-compose.yml -f docker-compose.etcd.yml --profile etcd --profile scale down
up:
	docker compose up --build -d --wait
down:
	docker compose --profile scale down
demo:
	./scripts/generate-sample.sh
	@echo 'Open http://127.0.0.1:8080 and upload data/sample.mp4 (start with make up or the local commands).'
clean:
	rm -rf $(BIN) .tools
