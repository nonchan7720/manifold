.PHONY: test lint serve

test: ## Run all tests with coverage
	setup-envtest use 1.36.2 -p path > /dev/null
	go test -coverprofile=coverage.out ./pkg/...
	go tool cover -func=coverage.out | tail -1

lint: ## Run golangci-lint
	golangci-lint run ./...

ENVFILE = .env

ifneq ("$(wildcard $(ENVFILE))","")
	include $(ENVFILE)
	export
endif

serve:
	@go run main.go gateway -c config-test.yaml
