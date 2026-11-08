# Distributed Rate Limiter

A production-grade, distributed rate-limiting service built in **Go** and backed by **Redis** (atomic Lua scripting) with support for Redis Cluster, hot-reloading rules, in-process fallback policies, and a gRPC API.

---

## 🏛️ Architecture Overview

```
                      +---------------------------------------+
                      |       Client / Upstream Service       |
                      +---------------------------------------+
                                          |
                              (HTTP Request with Key/IP)
                                          v
                      +---------------------------------------+
                      |   HTTP Middleware (pkg/middleware)    |
                      +---------------------------------------+
                                          |
                               (gRPC AllowRequest)
                                          v
                      +---------------------------------------+
                      |    Rate Limiter Server (cmd/server)   |
                      |          - gRPC API Service           |
                      |          - Hot-Reload Engine          |
                      +---------------------------------------+
                                     /         \
                      (Healthy / Primary)   (Outage / Degraded)
                                   /             \
                                  v               v
                +--------------------+   +-----------------------+
                |   Redis Cluster    |   |  In-Memory Fallback   |
                |   - Atomic Lua     |   |  - Local Token Bucket |
                |   - Hash-Tag Slots |   |  - Circuit Breaker    |
                +--------------------+   +-----------------------+
```

---

## 🗺️ Repo Map: "Where to Do What"

| Path | Purpose | What to Implement Here |
|---|---|---|
| [`proto/ratelimiter/v1/ratelimiter.proto`](proto/ratelimiter/v1/ratelimiter.proto) | API Contract | Protobuf definition for `Allow(AllowRequest) -> AllowResponse` with keys, rule IDs, cost, deadlines. |
| [`internal/algorithm/`](internal/algorithm/) | Algorithm Interfaces | Unified `Limiter` interface (`Check(ctx, key, cost) -> Decision`), memory/mock implementations for unit tests. |
| [`internal/redis/lua/`](internal/redis/lua/) | Redis Atomic Scripts | Pure Lua scripts for Fixed Window, Sliding Window Counter, and Token Bucket. Uses Redis `TIME` and sets TTLs. |
| [`internal/redis/`](internal/redis/) | Redis Cluster Client | Connection pooling, script preloading (`SCRIPT LOAD` / `EVALSHA`), and hash-tag key formatting (`rl:{rule}:{user123}`). |
| [`internal/config/`](internal/config/) | Rule Definitions & Hot Reload | YAML rule schema and file watcher using `atomic.Pointer` for lock-free snapshot swapping. |
| [`internal/engine/`](internal/engine/) | Rule Matching Engine | Dimension extraction (user, IP, API key, route), rule precedence resolution, and multi-rule enforcement. |
| [`internal/fallback/`](internal/fallback/) | Fault Tolerance | Circuit breaker for Redis timeouts, fail-open vs fail-closed policy, and in-memory local fallback limiter. |
| [`internal/server/`](internal/server/) | Service Handlers | gRPC service handler implementing `Allow`, plus an HTTP server exposing `/metrics` and `/healthz`. |
| [`internal/metrics/`](internal/metrics/) | Observability | Prometheus counters (`allowed_total`, `denied_total`, `redis_errors_total`) and latency histogram. |
| [`pkg/middleware/`](pkg/middleware/) | Client Middleware | Go `http.Handler` wrapper setting standard headers (`X-RateLimit-*`, `Retry-After`) and returning HTTP 429. |
| [`configs/rules.yaml`](configs/rules.yaml) | Configuration | Production & dev rate-limiting rules. |
| [`deployments/`](deployments/) | Infrastructure | Docker Compose setup for multi-node Redis Cluster and Grafana dashboard JSON. |
| [`test/`](test/) | Proof & Verification | Concurrency tests (goroutine hammering), P50/P99 latency benchmarks, and Redis outage simulation. |

---

## 🚀 Step-by-Step Implementation Guide

### Phase 1: Define the Contract & Algorithms
1. **Protobuf (`proto/ratelimiter/v1/ratelimiter.proto`)**:
   - Define message `AllowRequest` (`key`, `rule_id`, `cost`).
   - Define message `AllowResponse` (`allowed`, `limit`, `remaining`, `retry_after_ms`, `reset_at_ms`).
   - Define service `RateLimiter`.
2. **Unified Interface (`internal/algorithm/limiter.go`)**:
   - Define `type Limiter interface { Check(ctx context.Context, key string, cost int64) (Decision, error) }`.
   - Implement `Decision` struct holding decision state.

### Phase 2: Atomic Redis Operations with Lua
1. **Lua Scripts (`internal/redis/lua/`)**:
   - `fixed_window.lua`: `INCRBY` counter per `key:windowStart`, `EXPIRE` window.
   - `sliding_window.lua`: Maintain current & previous window counts, weight previous count by remaining overlap percentage.
   - `token_bucket.lua`: Read `tokens` & `last_refill`. Refill `(now - last_refill) * rate`, subtract cost, save state + TTL.
   - *Key rule:* Always fetch timestamp via `redis.call('TIME')` inside Lua rather than passing client clocks.
2. **Redis Client (`internal/redis/client.go`)**:
   - Connect using Go's `github.com/redis/go-redis/v9` (supports standalone and Redis Cluster).
   - Use `SCRIPT LOAD` on startup and execute via `EVALSHA`.

### Phase 3: Config & Hot-Reload Engine
1. **Config Definition (`internal/config/rule.go`)**:
   - Represent rule matching: dimension (IP, User ID, API Key, Route), algorithm, limit, window, cost, and failure policy (`fail-open` or `fail-closed`).
2. **Atomic Swap (`internal/config/watcher.go`)**:
   - Store rule set in a `sync/atomic.Pointer[RuleSet]`.
   - On file change event (or signal): parse new config -> validate -> atomically swap pointer. Zero locks on read path.

### Phase 4: Resilience & Failure Handling
1. **Circuit Breaker (`internal/fallback/breaker.go`)**:
   - Track Redis failure rate. If consecutive timeouts exceed threshold, trip breaker to avoid caller timeout latency.
2. **In-Memory Fallback (`internal/fallback/local_limiter.go`)**:
   - If Redis is down, route traffic to local per-instance token bucket to provide graceful degradation rather than an all-or-nothing outage.

### Phase 5: gRPC Service & HTTP Middleware
1. **gRPC Server (`internal/server/grpc_service.go`)**:
   - Bind to protobuf interface.
   - Respect client deadlines (`ctx.Done()`).
2. **HTTP Middleware (`pkg/middleware/http.go`)**:
   - Pluggable key extractors (`extractor.go`).
   - Set standard headers:
     - `X-RateLimit-Limit`: Maximum requests permitted in window.
     - `X-RateLimit-Remaining`: Remaining request quota.
     - `X-RateLimit-Reset`: Unix epoch timestamp when quota resets.
     - `Retry-After`: Seconds until retry allowed (only on 429 Too Many Requests).

### Phase 6: Proof & Benchmarks
1. **Concurrency Hammer (`test/concurrency_test.go`)**:
   - Spawn $N$ goroutines (e.g. 100 concurrent workers) firing at the same key with limit $L$. Verify total admitted requests equal exactly $L$.
2. **Benchmarks (`test/benchmark_test.go`)**:
   - Benchmark `Check` latency: assert P50 < 1ms and P99 < 3ms with local Redis.
3. **Outage Simulation (`test/failure_test.go`)**:
   - Cut Redis connection mid-flight; verify circuit breaker trips and fallback policy triggers as configured.

---

## 🛠️ Environment Prerequisites

To run and build this project:
- **Go**: Version 1.22+ (`sudo apt install golang-go` or download from [go.dev/dl](https://go.dev/dl/))
- **Docker & Docker Compose**: For running multi-node Redis Cluster
- **Protoc & Go plugins**:
  ```bash
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
  ```
