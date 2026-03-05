clank: go.* *.go */*.go
	go build -v

.PHONY: clean
clean:
	rm -rf dist

.PHONY: lint
lint: clank
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.0.2 run

.PHONY: lint-fix
lint-fix: clank
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.0.2 run --fix

.PHONY: pre-commit
pre-commit: clank
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.0.2 run --disable godox

.PHONY: release
release:
	goreleaser release --snapshot --clean

