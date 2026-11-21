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

---

## Brick 3.6 [B] Thin Slice End-to-End Smoke Test (`test/smoke_test.go`)
- **Date**: 2026-11-11
- **Prediction**: Calls 1–3 allowed (`Remaining`: 2 -> 1 -> 0, `RetryAfterMs`: 0); Call 4 denied (`Allowed`: false, `Remaining`: 0, `RetryAfterMs`: ~2000ms at 0.5 tokens/sec).
- **Result**: `TestSmoke_ThinSlice_EndToEnd` passed under `-race` with real Redis (`REQUIRE_REDIS=1`), and the full test suite plus `go vet` passed cleanly.
- **Defense Notes**:
  - *Why test gRPC over `bufconn` instead of a custom TCP port?* Custom TCP sockets across test cases can collide with other tests and touch the OS network stack. `bufconn` writes directly into an in-memory shared buffer, bypassing the kernel TCP stack while still exercising the real gRPC stack (Protobuf serialization, HTTP/2 framing, status codes, and deadlines).
- **Explain-back**: Wired and ran the full Phase 3 vertical slice end-to-end (`bufconn` gRPC client -> `GRPCServer` -> `Engine` -> `ScriptManager` -> real Redis Lua). Verified that sequential requests consume tokens deterministically (`2 -> 1 -> 0`) before rejecting the 4th request with `Allowed: false`, `Remaining: 0`, and `RetryAfterMs` matching the configured refill rate.

---

## Brick 4.1 [A] Key Formatting and Hash Tags (`internal/engine/engine.go`)
- **Date**: 2026-11-19
- **Prediction**: Without delimiter escaping, `:` creates collisions between ambiguous `(ruleID, subject)` pairs, and `{`/`}` corrupts Redis Cluster hash tag extraction. Suffixes must live outside `{...}` to share the slot.
- **Result**: Property-style collision tests, delimiter escaping tests, and cluster hash tag integrity tests passed under `-race` and `go vet`.
- **Defense Notes**:
  - *Why wrap `{ruleId:dimValue}` instead of `{ruleId}` or `{dimValue}`?* Tagging only `{ruleId}` forces all users for that rule onto a single Redis shard, creating a severe hotspot. Tagging `{ruleId:dimValue}` distributes traffic evenly across all 16,384 slots while guaranteeing any auxiliary keys for that specific (rule, subject) pair share the same slot.
  - *What does a rule spanning two slots cost you in atomicity?* Redis executes Lua scripts on a single node; it does not support cross-node distributed transactions. If a script touches keys on different slots, Redis immediately rejects it with a `CROSSSLOT` error. Even if cross-node calls were attempted outside Lua, coordinating them would break single-threaded atomicity and introduce network latency spikes.
- **Explain-back**: Implemented deterministic and collision-safe key formatting (`rl:{<escapedRuleID>:<escapedSubject>}[:<suffix>]`). Delimiter characters (`%`, `:`, `{`, `}`) are percent-encoded via `strings.NewReplacer` (with `%` escaped first) to make key generation strictly injective and prevent Redis Cluster hash tag corruption. Optional suffixes are placed outside `{...}` so auxiliary keys co-locate on the same slot without triggering `CROSSSLOT` errors. Documented decisions in `docs/adr/002-key-format-hash-tags.md`.

---

## Brick 4.2 [A] Multi-Dimensional Rule Matching (`internal/engine/engine.go`)
- **Date**: 2026-11-19
- **Prediction**: A request with multiple dimensions matches every rule whose dimension is present and non-empty. An empty string value (`""` or `"   "`) does not match.
- **Result**: All tests in `TestEngine_MatchRules` passed with `-race` across multi-dimension binding, whitespace trimming, explicit RuleID filter, and deterministic ordering.
- **Defense Notes**:
  - *Why evaluate all matching rules rather than stopping at the first rule (e.g. API Key > User > IP hierarchy)?* A single request can carry multiple identities representing different threat vectors. For example, multiple devices or compromised accounts might share a single IP behind a NAT/router; checking only API keys would leave infrastructure vulnerable to IP-level DDoS. Evaluating all matching rules ensures defense-in-depth across both network and identity layers.
- **Explain-back**: Implemented `MatchRules(req Request)` on `Engine`. It extracts the current configuration snapshot lock-free, matches rules whose configured `Dimension` exists and has a non-empty string in `req.Dimensions`, and pairs each rule with its trimmed subject value. When an explicit `RuleID` is supplied, only that rule is evaluated. Sorted results using `slices.SortFunc` by `Rule.ID` to guarantee deterministic evaluation order and reproducible testing.

---

## Brick 4.3 [A] Combine Decisions (`internal/engine/engine.go`)
- **Date**: 2026-11-20
- **Prediction**: Initially predicted early return on deny and refunding on failure, but realized early return causes client retry ping-pong and refunding across Redis shards requires complex distributed transactions while breaking natural DDoS backpressure.
- **Result**: Unit tests in `TestCombine` and end-to-end multi-rule evaluations in `TestEngine_AllowRequest` passed under `-race`.
- **Defense Notes**:
  - *Why can't multi-rule evaluation be executed in a single atomic Lua script?* Different rules for the same request produce different hash tags `rl:{<ruleId>:<dimValue>}`, causing them to hash to different Redis Cluster slots. Redis forbids cross-slot Lua scripts (`CROSSSLOT` error).
  - *Why accept partial consumption instead of rollback?* Refunding tokens across multiple Redis nodes is complex, introduces race conditions and network failure states, and requires distributed 2PC. Furthermore, denied requests still consumed gateway and Redis resources; deducting quota provides appropriate backpressure against abuse.
- **Explain-back**: Implemented multi-rule decision combination per Spec R13. If any matching rule denies, the aggregate verdict is `Allowed = false`, and `RetryAfter` is set to the maximum `RetryAfter` across all denying rules (the most restrictive wait). `Remaining` reflects the tightest bottleneck across evaluated rules (`min(Remaining)`), and unmetered requests return `math.MaxInt64`. Documented the partial consumption architecture and trade-offs in `docs/adr/003-multi-rule-semantics.md`.

---

## Brick 4.4 [B] Cost Per Request End to End (`internal/engine/engine.go`)
- **Date**: 2026-11-21
- **Prediction**: Cost 0 falls back to rule `DefaultCost` (2) and consumes 2; Cost 5 consumes 5; Cost 15 exceeds capacity 10 and cannot succeed regardless of elapsed wait time.
- **Result**: Unit tests in `TestResolveCost`, engine integration tests in `TestEngine_CostHandling`, and end-to-end gRPC tests in `TestSmoke_VariableCost_EndToEnd` passed under `-race`.
- **Defense Notes**:
  - *Why reject cost > capacity immediately without mutating tokens?* If a request requires more tokens than the bucket's maximum capacity, it can never be satisfied even when the bucket is full. Mutating or holding quota would unfairly starve concurrent legitimate requests that fit within capacity.
  - *What risk does returning a non-zero RetryAfter introduce to automated clients?* Returning a positive `RetryAfter` falsely signals that the request will succeed if the client waits and retries. Automated clients (e.g. gRPC retry interceptors) will loop repeatedly, wasting network bandwidth, CPU cycles, and flooding the rate limiting service.
- **Explain-back**: Implemented `ResolveCost` to enforce the quota cost resolution hierarchy: explicit positive request cost (`req.Cost > 0`), fallback to configured rule default (`rule.DefaultCost > 0`), and fallback to 1. Non-positive values are sanitized to prevent clients from gaining quota or bypassing limits. In Redis Lua, requests with cost > available tokens immediately deny without mutating stored bucket state. End-to-end tests confirmed cost 5 consumes 5, fallbacks work, and oversized requests fail fast.





