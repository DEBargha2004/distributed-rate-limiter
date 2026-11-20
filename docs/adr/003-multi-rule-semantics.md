# ADR 003: Multi-Rule Evaluation Semantics and Partial Consumption

## Status
Accepted

## Context
Incoming requests frequently match multiple rate limiting rules simultaneously (e.g. an IP-level DDoS defense rule, a user-level subscription tier quota, and a route-level expensive endpoint limit).

In a distributed architecture backed by Redis Cluster:
1. Each rule-subject pair produces a distinct hash tag `rl:{<ruleId>:<dimValue>}` (ADR 002).
2. Different rules for the same incoming request almost always hash to different CRC16 slots across different Redis Cluster nodes.
3. Redis prohibits multi-key operations or Lua scripts across different hash slots (`CROSSSLOT` error).
4. As a result, multi-rule evaluation cannot be executed in a single atomic Lua script. Rules must be evaluated as independent Redis commands.

We must define:
- How multiple decisions are combined into a single client-facing response.
- Whether evaluation short-circuits on the first denied rule.
- How partial consumption is handled when one rule allows and a subsequent rule denies.

## Considered Options

### 1. Short-circuit on first deny vs. Complete evaluation
- **Option A (Short-circuit)**: Stop evaluating rules as soon as one rule returns `allowed: false`.
  - *Problem*: If Rule 1 (IP burst) denies with `retry_after: 50ms`, but Rule 2 (Daily user quota) is exhausted with `retry_after: 12h`, short-circuiting returns `50ms`. The client immediately retries, Rule 1 passes, and only then is the client hit with a `12h` block. This triggers thundering retries and misleads clients about the true bottleneck.
- **Option B (Complete evaluation)**: Evaluate all matching rules unconditionally and combine the results.
  - *Benefit*: Yields the true bottleneck capacity (`min(remaining)`) and the most restrictive backoff delay (`max(retry_after)`), minimizing client retry thrashing.

### 2. Transactional rollback (Refunding) vs. Accepting partial consumption
- **Option A (Distributed Rollback / Saga)**: If Rule 1 consumes a token but Rule 2 denies, issue an asynchronous refund or decrement to Rule 1's key.
  - *Problem*: Refunding across algorithms is fraught with edge cases (e.g., fixed window reset during in-flight network round-trip decrementing into negative numbers; sliding window timestamp removal). Network failures during refund require a distributed transaction coordinator, adding latency and complexity.
- **Option B (Accept partial consumption)**: Tokens consumed by earlier allowed rules are not refunded when a later rule denies.
  - *Benefit*: A denied request still consumed gateway bandwidth, CPU, and Redis resources. Charging the caller's IP/dimension for processing an invalid or over-quota request acts as natural DDoS backpressure.

## Decision

We adopt **complete evaluation with deny-if-any composition and accepted partial consumption**:

1. **Deny-if-any**: The request is admitted (`allowed: true`) if and only if **all** matching rules allow. If any rule denies, the aggregate response is `allowed: false`.
2. **Most Restrictive Wait**: If denied, `retry_after` is set to $\max(\text{retry\_after}_i)$ across all denying rules, and `reset_at` is set to the latest reset time.
3. **Bottleneck Metrics**: `remaining` is reported as $\min(\text{remaining}_i)$ across all evaluated rules (representing the limiting bottleneck constraint).
4. **No-op for Unmatched Requests**: If a request matches zero configured rules, it is admitted unconditionally (`allowed: true`, `remaining: math.MaxInt64`, `retry_after: 0`).
5. **Partial Consumption**: Rules are evaluated independently across their respective Redis Cluster slots. Any token consumed by an admitted rule is retained; no compensation/refund transactions are issued upon subsequent rule rejection.

## Consequences

- **Positive**: Clients receive accurate backoff advice reflecting the true bottleneck, eliminating thundering-herd retry loops.
- **Positive**: Strict defense-in-depth: IP quotas continue to penalize attackers attempting to brute-force accounts or endpoints even if account-level quotas reject them.
- **Positive**: Avoids high-latency distributed transaction protocols (2PC/Saga) across Redis shards.
- **Negative / Trade-off**: Under high contention with mixed rule exhaustion, a user might consume an IP quota token on a request that was denied by a route quota.

