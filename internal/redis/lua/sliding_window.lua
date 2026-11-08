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

-- =========================================================================
-- LEARNER IMPLEMENTATION AREA:
--
-- Step A: Calculate bucket indices for the current and previous windows:
--         - curr_bucket = tostring(math.floor(now / window))
--         - prev_bucket = tostring(math.floor(now / window) - 1)
--
-- Step B: Fetch current and previous counts from the Redis Hash:
--         - local counts = redis.call('HMGET', key, curr_bucket, prev_bucket)
--         - Convert counts[1] (curr) and counts[2] (prev) to numbers (default to 0 if false/nil)
--
-- Step C: Calculate the sliding overlap weight:
--         - time_into_current = now % window
--         - prev_weight = (window - time_into_current) / window
--         - estimated_count = curr_count + (prev_count * prev_weight)
--
-- Step D: Check quota against limit:
--         - If estimated_count + cost > limit:
--             allowed = 0
--             remaining = math.max(0, math.floor(limit - estimated_count))
--             retry_after = math.max(1, math.ceil(window - time_into_current))
--             reset_at = now + retry_after
--         - Else (Allowed):
--             allowed = 1
--             Increment current bucket: redis.call('HINCRBY', key, curr_bucket, cost)
--             Refresh key TTL: redis.call('EXPIRE', key, window * 2)
--             remaining = math.max(0, math.floor(limit - (estimated_count + cost)))
--             retry_after = 0
--             reset_at = now + (window - time_into_current)
-- =========================================================================

-- TODO: Write the logic for Steps A, B, C, and D here!
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
