-- Token Bucket Lua Script
--
-- KEYS[1] : Base Redis key for hash storage (e.g. {rl:rule_id:dimension}:bucket)
-- ARGV[1] : Bucket capacity / maximum burst limit (number)
-- ARGV[2] : Refill rate in tokens per second (number / float)
-- ARGV[3] : Request cost (number)
-- ARGV[4] : Idle TTL in seconds (number)
--
-- Output Contract:
--   { allowed (0 or 1), remaining, retry_after_seconds, reset_at_timestamp }

-- 1. Unpack & parse arguments
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local idle_ttl = tonumber(ARGV[4])

-- 2. Obtain current server timestamp from Redis with microsecond precision
local time_res = redis.call('TIME')
local now_sec = tonumber(time_res[1])
local now_usec = tonumber(time_res[2])
local now = now_sec + (now_usec / 1000000.0)

-- Default return values
local allowed = 0
local remaining = 0
local retry_after = 0
local reset_at = math.ceil(now)

-- =========================================================================
-- LEARNER IMPLEMENTATION AREA:
--
-- Step A: Fetch current bucket state from the Redis Hash:
--         - local data = redis.call('HMGET', key, 'tokens', 'last_refill')
--         - If data[1] is false/nil (brand new bucket):
--             tokens = capacity
--             last_refill = now
--         - Else:
--             tokens = tonumber(data[1])
--             last_refill = tonumber(data[2])
--             Calculate elapsed time: elapsed = math.max(0, now - last_refill)
--             Replenish tokens: tokens = math.min(capacity, tokens + (elapsed * refill_rate))
--
-- Step B: Evaluate if available tokens satisfy the request cost:
--         - If cost > tokens (Rejected):
--             allowed = 0
--             remaining = math.floor(tokens)
--             local missing_tokens = cost - tokens
--             retry_after = math.max(1, math.ceil(missing_tokens / refill_rate))
--             reset_at = math.ceil(now + retry_after)
--         - Else (Allowed):
--             allowed = 1
--             tokens = tokens - cost
--             Save updated state: redis.call('HSET', key, 'tokens', tostring(tokens), 'last_refill', tostring(now))
--             Set idle cleanup: redis.call('EXPIRE', key, idle_ttl)
--             remaining = math.floor(tokens)
--             retry_after = 0
--             reset_at = math.ceil(now + ((capacity - tokens) / refill_rate))
-- =========================================================================

-- TODO: Write the logic for Step A and Step B here!

local data = redis.call('HMGET', key, 'tokens', 'last_refill')

local tokens = data[1]
local last_refill = data[2]

if not data[1] then
    tokens = capacity
    last_refill = now
else
    tokens = tonumber(tokens)
    last_refill = tonumber(last_refill)

    local elapsed_time = math.max(0, now - last_refill)
    tokens = math.min(capacity, tokens + (elapsed_time * refill_rate))
end


if cost > tokens then
    allowed = 0
    remaining = math.floor(tokens)
    local missing_tokens = cost - tokens
    retry_after = math.max(1, math.ceil(missing_tokens / refill_rate))
    reset_at = math.ceil(now + retry_after)
else
    allowed = 1
    tokens = tokens - cost
    redis.call('HSET', key, 'tokens', tostring(tokens), 'last_refill', tostring(now))
    redis.call('EXPIRE', key, idle_ttl)
    remaining = math.floor(tokens)
    retry_after = 0
    reset_at = math.ceil(now + (capacity - tokens) / refill_rate)
end

-- 3. Return result tuple
return { allowed, remaining, retry_after, reset_at }
