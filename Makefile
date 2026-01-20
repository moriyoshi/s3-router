.PHONY: help lint test build docker clean fmt run integration-test helm-integration-test

help:
	@echo "s3-router makefile targets"
	@echo "  make lint                  - Run linters"
	@echo "  make test                  - Run unit tests"
	@echo "  make integration-test      - Run moto integration tests"
	@echo "  make helm-integration-test - Run helm chart integration tests"
	@echo "  make build                 - Build binary"
	@echo "  make docker                - Build docker image"
	@echo "  make clean                 - Clean build artifacts"
	@echo "  make run                   - Run server"
	@echo "  make fmt                   - Format code"

lint:
	make -C tests/integration lint
	make -C tests/helm-integration lint
	golangci-lint run ./...
	gofmt -l -w .

test:
	go test -v -race -coverprofile=coverage.out ./...

build:
	go build -o bin/s3router ./cmd/s3router

run:
	go run ./cmd/s3router -config config.example.yaml -log-level debug

clean:
	rm -rf bin/ coverage.out

integration-test:
	$(MAKE) -C tests/integration test

helm-integration-test:
	$(MAKE) -C tests/helm-integration test

fmt:
	make -C tests/integration fmt
	make -C tests/helm-integration fmt
	go fmt ./...
	go mod tidy

docker:
	docker build -t s3router:latest -f Dockerfile .
