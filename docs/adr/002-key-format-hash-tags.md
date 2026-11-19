# ADR 002: Redis Key Format and Hash Tags

## Status
Accepted

## Context
In a distributed rate limiter backed by Redis, keys must:
1. Prevent collisions across different rules and subjects (tenants, IPs, users, routes).
2. Avoid delimiter injection attacks (e.g., embedding separators or braces inside a user ID or rule ID).
3. Support Redis Cluster sharding without causing `CROSSSLOT` errors when multi-key scripts or transactions are used.
4. Prevent hot-shard bottlenecks where an entire rule or tenant saturates a single cluster node.
5. Support optional ancillary sub-keys (e.g. locks, metadata, timestamp trackers) that are guaranteed to co-locate on the same slot as the primary rate limit key.

## Considered Options
1. **Plain concatenation**: `rl:<ruleId>:<dimValue>[:suffix]`
   - *Problem*: In Redis Cluster, CRC16 is computed over the entire key. Suffixes (e.g. `...:lock`) will hash to different slots than the main key, causing `CROSSSLOT` errors in multi-key operations.
2. **Rule-only hash tag**: `rl:{<ruleId>}:<dimValue>[:suffix]`
   - *Problem*: CRC16 hashes only `<ruleId>`. All users under `login` or `api-tier-1` hash to the exact same slot, concentrating all traffic for that rule onto a single Redis shard (hot shard).
3. **Subject-only hash tag**: `rl:<ruleId>:{<dimValue>}[:suffix]`
   - *Problem*: CRC16 hashes only `<dimValue>`. While different subjects distribute well, a heavy subject touching multiple rules still concentrates on one node.
4. **Composite hash tag**: `rl:{<ruleId>:<dimValue>}[:suffix]`
   - *Benefit*: CRC16 hashes both `<ruleId>` and `<dimValue>`. Traffic for a given rule is distributed evenly across all 16,384 slots by subject. Simultaneously, all auxiliary keys for that specific (rule, subject) pair share the exact same hash tag, guaranteeing slot co-location without `CROSSSLOT` errors.

## Decision
We adopt the composite hash tag format:
```
rl:{<escapedRuleId>:<escapedDimValue>}[:<suffix>]
```

To guarantee injectivity (no collisions) and prevent Redis Cluster hash tag corruption:
- Special delimiter characters `:`, `{`, `}`, and the escape symbol `%` are percent-encoded (`%3A`, `%7B`, `%7D`, `%25`) before assembly.
- Any optional suffix is appended *outside* the `{...}` hash tag, preceded by `:`.

## Consequences
- **Positive**: Strict 1-to-1 mapping. No two distinct `(ruleId, dimValue)` pairs can produce the same key.
- **Positive**: Cluster-safe. Redis Cluster computes CRC16 only on `{<escapedRuleId>:<escapedDimValue>}`. Auxiliary keys with suffixes co-locate on the same node.
- **Positive**: Uniform distribution across cluster shards, avoiding single-shard saturation per rule.
- **Negative / Trade-off**: Minor CPU overhead for string escaping during key formatting. Keys with special characters are slightly longer due to percent-encoding.

