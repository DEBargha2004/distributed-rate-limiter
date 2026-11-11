# Distributed Rate Limiter: Master Task Board

> Single source of truth across sessions. Tier tags: **A** learner writes, **B** paired, **C** agent writes + learner reviews, **W** worked example. See `AGENT_GUIDE.md`.
> Brick IDs of completed work are unchanged. Bricks marked **(retro)** were added by the spec audit: the spec requires them and they were not covered earlier.

---

## Status

| Phase | Focus                                                                            | Status                             | Done / Total |
| ----- | -------------------------------------------------------------------------------- | ---------------------------------- | ------------ |
| 1     | Config and hot-reload (`internal/config`)                                        | Core complete, 2 retro bricks open | 4 / 6        |
| 2     | Atomic algorithms in Lua (`internal/redis/lua`)                                  | Core complete, 3 retro bricks open | 4 / 7        |
| 3     | Redis client, script manager, **thin end-to-end slice**                          | In progress                        | 4 / 6        |
| 4     | Engine: keys, rule matching, cost, multi-rule combine                            | Pending                            | 0 / 5        |
| 5     | Resilience: breaker, local limiter, failure policy, deadlines                    | Pending                            | 0 / 4        |
| 6     | Edge: gRPC hardening, key extractors, HTTP middleware, headers                   | Pending                            | 0 / 4        |
| 7     | Observability: metrics and `/metrics` (swappable with 6)                         | Pending                            | 0 / 4        |
| 8     | Redis Cluster: slots, per-node scripts, scaling, hot key (swappable with 5 to 7) | Pending                            | 0 / 4        |
| 9     | Proof runs: concurrency, benchmark, outage                                       | Pending                            | 0 / 3        |
| 10    | Ship: README, ADRs, traceability, final cold rebuild                             | Pending                            | 0 / 3        |

**Active brick**: **3.5 Minimal gRPC `Allow` (`proto/`, `internal/server/`)**.
**Suggested order for open retro bricks**: 2.4 now; 2.5, 2.6, 2.7, 1.5, 1.6 can be done at any phase boundary but must be closed before the Phase 9 gate.
**Feedback loop**: `go test -race ./...` for unit tests; Redis integration tests (Docker, build tag) for Lua and client; `go test -bench` for latency; `CLUSTER KEYSLOT` and cluster runs for sharding.

---

## Phase 1: Config and hot-reload (`internal/config`)

**Goal**: validated rules, lock-free reads, safe reload, watched file.
**Learn**: `atomic.Pointer`, validation, defensive copying, table-driven tests.

- [x] 1.1 [A] Rule and RuleSet validation (`rule.go`)
- [x] 1.2 [A] Atomic pointer storage and read path (`watcher.go`)
- [x] 1.3 [A] Safe `Reload()` (parse, validate, swap, keep old on failure)
- [x] 1.4 [A] Tests with dynamic updates under load (`watcher_test.go`)
- [ ] 1.5 (retro) [B] Rule-set version stamp: each `RuleSet` carries a version (counter or content hash) readable from `Get()`, so decisions can log which version they used. Confirm `Rule` already has fields: id, match dimension(s), algorithm, limit, window, failure policy; add any missing.
- [ ] 1.6 (retro) [B] File watching: reload automatically on file change (fsnotify or poll; record choice in an ADR). Rapid successive writes must not produce a torn or half-applied state.

**Done when**: 1.5: `Get()` exposes the version and a test shows the version changes only on successful reload. 1.6: editing the rules file changes active rules within a bounded time; an invalid edit leaves the old set and logs the error.
**Defend**: Why an immutable snapshot behind an atomic pointer instead of an RWMutex? What does a reader holding an old snapshot see?

---

## Phase 2: Atomic algorithms in Lua (`internal/redis/lua`)

**Goal**: three algorithms, atomic, server-clocked, TTL-safe, and proven against reference vectors.
**Learn**: single-threaded script atomicity, `TIME`, `KEYS`/`ARGV`, `tonumber`, float-vs-integer returns, TTL.

- [x] 2.1 [A] Fixed window (`fixed_window.lua`)
- [x] 2.2 [A] Sliding window counter (`sliding_window.lua`)
- [x] 2.3 [A] Token bucket (`token_bucket.lua`)
- [x] 2.4 (retro) [B] **Confirm-or-do**: scripts exercised against a real Redis (Docker) by integration tests covering allow, deny, remaining, retry-after, and TTL set on every key (`TTL` > 0 after a call). Verified in `internal/redis/lua_integration_test.go`.
- [ ] 2.5 (retro) [A] Demonstrate the race first: a naive Go read-compute-write limiter (GET, compute, SET) hit by N goroutines admits more than the limit; the same load against the Lua version never does. Keep as `sandbox/` or `test/` demo.
- [ ] 2.6 (retro) [A] Demonstrate the fixed-window flaw: test showing about 2x the limit admitted across a window boundary; show the sliding window and token bucket do not.
- [ ] 2.7 (retro) [A] Go reference models of the three algorithms (pure functions, injectable clock) that export **golden vectors** to `testdata/golden/*.json`; the Lua scripts are run against the same inputs and must match. Then write `docs/adr/001-algorithm-selection.md`: when you would pick each algorithm.

**Done when**: 2.4 integration tests green with Redis tag; 2.5 shows naive admitted > limit and Lua admitted <= limit; 2.6 shows boundary burst vs no burst; 2.7 Lua matches golden vectors for all three algorithms (including idle-expiry and fractional refill cases), ADR written.
**Defend**: Why does Lua make the decision atomic when Redis commands alone do not? Why `TIME` inside the script? What goes wrong with floats returned from Lua (integer truncation)?

---

## Phase 3: Redis client, script manager, thin end-to-end slice

**Goal**: one real `Allow` call travelling gRPC -> engine stub -> Redis Lua -> response, before anything is deepened.
**Learn**: go-redis pool and timeouts, `SCRIPT LOAD`/`EVALSHA`/`NOSCRIPT`, argument and return conversion, protobuf basics.

- [x] 3.1 [B] Redis client wrapper (`internal/redis/client.go`): config struct, timeouts, health check, standalone and cluster abstraction behind one small interface. Agent writes options and tests; learner writes connect, ping, close, timeouts.
- [x] 3.2 [A] Script manager (`internal/redis/scripts.go`): preload at startup, keep SHA1s, run with `EVALSHA`, recover from `NOSCRIPT` by reloading and retrying once. (W first: a sandbox `EVALSHA` demo.)
- [x] 3.3 [A] Limiter implementation (`internal/redis/limiter.go`): `Check(ctx, key, cost) -> Decision` per section 9 of the guide (no client `now`); convert Go types to `ARGV`, map script arrays (strings for floats) to `Decision`.
- [x] 3.4 [C] Stub engine: one hard-coded rule from the Phase 1 watcher; maps `rule_id` + subject to a key; no multi-rule logic yet.
- [ ] 3.5 [B] Minimal gRPC `Allow` (proto + handler; codegen is C). Fields: `allowed, limit, remaining, retryAfterMs, resetAtMs`.
- [ ] 3.6 [B] Smoke test: start Redis, start server, call `Allow` N times, observe allow then deny.

**Done when**: 3.2 a test flushes scripts (`SCRIPT FLUSH`) mid-run and the next call still succeeds; 3.3 table-driven tests pass for all three algorithms through the Go limiter against real Redis; 3.6 an end-to-end call shows allowed true then false with correct `remaining` and `retryAfterMs`.
**Defend**: What happens on `NOSCRIPT` and why is a single retry enough? Why must float values cross the Lua boundary as strings?

---

## Phase 4: Engine, keys, rules, cost, combine

**Goal**: deterministic keys, multi-rule evaluation, cost support, rule version in every decision log.
**Learn**: hash tags, rule matching, combining decisions, structured logging.

- [ ] 4.1 [A] Key formatting (`internal/engine/engine.go`): `rl:{<ruleId>:<dimValue>}[:suffix]`, deterministic and collision-safe (delimiter escaping). `docs/adr/002-key-format-hash-tags.md` records why the tag wraps rule id AND dimension value.
- [ ] 4.2 [A] Rule matching: select every rule that matches the request dimensions (user, API key, IP, route, combinations).
- [ ] 4.3 [A] Combine decisions: apply all matching rules, deny if any denies, return the most restrictive `retryAfter`. `docs/adr/003-multi-rule-semantics.md` documents partial consumption when one rule denies.
- [ ] 4.4 [B] Cost per request end to end (request, engine, Lua arg), including a cost larger than capacity (must deny, not loop).
- [ ] 4.5 [B] Structured decision log line includes rule id, algorithm, rule-set version (from 1.5), decision.

**Done when**: 4.1 property-style test: same inputs same key; different subjects never collide; 4.3 tests for allow-all, deny-one, two denies with different retry-after; 4.4 cost 5 consumes 5; 4.5 log shows version and changes after a reload.
**Defend**: Why does the tag wrap both rule id and dimension value? What does a rule spanning two slots cost you in atomicity?

---

## Phase 5: Resilience

**Goal**: Redis slowness or outage degrades gracefully per rule.
**Learn**: state machines, `sync.Mutex`, fail-open vs fail-closed, deadlines driving policy.

- [ ] 5.1 [A] Circuit breaker (`internal/fallback/breaker.go`): closed, open, half-open; failure threshold, cooldown, single-probe recovery.
- [ ] 5.2 [A] Local token bucket (`internal/fallback/local_limiter.go`): Go port of the token bucket that must match the Phase 2 golden vectors (injectable clock); thread-safe.
- [ ] 5.3 [A] Failure policy handler (`internal/fallback/policy.go`): per-rule `fail_open` / `fail_closed`; with the fallback limiter, route to local limiter first, then the policy for the residual case. `docs/adr/004-failure-policy.md`.
- [ ] 5.4 [B] Deadline wiring: strict timeout on the Redis call; timeout counts as a breaker failure and triggers the policy.

**Done when**: 5.1 table tests for every transition under `-race`; 5.2 matches golden vectors; 5.3 tests for both policies; 5.4 an injected slow Redis returns within the deadline, never hangs.
**Defend**: Fail-open vs fail-closed: what does each protect, and which would you choose for a login endpoint vs a public read API?

---

## Phase 6: Edge (gRPC hardening, extractors, HTTP middleware, headers)

**Goal**: the service is usable by real clients.
**Learn**: protobuf compatibility, deadlines/cancellation, proxy trust, HTTP header semantics.

- [ ] 6.1 [A] gRPC handler hardening: deadline propagation (`ctx.Done()`), error mapping, stable proto fields (no renumbering; reserved fields policy).
- [ ] 6.2 [A] Key extractors (`pkg/middleware/extractor.go`): pluggable interface; IP with `X-Forwarded-For` honouring only trusted proxy CIDRs, Bearer token, API key.
- [ ] 6.3 [B] HTTP middleware (`pkg/middleware/http.go`): extract key, call `Allow`, set `X-RateLimit-Limit/Remaining/Reset`, `Retry-After` on 429, return 429. Learner decides Reset semantics.
- [ ] 6.4 [C] README section documenting header semantics and example requests.

**Done when**: 6.1 cancelled/expired context returns promptly; 6.2 spoofed `X-Forwarded-For` from an untrusted peer is ignored (test); 6.3 allowed responses carry the three headers, 429 carries `Retry-After`; 6.4 README states Reset unit and meaning.
**Defend**: Why must you not trust `X-Forwarded-For` blindly? What is a client expected to do with `Retry-After`?

---

## Phase 7: Observability (can be done before Phase 6)

**Goal**: see what the limiter is doing.
**Learn**: counters vs histograms, label cardinality, Prometheus exposition.

- [ ] 7.1 [B] Counters: allowed/denied by rule, Redis errors, fallback activations, breaker state.
- [ ] 7.2 [A] Decision-latency histogram: choose bucket boundaries around the observed latency and justify them (W first: a sandbox histogram).
- [ ] 7.3 [C] `/metrics` endpoint wired into the server.
- [ ] 7.4 [C] (stretch) Grafana dashboard JSON.

**Done when**: 7.1 a test drives allow/deny/error paths and asserts counter values; 7.2 histogram shows a plausible distribution under load; 7.3 `curl /metrics` shows all metric families.
**Defend**: Why is a high-cardinality label (per user) a problem, and why only per rule?

---

## Phase 8: Redis Cluster (independent; stretch parts marked)

**Goal**: prove co-location, per-node script loading, and what scaling does and does not give.
**Learn**: key slots, hash tags, per-node script cache, hot keys.

- [ ] 8.1 [C] Local cluster via docker-compose (for example 3 masters; record resource limits).
- [ ] 8.2 [A] Slot verification: `CLUSTER KEYSLOT` test that all keys one script touches share a slot; script manager loads scripts on every master (`ForEachMaster`) and survives a node's script cache reset.
- [ ] 8.3 [B] (stretch) Scale test: throughput with 3 vs more masters. Label any result as single-host and CPU-bound; do not extrapolate beyond what you measured.
- [ ] 8.4 [A] Hot-key demonstration and write-up: one very busy subject saturates one shard regardless of cluster size; list mitigations (not implemented).

**Done when**: 8.2 `CROSSSLOT` never occurs in tests; scripts work after restarting a node; 8.3 `reports/cluster-scaling.md` with environment and method; 8.4 hot-key run shows one shard saturated while others idle.
**Defend**: Why does the hash tag need to wrap the whole subject, and what is the hot-key limit of any key-based sharding?

---

## Phase 9: Proof runs

- [ ] 9.1 [A] Concurrency hammer (`test/concurrency_test.go`): N goroutines on one key; admitted count never exceeds the limit (with and without refill complexity: use a fixed-window or a token bucket with refill disabled for the exact-count assertion).
- [ ] 9.2 [B] Benchmark (`test/benchmark_test.go`): P50 and P99 of a decision with method recorded in `reports/`. Compare against the 1 ms / 3 ms **hypotheses**; the criterion is a recorded measurement, not a pass/fail.
- [ ] 9.3 [B] Recorded Redis-outage run: kill or pause Redis mid-load; breaker trips, fallback engages, policy applied, recovery after cooldown; save logs and counters in `reports/outage-run.md`.

**Done when**: 9.1 passes repeatedly under `-race`; 9.2 report has hardware, versions, run count, P50/P99; 9.3 report shows the timeline (trip, fallback, recovery).
**Defend**: Why can a concurrency test pass once and still be wrong? What does your benchmark not measure?

---

## Phase 10: Ship

- [ ] 10.1 [C] README: architecture, algorithm selection guidance (from ADR 001), header semantics, how to run tests and benchmarks.
- [ ] 10.2 [A] ADR sweep: every decision in the log below has an ADR or an explicit "default accepted" note.
- [ ] 10.3 [A] Final cold rebuild of one Tier A brick of your choice, plus `docs/spec-traceability.md` review: every row verified.

**Done when**: a new reader can run the stack and reproduce the proof runs from the README; traceability has no unmapped requirement.

---

## Decision log (proposed by audit; confirm or change, then write the ADR)

| ID  | Decision                                                                               | Status   | ADR        |
| --- | -------------------------------------------------------------------------------------- | -------- | ---------- |
| D1  | `Limiter.Check(ctx, key, cost)`; no client `now`; time from Redis `TIME`               | Proposed | 001 or 005 |
| D2  | Key format `rl:{<ruleId>:<dimValue>}[:suffix]`                                         | Proposed | 002        |
| D3  | Multi-rule semantics: apply all, deny if any, most restrictive `retryAfter`; no refund | Proposed | 003        |
| D4  | Failure policy: local fallback then per-rule policy                                    | Proposed | 004        |
| D5  | Middleware: Go `http.Handler` (MVP); NestJS guard stretch                              | Proposed | -          |
| D6  | Rule watching: fsnotify vs polling                                                     | Open     | -          |

## Benchmark log

| Date | Hardware / OS | Go / Redis versions | Method | P50 | P99 | Runs | Notes |
| ---- | ------------- | ------------------- | ------ | --- | --- | ---- | ----- |

## Debts

| ID  | Debt                                                                                    | Created in |
| --- | --------------------------------------------------------------------------------------- | ---------- |
| T1  | Phase 2 "verified" claim has no linked tests (see 2.4)                                  | Audit      |
| T2  | `algorithm.Limiter` interface may still carry a `now` argument; check and align with D1 | Audit      |
| T3  | Phase 1 `Rule` fields vs spec (cost is per request; failure policy per rule): confirm   | Audit      |

## Open questions

1. Time per week and any deadline (assumed 6 to 8 h/week, none).
2. Hardware limits for a local multi-node cluster (RAM/CPU)?
3. Do you want the NestJS guard (stretch) as a TypeScript bridge exercise?
4. Is the Redis version in use one where `TIME` inside scripts is fine? (verify)

---

## Session changelog

- **Session 1**: Phase 1 config and hot-reload complete; `-race` verified.
- **Session 2**: Phase 2 Tasks 2.1 to 2.3 complete (microsecond lazy refill, `HMGET`/`HSET`, retry-after, idle TTL).
- **Audit session**: guide, tasks, rules and traceability regenerated against the spec; retro bricks 1.5, 1.6, 2.4 to 2.7 added; active brick 2.4 then 3.1.
