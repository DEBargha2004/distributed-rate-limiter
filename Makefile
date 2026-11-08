.PHONY: all proto build test run cluster-up cluster-down

all: build

proto:
	@echo "Generating protobuf code..."
	protoc --go_out=. --go_opt=paths=source_relative \
	       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	       proto/ratelimiter/v1/ratelimiter.proto

build:
	@echo "Building Rate Limiter server..."
	go build -o bin/server cmd/server/main.go

test:
	@echo "Running tests & race detection..."
	go test -v -race ./...

bench:
	@echo "Running latency benchmarks..."
	go test -bench=. -benchmem ./test

cluster-up:
	@echo "Spinning up 3-node Redis Cluster..."
	docker compose -f deployments/docker-compose.yml up -d

cluster-down:
	@echo "Tearing down Redis Cluster..."
	docker compose -f deployments/docker-compose.yml down -v
