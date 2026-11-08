package engine

import (
	"distributed-rate-limiter/internal/config"
)

// FormatKey generates a deterministic Redis Cluster compatible key.
// Uses hash tags e.g. rl:{ruleId}:{dimensionValue} so that all keys for this dimension slot co-locate on the same shard.
func FormatKey(ruleID string, dimensionValue string) string {
	// TODO: Compose deterministic key with Redis hash tags
	panic("TODO: implement FormatKey")
}

// Engine matches incoming requests against rules and resolves precedence.
type Engine struct {
	watcher *config.Watcher
}

func NewEngine(watcher *config.Watcher) *Engine {
	return &Engine{watcher: watcher}
}

// GetRule finds a rule by ID or matches based on request dimensions.
func (e *Engine) GetRule(ruleID string) (*config.Rule, error) {
	// TODO: Implement rule lookup and matching
	panic("TODO: implement GetRule")
}
