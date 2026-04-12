.PHONY: build test lint fmt vet clean docker-build

GO := go
GOFLAGS := -v
LDFLAGS := -s -w

build: build-server build-worker

build-server:
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o bin/server ./cmd/server

build-worker:
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o bin/worker ./cmd/worker

test:
	$(GO) test -race -count=1 ./...

test-coverage:
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf bin/ coverage.out coverage.html

docker-build:
	docker build -f Dockerfile.server -t platform-orchestrator-server:latest .
	docker build -f Dockerfile.worker -t platform-orchestrator-worker:latest .

mod-tidy:
	$(GO) mod tidy

vuln-check:
	govulncheck ./...
