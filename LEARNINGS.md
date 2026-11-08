# Distributed Rate Limiter: Engineering Learnings & Architecture Journey

> **About this document**: A comprehensive engineering log detailing the core architectural choices, mathematical models, distributed systems trade-offs, and subtle edge cases solved while building a high-throughput distributed rate limiter in Go, Redis, and Lua.

---

## 📑 Table of Contents
1. [Architectural Overview](#1-architectural-overview)
2. [Phase 1: Configuration & Lock-Free Hot-Reload Engine](#2-phase-1-configuration--lock-free-hot-reload-engine)
3. [Phase 2: Atomic Rate Limiting in Redis & Lua](#3-phase-2-atomic-rate-limiting-in-redis--lua)
   - [Why Lua in Redis?](#why-lua-in-redis)
   - [Fixed Window Counter](#fixed-window-counter)
   - [Sliding Window Counter](#sliding-window-counter)
   - [Token Bucket Algorithm](#token-bucket-algorithm)
4. [Gotchas & Hard-Won Lessons](#4-gotchas--hard-won-lessons)
5. [Algorithm Trade-Off Matrix](#5-algorithm-trade-off-matrix)
6. [Roadmap & Next Milestones](#6-roadmap--next-milestones)

---

## 1. Architectural Overview

Rate limiters sit on the **critical hot path of every incoming request** in an API gateway or distributed service mesh. A slow or buggy rate limiter degrades the entire system's latency and availability.

```
Incoming Request (IP, API Key, User ID)
           │
           ▼
┌────────────────────────────────────────────────────────┐
│ Go Application Layer (Zero-Lock Read Path)             │
│ - Reads active RuleSet via sync/atomic.Pointer         │
│ - Matches rule dimensions & resolves precedence        │
│ - Circuit breaker checks Redis health                  │
└──────────────────────────┬─────────────────────────────┘
                           │ 1 Network RTT (EVALSHA)
                           ▼
┌────────────────────────────────────────────────────────┐
│ Redis Single-Threaded Event Loop (Atomic Lua)          │
│ - Authoritative server clock: redis.call('TIME')       │
│ - Slot co-located via Hash Tags: {rl:rule:dimension}   │
│ - Check-then-act execution with zero race conditions   │
└──────────────────────────┬─────────────────────────────┘
                           │
                           ▼
Returns [allowed, remaining, retry_after, reset_at]
Mapped directly to RFC HTTP headers (429, Retry-After, X-RateLimit-*)
```

---

## 2. Phase 1: Configuration & Lock-Free Hot-Reload Engine

*Location:* [`internal/config/`](internal/config/)

### The Problem
In production, rate-limit policies change frequently (marketing campaigns, DDoS mitigation, tier upgrades). The rate limiter must reload configuration dynamically from disk **without restarting the server and without blocking active HTTP/gRPC requests**.

### Why Not `sync.RWMutex`?
A naive approach protects the shared configuration struct with an `RWMutex`:
* Readers acquire `RLock()`.
* The reloader acquires `Lock()`.

Under 100,000+ requests/sec across dozens of worker goroutines, **read-lock contention degrades CPU cache lines** because every `RLock` modifies the internal mutex counter. Furthermore, a slow reload write-lock blocks all incoming traffic.

### The Solution: `sync/atomic.Pointer[RuleSet]`
Go 1.19+ introduced strongly-typed atomic pointers.
* **Read Path**: `w.current.Load()` reads the 64-bit memory address of the active `RuleSet`. It is **100% lock-free**, performs zero heap allocations, and never blocks readers.
* **Write Path**:
  1. Parse the new YAML configuration into an isolated, temporary struct in memory.
  2. Validate all rules (checking positive limits, non-empty IDs, valid algorithms).
  3. **Atomically swap** the pointer: `w.current.Store(newRuleSet)`.
  4. If parsing or validation fails, return the error and discard the temp object. **The active runtime snapshot remains completely untouched and healthy.**

---

## 3. Phase 2: Atomic Rate Limiting in Redis & Lua

*Location:* [`internal/redis/lua/`](internal/redis/lua/)

### Why Lua in Redis?
Evaluating rate limits requires a **Check-Then-Act** sequence:
1. Read current count / token balance.
2. Check if `current + cost <= limit`.
3. If allowed, increment count or deduct tokens and update expiry.

* **Why standard Redis commands fail**: Running `GET` followed by `INCR` creates a classic race condition where concurrent clients both see capacity and both increment, exceeding the quota.
* **Why `MULTI/EXEC` fails**: Redis transactions are blind execution queues; you cannot use the return value of a `GET` inside the transaction to conditionally decide whether to run `INCR`.
* **The Lua Guarantee**: Redis executes Lua scripts on its main single-threaded event loop. A script executes as **one single, indivisible atomic operation** in **1 network round-trip**.

---

### Fixed Window Counter ([`fixed_window.lua`](internal/redis/lua/fixed_window.lua))

* **Data Structure**: A single Redis String key storing an integer counter.
* **Key Lifecycle**:
  ```lua
  redis.call('INCRBY', key, cost)
  if current == 0 then
      redis.call('EXPIRE', key, window)
  end
  ```
* **The "Accidental Rolling Lockout" Bug Solved**:
  * If `EXPIRE` is called on *every* request, the window countdown keeps getting pushed forward whenever the user is active, turning a fixed window into an accidental rolling lockout.
  * **Fix**: Only set `EXPIRE` on the **first request of the window** (`current == 0`).
* **The 2x Boundary Burst Vulnerability**:
  * A user with a limit of 100 req/min can send 100 requests at `00:59` and another 100 requests at `01:01`. The fixed window allows all 200 requests, processing 2x the quota in a 2-second interval.

---

### Sliding Window Counter ([`sliding_window.lua`](internal/redis/lua/sliding_window.lua))

* **Core Innovation**: Solves the 2x boundary burst flaw by smoothing traffic across the previous and current window using a weighted formula.
* **Data Structure**: A Redis **Hash** storing bucket counts:
  * Field 1: `curr_bucket = math.floor(now / window)`
  * Field 2: `prev_bucket = curr_bucket - 1`
* **Cluster Slot Co-location via Hash Tags**:
  * Storing both buckets under a single key `{rl:rule:dimension}:sliding` guarantees that Redis Cluster routes all hash fields to the **same shard**, eliminating `CROSSSLOT` errors.
* **The Weighting Formula**:
  $$\text{time\_into\_current} = \text{now} \pmod{\text{window}}$$
  $$\text{prev\_weight} = \frac{\text{window} - \text{time\_into\_current}}{\text{window}}$$
  $$\text{estimated\_count} = \text{curr\_count} + (\text{prev\_count} \times \text{prev\_weight})$$
* **Idle TTL Garbage Collection**:
  * TTL is set to `2 * window` on every allowed request. This ensures previous bucket data lives long enough to serve as history for the next window, while idle users are automatically cleaned up from Redis RAM.

---

### Token Bucket Algorithm ([`token_bucket.lua`](internal/redis/lua/token_bucket.lua))

* **Core Innovation**: Accommodates natural traffic bursts up to `capacity` while enforcing a strict long-term rate ceiling (`refill_rate` tokens/sec).
* **Data Structure**: A Redis **Hash** with two fields:
  * `tokens`: Current available token balance (float).
  * `last_refill`: Microsecond timestamp of the last replenishment.
* **Lazy Continuous Replenishment**:
  * Instead of background tick threads, replenishment is calculated on-demand:
    $$\Delta t = \max(0,\; \text{now} - \text{last\_refill})$$
    $$\text{tokens} = \min\big(\text{capacity},\; \text{tokens} + (\Delta t \times \text{refill\_rate})\big)$$
* **Dynamic `Retry-After` Calculation**:
  * When a request is rejected because `cost > tokens`, the script computes exactly how long the client must wait:
    $$\text{missing} = \text{cost} - \text{tokens}$$
    $$\text{retry\_after} = \left\lceil \frac{\text{missing}}{\text{refill\_rate}} \right\rceil$$

---

## 4. Gotchas & Hard-Won Lessons

### 1. The Clock Authority Rule (`redis.call('TIME')`)
* **Problem**: Never pass `time.Now()` from the Go client into Redis. In a cluster of 20 API gateways, slight NTP time drift (even 50ms) leads to out-of-order buckets or premature window resets.
* **Rule**: Query `redis.call('TIME')` inside Lua. The Redis server host is the single source of truth for the entire cluster.

### 2. The NTP Backward Step Defense (`math.max`)
* **Problem**: When a server’s NTP daemon adjusts a drifting clock backward, `now - last_refill` produces a negative number. Without protection, `tokens + (elapsed * refill_rate)` **subtracts tokens**, penalizing innocent users.
* **Rule**: Always wrap elapsed time: `math.max(0, now - last_refill)`.

### 3. Redis Types in Lua (`false` vs `nil`)
* When a key or hash field does not exist in Redis, `redis.call('GET')` or `redis.call('HMGET')` returns **boolean `false`**, NOT `nil` and NOT `0`.
* Evaluating `false + cost` immediately crashes the Lua runtime with `cannot perform add operation between boolean and number`.
* **Rule**: Always use the Lua idiom `tonumber(val) or 0` or check `if not val then`.

### 4. Lua Arrays Are 1-Indexed
* Unlike Go, C, Python, or JavaScript, **Lua tables and arrays start at index 1**.
* Reading `counts[0]` returns `nil`, leading to silent logic bugs where the current bucket count is perpetually evaluated as 0.

### 5. Token Bucket State Mutation
* When refilling tokens lazily, `last_refill` **must be updated to `now`** in Redis. If the old `last_refill` is persisted, the next request will measure elapsed time from the original timestamp, refilling the same tokens twice.

---

## 5. Algorithm Trade-Off Matrix

| Algorithm | Redis Data Structure | Memory per Key | Burst Tolerance | Boundary Behavior | Best For |
|---|---|---|---|---|---|
| **Fixed Window** | String (Counter) | Minimal (~40 B) | None | Allows up to 2x burst across boundary | Low-memory, coarse limits (e.g. 10,000 req/day) |
| **Sliding Window** | Hash (2 Buckets) | Low (~100 B) | Smooth | Eliminates boundary bursts with weighted average | Standard API rate limiting (e.g. 100 req/min) |
| **Token Bucket** | Hash (`tokens`, `time`) | Low (~100 B) | High (up to capacity) | Constant sustained refill rate | Webhooks, write endpoints, bursty user traffic |

---

## 6. Roadmap & Next Milestones

* [x] **Phase 1: Configuration & Lock-Free Hot-Reload Engine** (Done)
* [x] **Phase 2: Atomic Rate Limiting in Redis & Lua** (Done)
* [ ] **Phase 3: Redis Client & Script Management**
  * `client.go`: Connection pooling for Standalone & Redis Cluster.
  * `scripts.go`: Preloading scripts via `SCRIPT LOAD`, executing via `EVALSHA`, recovering from `NOSCRIPT`.
  * `limiter.go`: Implementing the unified Go `Limiter` interface.
* [ ] **Phase 4: Dimension Matching Engine** (`internal/engine`)
* [ ] **Phase 5: Circuit Breaker & In-Memory Fallbacks** (`internal/fallback`)
* [ ] **Phase 6: gRPC Service & HTTP Middleware** (`pkg/middleware`)
* [ ] **Phase 7: Concurrency Stress Testing & Latency Benchmarks** (`test`)

