.PHONY: test lint test-guardrails lint-guardrails serve runn

test: ## Run all tests with coverage
	setup-envtest use 1.36.2 -p path > /dev/null
	go test -coverprofile=coverage.out ./pkg/...
	go tool cover -func=coverage.out | tail -1

lint: ## Run golangci-lint
	golangci-lint run ./...

# guardrails は本体とは別の Go モジュール（guardrails/go.mod）。上の test / lint は入れ子の
# モジュールを見ないので、別のターゲットにしている。
test-guardrails: ## Run the tests of the guardrails module
	cd guardrails && go test ./...

lint-guardrails: ## Run golangci-lint on the guardrails module
	cd guardrails && golangci-lint run ./...

runn: ## Run the runn end-to-end suite (needs docker and runn)
	tests/runn/run.sh

ENVFILE = .env

ifneq ("$(wildcard $(ENVFILE))","")
	include $(ENVFILE)
	export
endif

serve:
	@go run main.go gateway -c config-test.yaml
