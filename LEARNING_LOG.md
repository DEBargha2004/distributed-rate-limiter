# Learning Log: Distributed Rate Limiter

> Learner-owned log for predictions, explain-backs, overrides, and defense notes.

---

## Brick 2.4 [B] Real Redis Integration Test for Lua Scripts
- **Date**: 2026-11-09
- **Prediction**: Miniredis mocks actual Redis semantics and `redis.call('TIME')` returns the expected array, so no test failures expected.
- **Result**: All tests passed under real Redis:7 (`redis:7-alpine`) with `-race`. TTL was verified > 0 across all 3 algorithms.
- **Explain-back**: Testing against real Redis (Docker) validates aspects that miniredis cannot catch: protocol serialization (float truncation across the C-Lua boundary), real server clock resolution (`redis.call('TIME')`), and Redis Cluster slot distribution constraints (`CROSSSLOT` failures). Hash tags `{...}` force keys to share a hash slot so multi-key scripts run atomically on one node. Real Redis also verified that our Lua scripts set TTL > 0 on every state key to prevent memory leaks.

---

## Brick 3.1 [B] Redis Client Wrapper
- **Date**: 2026-11-09
- **Prediction**: `redis.NewUniversalClient` handles options mapping cleanly and abstracts standalone vs cluster seamlessly.
- **Result**: All tests passed under `-race`.
- **Explain-back**: `redis.UniversalClient` provides a unified client API regardless of whether one Redis address (standalone) or multiple (cluster) are provided. Forwarding the incoming context in `Ping(ctx)` ensures caller timeouts and cancellations immediately stop in-flight Redis commands, and aggressive timeouts (100ms) prevent a lagging Redis from stalling API gateway traffic.

---

## Brick 3.2 [A] Script Manager (`internal/redis/scripts.go`)
- **Date**: 2026-11-09
- **Prediction**: Scripts will preload into Redis at startup, returning SHA1 hashes. When Redis experiences a script cache flush (`SCRIPT FLUSH`), `EVALSHA` fails with `NOSCRIPT`, which the manager catches to reload and retry.
- **Result**: All tests passed under `go test -race ./...` (including mid-run cache eviction and recovery).
- **Explain-back**: `NOSCRIPT` occurs when the script SHA is missing from Redis's volatile in-memory script cache (e.g. after restart, replica failover, or `SCRIPT FLUSH`). Unlike network drops or throttling—which require exponential backoff—`NOSCRIPT` is a deterministic cache-miss. Performing a synchronous `SCRIPT LOAD` guarantees that bytecode is cached and ready, making a single retry sufficient. Keep mutex locks strictly around in-memory map accesses to prevent deadlocks and lock contention during network calls.

---

## Brick 3.3 [A] Limiter Implementation (`internal/redis/limiter.go`)
- **Date**: 2026-11-11
- **Prediction**: Go limiter will map configuration rules to Lua ARGV and parse raw multi-bulk returns into strongly typed `algorithm.Decision` structs.
- **Result**: All unit and real Redis integration tests passed under `-race` across Fixed Window, Sliding Window, and Token Bucket algorithms.
- **Defense Notes**:
  - *Why omit client-supplied `now`?* Time drifts across distributed systems (clock skew), causing stale or unsynchronized timestamps across different machines. Relying on Redis `TIME` inside the Lua script establishes a single authoritative clock.
  - *Why pass floats as formatted strings?* Passing raw floats can cause Redis protocol serialization and the Lua C API to unintentionally truncate values or lose precision. Formatting floats as decimal strings preserves full fidelity until Lua parses them with `tonumber()`.
- **Explain-back**: Omitting a client-supplied `now` prevents clock drift across distributed application servers from causing unfair quota resets or backward time jumps, making Redis `TIME` the single authoritative clock. Floating-point numbers (such as refill rates) must cross into Redis Lua as formatted decimal strings to avoid unintended integer truncation and precision loss across the Redis RESP / Lua C boundary. Redis multi-bulk arrays are decoded by go-redis into `[]any` containing `int64` elements, which require explicit type assertions before being cast into domain types like `time.Duration` and `time.Time`.

---

## Brick 3.4 [C] Stub Engine (`internal/engine/engine.go`)
- **Date**: 2026-11-11
- **Prediction**: Stub engine connects the atomic watcher snapshot with the Redis limiter to provide an Allow() execution path for the thin slice.
- **Result**: All unit and end-to-end reload tests passed under `-race`.
- **Defense Notes**:
  - *Why tag `{ruleID:subject}` instead of `{ruleID}`?* Tagging only `{ruleID}` would cause every subject hitting that rule to calculate the exact same CRC16 hash slot, concentrating 100% of that rule's traffic onto a single Redis node and burning it out (hot slot). Tagging `{ruleID:subject}` distributes different users evenly across all cluster shards while still guaranteeing that any multi-key operations for a single subject share a node.
- **Explain-back**: The engine acts as the orchestration layer between configuration snapshots and rate limit execution. It reads the active `RuleSet` lock-free using `atomic.Pointer`, formats deterministic hash-tagged keys compatible with Redis Cluster, and delegates atomic script execution to `redis.Limiter`. This establishes the vertical pipeline required before introducing gRPC.

---

## Brick 3.5 [B] Minimal gRPC Allow (`internal/server/grpc.go`)
- **Date**: 2026-11-11
- **Prediction**: Handler will validate requests, delegate to Engine.Allow, map errors to standard gRPC status codes, and serialize decisions into millisecond wire fields.
- **Result**: All unit tests in `internal/server` passed under `-race`.
- **Defense Notes**:
  - *Why return milliseconds (`retry_after_ms`, `reset_at_ms`)?* In high-throughput distributed systems handling thousands of requests per second, quota replenishment and window boundaries occur at sub-second scales. Forcing clients to wait whole seconds introduces artificial latency and tail queueing; millisecond precision allows callers to resume traffic as soon as capacity refills.
  - *Why map internal errors to `status.Error(codes.X)`?* Returning raw Go errors defaults to an opaque `codes.Unknown` across RPC boundaries. Mapping to standard status codes (`InvalidArgument`, `NotFound`, `Internal`) enables cross-language client SDKs (Go, TypeScript, Python) to handle errors programmatically (like HTTP 400, 404, 500) rather than parsing error message strings with fragile regexes.
- **Explain-back**: Implemented the `RateLimiterServer` gRPC handler implementing `Allow`. Used `errors.Is` to unwrap engine errors for clean mapping to gRPC status codes (`codes.NotFound` vs `codes.Internal`), and converted Go's domain `time.Duration` and `time.Time` into millisecond integers (`Milliseconds()` and `UnixMilli()`) for Protobuf transmission.





