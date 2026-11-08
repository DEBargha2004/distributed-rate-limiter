# Distributed Rate Limiter: Master Task Board & Progress Tracker

> **Session State Tracking**: This file maintains the single source of truth across all sessions. Update this file whenever a task status changes or when new engineering notes and learnings are established.

---

## 📌 Quick Status Dashboard

| Phase | Module / Focus | Status | Completed / Total |
|---|---|---|---|
| **Phase 1** | Configuration & Hot-Reload Engine (`internal/config`) | `[x] Completed` | 4 / 4 |
| **Phase 2** | Core Rate Limiting Algorithms in Lua (`internal/redis/lua`) | `[x] Completed` | 3 / 3 |
| **Phase 3** | Redis Client & Script Management (`internal/redis`) | `[ ] Pending` | 0 / 3 |
| **Phase 4** | Dimension Matching Engine (`internal/engine`) | `[ ] Pending` | 0 / 2 |
| **Phase 5** | Resilience, Circuit Breaker & Fallback (`internal/fallback`) | `[ ] Pending` | 0 / 3 |
| **Phase 6** | gRPC Service & HTTP Middleware (`internal/server`, `pkg/middleware`) | `[ ] Pending` | 0 / 3 |
| **Phase 7** | Concurrency Testing & Latency Benchmarks (`test`) | `[ ] Pending` | 0 / 3 |

**Active Task Target**: **Phase 3, Task 3.1 — Redis Client Wrapper & Connection Pool (`internal/redis/client.go`)**

---

## 🗺️ Detailed Phase & Task Roadmap

### Phase 1: Configuration & Lock-Free Hot-Reload Engine
*Location:* [`internal/config/`](internal/config/)
*Core Concepts:* Lock-free concurrency with `sync/atomic.Pointer`, YAML unmarshaling, input validation, defensive copying, table-driven unit testing.

- [x] **Task 1.1: Rule Validation Logic (`internal/config/rule.go`)**
  - **Goal**: Implement validation rules for `Rule` and `RuleSet` (e.g. valid algorithms, positive limits, non-empty IDs, valid window durations).
  - **Learner Responsibility**: Write the `Validate() error` methods for individual `Rule` and the aggregated `RuleSet`.
  - **Status**: `[x] Completed`

- [x] **Task 1.2: Atomic Pointer Storage & Read Path (`internal/config/watcher.go`)**
  - **Goal**: Implement `NewWatcher` and `Get()` using `atomic.Pointer[RuleSet]` for zero-lock reads on high-throughput request paths.
  - **Learner Responsibility**: Initialize the atomic pointer and return the current snapshot without data races.
  - **Status**: `[x] Completed`

- [x] **Task 1.3: Safe Hot-Reloading (`internal/config/watcher.go`)**
  - **Goal**: Implement `Reload() error` to parse file from disk, validate new configuration, and atomically swap the active pointer. Ensure failed reloads do not corrupt active state.
  - **Learner Responsibility**: Implement the reload flow and error handling.
  - **Status**: `[x] Completed`

- [x] **Task 1.4: Unit Tests with Dynamic Updates (`internal/config/watcher_test.go`)**
  - **Goal**: Write comprehensive unit tests verifying initial load, invalid YAML rejection, and concurrent atomic swapping under load.
  - **Learner Responsibility**: Implement test scenarios with `t.Run`.
  - **Status**: `[x] Completed`

---

### Phase 2: Atomic Rate Limiting Algorithms in Redis & Lua
*Location:* [`internal/redis/lua/`](internal/redis/lua/)
*Core Concepts:* Atomic execution in single-threaded Redis, clock synchronization using `redis.call('TIME')`, hash slots for Redis Cluster, TTL management, floating-point math in Lua.

- [x] **Task 2.1: Fixed Window Counter (`internal/redis/lua/fixed_window.lua`)**
  - **Goal**: Window-based increment with automatic key expiration.
  - **Learner Responsibility**: Script logic checking quota, calculating remaining count, and setting key TTL.
  - **Status**: `[x] Completed`

- [x] **Task 2.2: Sliding Window Counter (`internal/redis/lua/sliding_window.lua`)**
  - **Goal**: Estimate request rate across window boundaries by weighting previous window count.
  - **Learner Responsibility**: Microsecond window calculation, overlap weighting formula, and atomic state updates.
  - **Status**: `[x] Completed`

- [x] **Task 2.3: Token Bucket Algorithm (`internal/redis/lua/token_bucket.lua`)**
  - **Goal**: Burst-tolerant rate limiter with continuous refill and microsecond precision.
  - **Learner Responsibility**: Hash read/write (`HMGET`/`HSET`), token replenishment formula, retry-after computation, and idle TTL cleanup.
  - **Status**: `[x] Completed`

---

### Phase 3: Redis Cluster Client & Script Preloading
*Location:* [`internal/redis/`](internal/redis/)
*Core Concepts:* Go-Redis connection pool, Redis Cluster vs Standalone client abstraction, script preloading via `SCRIPT LOAD` and execution via `EVALSHA`, fallback handling for `NOSCRIPT`.

- [ ] **Task 3.1: Redis Client Wrapper & Connection Pool (`internal/redis/client.go`)**
  - **Goal**: Initialize connection with health check, timeouts, and cluster topology discovery.
  - **Learner Responsibility**: Connection lifecycle and configuration.
  - **Status**: `[ ] Pending`

- [ ] **Task 3.2: Lua Script Manager (`internal/redis/scripts.go`)**
  - **Goal**: Preload all Lua scripts at startup, retain SHA1 digests, execute with `EVALSHA`, and gracefully handle cache misses (`NOSCRIPT`).
  - **Learner Responsibility**: Preload logic and execution wrapper.
  - **Status**: `[ ] Pending`

- [ ] **Task 3.3: Limiter Interface Implementation (`internal/redis/limiter.go`)**
  - **Goal**: Implement `algorithm.Limiter` interface bridging Go requests to Redis Lua scripts.
  - **Learner Responsibility**: Type conversions between Go `time.Duration` / `int64` and Redis string arguments, mapping script return arrays to `algorithm.Decision`.
  - **Status**: `[ ] Pending`

---

### Phase 4: Dimension Matching Engine
*Location:* [`internal/engine/`](internal/engine/)
*Core Concepts:* Redis Cluster hash-tags `{...}`, dimension extraction, rule precedence and fallback hierarchy.

- [ ] **Task 4.1: Cluster Hash-Tag Key Formatting (`internal/engine/engine.go`)**
  - **Goal**: Generate deterministic keys like `rl:{rule_id:dimension_val}` to ensure all keys for a subject map to the same Redis Cluster slot.
  - **Learner Responsibility**: Formatting function and test validation.
  - **Status**: `[ ] Pending`

- [ ] **Task 4.2: Rule Lookup & Precedence Matching (`internal/engine/engine.go`)**
  - **Goal**: Find applicable rules by rule ID or dimension (e.g. API Key > User ID > IP).
  - **Learner Responsibility**: Lookup matching logic and precedence resolver.
  - **Status**: `[ ] Pending`

---

### Phase 5: Resilience, Circuit Breaker & In-Memory Fallbacks
*Location:* [`internal/fallback/`](internal/fallback/)
*Core Concepts:* State machine (Closed, Open, Half-Open), concurrency safety with `sync.Mutex`, fail-open vs fail-closed degraded policies, in-process token bucket.

- [ ] **Task 5.1: Circuit Breaker Implementation (`internal/fallback/breaker.go`)**
  - **Goal**: Protect system latency when Redis degrades; trip on threshold timeouts and probe after cooldown.
  - **Learner Responsibility**: State transitions, failure counter, cooldown probing logic.
  - **Status**: `[ ] Pending`

- [ ] **Task 5.2: In-Memory Local Limiter (`internal/fallback/local_limiter.go`)**
  - **Goal**: Process-local token bucket to provide graceful degraded limiting during Redis downtime.
  - **Learner Responsibility**: Token refill math, thread-safe counter decrements.
  - **Status**: `[ ] Pending`

- [ ] **Task 5.3: Fallback Policy Handler (`internal/fallback/policy.go`)**
  - **Goal**: Route failed Redis calls according to rule's `FailurePolicy` (`fail_open` vs `fail_closed`).
  - **Learner Responsibility**: Decision logic for failed states.
  - **Status**: `[ ] Pending`

---

### Phase 6: gRPC Service & HTTP Middleware
*Location:* [`internal/server/`](pkg/middleware/)
*Core Concepts:* gRPC server lifecycle, protobuf serialization, client context cancellation / deadlines, standard HTTP rate-limiting headers.

- [ ] **Task 6.1: Protobuf Code Generation & Service Handler (`internal/server/grpc_service.go`)**
  - **Goal**: Implement `Allow` RPC endpoint with deadline propagation (`ctx.Done()`).
  - **Learner Responsibility**: RPC handler logic, request translation, protobuf response mapping.
  - **Status**: `[ ] Pending`

- [ ] **Task 6.2: Key Extractors (`pkg/middleware/extractor.go`)**
  - **Goal**: Extract IP (supporting `X-Forwarded-For`), Bearer token, or API Key from HTTP requests.
  - **Learner Responsibility**: Header parsing, CIDR/proxy trust handling.
  - **Status**: `[ ] Pending`

- [ ] **Task 6.3: HTTP Middleware (`pkg/middleware/http.go`)**
  - **Goal**: Intercept HTTP traffic, call rate limiter, populate headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`, `Retry-After`), and return HTTP 429 when limited.
  - **Learner Responsibility**: `http.Handler` wrapper and header logic.
  - **Status**: `[ ] Pending`

---

### Phase 7: Verification, Concurrency & Latency Benchmarks
*Location:* [`test/`](test/)
*Core Concepts:* High-concurrency goroutine hammering (`sync.WaitGroup`, atomic counters), race condition detection (`-race`), benchmark profiling (`b.ResetTimer`, `b.ReportAllocs`).

- [ ] **Task 7.1: Concurrency Hammer Test (`test/concurrency_test.go`)**
  - **Goal**: Hammer single key with 100+ concurrent workers and assert exact admitted request count.
  - **Learner Responsibility**: Goroutine orchestration and assertions.
  - **Status**: `[ ] Pending`

- [ ] **Task 7.2: Latency Benchmarks (`test/benchmark_test.go`)**
  - **Goal**: Verify P50 < 1ms and P99 < 3ms for rate limiting operations.
  - **Learner Responsibility**: Benchmark loop and profiling.
  - **Status**: `[ ] Pending`

- [ ] **Task 7.3: Outage Simulation Test (`test/failure_test.go`)**
  - **Goal**: Simulate Redis connection disruption mid-flight; assert circuit breaker trips and fallback policy executes without panics.
  - **Learner Responsibility**: Fault injection test case.
  - **Status**: `[ ] Pending`

---

## 📓 Session Changelog & Learning Notes

### Session 1
- **Completed**: Phase 1 Configuration & Hot-Reload Engine (`internal/config`). Lock-free `atomic.Pointer` verified with `-race`.

### Session 2 (Current Session)
- **Completed**: Phase 2, Task 2.1 Fixed Window Counter (`internal/redis/lua/fixed_window.lua`).
- **Completed**: Phase 2, Task 2.2 Sliding Window Counter (`internal/redis/lua/sliding_window.lua`).
- **Completed**: Phase 2, Task 2.3 Token Bucket Algorithm (`internal/redis/lua/token_bucket.lua`). Implemented microsecond lazy continuous replenishment, hash storage (`HMGET`/`HSET`), burst allowance, dynamic retry-after computation, and idle TTL cleanup. All Lua algorithms verified.
- **Active Task**: Phase 3, Task 3.1 Redis Client Wrapper & Connection Pool (`internal/redis/client.go`).
