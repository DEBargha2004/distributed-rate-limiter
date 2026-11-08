package algorithm

import "time"

// Decision represents the result of a rate limit check.
type Decision struct {
	Allowed    bool          // Whether request is admitted
	Limit      int64         // Configured capacity or window limit
	Remaining  int64         // Remaining tokens/requests allowed
	RetryAfter time.Duration // How long the client must wait if rejected
	ResetAt    time.Time     // When the current counter/window resets
	RuleID     string        // ID of rule enforced
}
