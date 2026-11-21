-- Sliding Window Counter Lua Script
--
-- KEYS[1] : Base Redis key for hash storage (e.g. {rl:rule_id:dimension}:sliding)
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

-- 2. Obtain current server timestamp from Redis (Seconds)
local time_res = redis.call('TIME')
local now = tonumber(time_res[1])

-- Default return values
local allowed = 0
local remaining = 0
local retry_after = 0
local reset_at = now + window

local curr_bucket = tostring(math.floor(now / window))
local prev_bucket = tostring(math.floor(now / window) - 1)

local counts = redis.call('HMGET', key, curr_bucket, prev_bucket)

local curr_count = tonumber(counts[1]) or 0
local prev_count = tonumber(counts[2]) or 0

local time_into_current = now % window
local prev_weight = (window - time_into_current) / window
local estimated_count = prev_weight * prev_count + curr_count


if estimated_count + cost > limit then
    allowed = 0
    remaining = math.max(0, math.floor(limit - estimated_count))
    retry_after = math.max(1, math.ceil(window - time_into_current))
    reset_at = now + retry_after
else
    allowed = 1
    redis.call('HINCRBY', key, curr_bucket, cost)
    redis.call('EXPIRE', key, 2 * window)
    remaining = math.max(0, math.floor(limit - (estimated_count + cost)))
    retry_after = 0
    reset_at = now + (window - time_into_current)
end



-- 3. Return result tuple
return { allowed, remaining, retry_after, reset_at }
