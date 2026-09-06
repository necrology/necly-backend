.PHONY: help build test run dev docker-up docker-down docker-logs fmt vet tidy migrate seed

# Default target
help:
	@echo "Necly Services - Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make build          Build the binary"
	@echo "  make test           Run all tests"
	@echo "  make run            Run the server (requires database)"
	@echo "  make dev            Run with hot reload (requires air)"
	@echo "  make fmt            Format code with gofmt"
	@echo "  make vet            Run go vet"
	@echo "  make tidy           Run go mod tidy"
	@echo "  make docker-up      Start docker compose services"
	@echo "  make docker-down    Stop docker compose services"
	@echo "  make docker-logs    View docker compose logs"
	@echo "  make migrate        Run database migrations"
	@echo "  make seed           Seed database with sample data"

# Build the binary
build:
	@mkdir -p bin
	go build -o bin/necly-backend ./cmd/server

# Run all tests
test:
	go test ./... -count=1 -v

# Run the server
run: build
	./bin/necly-backend

# Development with hot reload (requires: go install github.com/air-verse/air@latest)
dev:
	air -c .air.toml

# Format code
fmt:
	gofmt -w .

# Static analysis
vet:
	go vet ./...

# Tidy modules
tidy:
	go mod tidy

# Docker compose
docker-up:
	docker compose up -d

docker-down:
	docker compose down -v

docker-logs:
	docker compose logs -f

# Database migrations
migrate:
	go run ./cmd/migrate

# Seed database
seed:
	go run ./cmd/seed

# Clean build artifacts
clean:
	rm -rf bin/
	go clean -cache