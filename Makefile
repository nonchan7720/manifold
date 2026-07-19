.PHONY: test lint

test: ## Run all tests with coverage
	go test -coverprofile=coverage/all.out ./pkg/...
	go tool cover -func=coverage/all.out | tail -1

lint: ## Run golangci-lint
	$$(go env GOPATH)/bin/golangci-lint run ./...

ENVFILE = .env

ifneq ("$(wildcard $(ENVFILE))","")
	include $(ENVFILE)
	export
endif

serve:
	@go run main.go gateway -c config-test.yaml
