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


