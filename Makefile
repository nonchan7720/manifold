.PHONY: test lint serve postman

test: ## Run all tests with coverage
	setup-envtest use 1.36.2 -p path > /dev/null
	go test -coverprofile=coverage.out ./pkg/...
	go tool cover -func=coverage.out | tail -1

lint: ## Run golangci-lint
	golangci-lint run ./...

postman: ## Run the Postman CLI end-to-end suite (needs docker and the Postman CLI)
	tests/postman/run.sh

ENVFILE = .env

ifneq ("$(wildcard $(ENVFILE))","")
	include $(ENVFILE)
	export
endif

serve:
	@go run main.go gateway -c config-test.yaml
