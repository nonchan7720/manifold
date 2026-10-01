.PHONY: test lint serve proto proto-lint proto-breaking

test: ## Run all tests with coverage
	setup-envtest use 1.36.2 -p path > /dev/null
	go test -coverprofile=coverage.out ./pkg/...
	go tool cover -func=coverage.out | tail -1

lint: ## Run golangci-lint
	golangci-lint run ./...

proto: ## Generate Go code from proto/ (pkg/proto)
	buf generate

proto-lint: ## Lint proto/
	buf lint

PROTO_AGAINST ?= origin/main
proto-breaking: ## Check proto/ for breaking changes against $(PROTO_AGAINST)
	buf breaking --against '.git#ref=$(PROTO_AGAINST)'

ENVFILE = .env

ifneq ("$(wildcard $(ENVFILE))","")
	include $(ENVFILE)
	export
endif

serve:
	@go run main.go gateway -c config-test.yaml
