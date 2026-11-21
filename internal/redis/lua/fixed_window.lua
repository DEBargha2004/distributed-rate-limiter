-- Fixed Window Counter Lua Script
--
-- KEYS[1] : Redis key for the counter (e.g. {rl:rule_id:dimension})
-- ARGV[1] : Limit quota (number)
-- ARGV[2] : Window duration in seconds (number)
-- ARGV[3] : Request cost (number)
--
-- Output Contract:
--   { allowed (0 or 1), remaining, retry_after_seconds, reset_at_timestamp }

-- 1. Unpack & parse arguments
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])

-- 2. Obtain current server timestamp from Redis (Seconds & Microseconds)
local time_res = redis.call('TIME')
local now = tonumber(time_res[1])

local allowed = 0
local remaining = 0
local retry_after = 0
local reset_at = now + window

local current = redis.call('GET', key)

if not current then
    current = 0
else
    current = tonumber(current)
end

if current + cost > limit then
    allowed = 0
    remaining = math.max(0, limit - current)
    retry_after = redis.call('TTL', key)
    -- If key does not exist or has no expiry
    if retry_after < 0 then
        retry_after = window
    end

    reset_at = now + retry_after
else
    allowed = 1
    redis.call('INCRBY', key, cost)

    if current == 0 then
        redis.call('EXPIRE', key, window)
    end

    remaining = limit - (current + cost)
    retry_after = 0
    reset_at = now + window
end

-- 3. Return result tuple
return { allowed, remaining, retry_after, reset_at }
