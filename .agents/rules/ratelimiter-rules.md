# Distributed rate limiter: domain invariants (always on)

Spec items that name a technology are requirements satisfied by a recorded decision (ADR in `docs/adr/`), not fixed choices, unless the learner says fixed.

## Correctness

1. All rate-limit decisions are atomic: the whole check-and-update runs in one Lua script via `EVALSHA`. Load scripts once; handle `NOSCRIPT` by reload and one retry.
2. Time comes from Redis `TIME` inside the script. No client timestamp reaches Redis-backed limiting logic. Only the Go local limiter takes an injectable `Clock`, for tests.
3. Every key written gets a TTL. Tests assert TTL > 0 after a call.
4. Everything is passed through `KEYS` and `ARGV`; `tonumber()` every input; return floats as strings (Lua to Redis truncates floats).
5. Keys are deterministic: `rl:{<ruleId>:<dimValue>}[:suffix]`. All keys one script touches share the same hash tag. Escape delimiters in dimension values.
6. Multi-rule evaluation: apply all matching rules, deny if any denies, return the most restrictive `retryAfter`. Partial consumption is a documented consequence.
7. Cost per request is supported end to end. Cost above capacity must deny, never loop.
8. Rule changes: validate first, swap an immutable snapshot atomically, keep the old set on failure, log the rule-set version used by each decision.

## Failure and latency

9. Every Redis call has a context deadline. Timeout or error triggers the rule's failure policy (`fail_open` or `fail_closed`).
10. A circuit breaker (closed, open, half-open with probing) prevents waiting on repeated timeouts.
11. A per-instance fallback token bucket gives approximate limiting during a Redis outage.
12. A slow limiter is worse than none: never block a request path beyond the configured deadline.

## API and edge

13. gRPC `Allow` fields are stable; never renumber or reuse protobuf fields.
14. HTTP: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`, `Retry-After` on 429. Header semantics documented in the README.
15. Trust `X-Forwarded-For` only from configured trusted proxies. Key extraction is pluggable.

## Cluster

16. Verify co-location with `CLUSTER KEYSLOT`. Scripts must be loaded on every master. Never use cross-slot multi-key operations.
17. State the hot-key limitation: one busy subject stays on one shard.

## Metrics

18. Metrics: allowed/denied by rule, decision-latency histogram, Redis errors, fallback activations. Labels are per rule, never per subject. Expose Prometheus `/metrics`.

## Testing and measurement

19. Default test command: `go test -race ./...`. Redis integration tests run against real Redis (Docker) behind a build tag.
20. Reference-first: Go reference models export golden vectors to `testdata/golden/`; Lua and Go local limiter must match them.
21. Concurrency proof: N goroutines on one key never exceed the limit. Race demo (naive read-modify-write over-admits) exists before Lua is trusted.
22. Fixed-window boundary flaw is demonstrated by a test (about 2x burst).
23. Benchmarks report P50 and P99 with hardware, OS, Go and Redis versions, run count, load method. Latency numbers of 1 ms / 3 ms are hypotheses, not requirements.
24. Local results are labelled local. Single-host cluster results are CPU-bound; do not extrapolate.
25. Pin and record versions (Go, Redis image, go-redis, grpc, protobuf, prometheus client). Verify them; do not recall them.
26. Seeded, deterministic tests; no sleeps where an injectable clock or `TIME`-based wait suffices.

## Decisions

27. Record decisions in `TASKS.md` decision log and `docs/adr/NNN-title.md`: algorithm selection, key format, multi-rule semantics, failure policy, rule watching, middleware framework.
28. Out of scope: AI Gateway and URL Shortener integration, config-service rule source.
