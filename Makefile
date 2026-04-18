.PHONY: test lint

test: ## Run all tests with coverage
	go test -coverprofile=coverage/all.out ./pkg/...
	go tool cover -func=coverage/all.out | tail -1

lint: ## Run golangci-lint
	$$(go env GOPATH)/bin/golangci-lint run ./...
