package redis

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
)

var (
	//go:embed lua/fixed_window.lua
	fixedWindowLua string

	//go:embed lua/sliding_window.lua
	slidingWindowLua string

	//go:embed lua/token_bucket.lua
	tokenBucketLua string
)

// ScriptName identifies one of the rate limiting Lua scripts.
type ScriptName string

const (
	ScriptFixedWindow   ScriptName = "fixed_window"
	ScriptSlidingWindow ScriptName = "sliding_window"
	ScriptTokenBucket   ScriptName = "token_bucket"
)

var (
	ErrUnknownScript = errors.New("redis: unknown script name")
)

// ScriptManager manages preloading, caching SHA1 digests, and executing rate limiting scripts.
// It handles NOSCRIPT errors by reloading the script and retrying the execution once.
type ScriptManager struct {
	client redis.UniversalClient

	mu      sync.RWMutex
	sources map[ScriptName]string
	shas    map[ScriptName]string
}

// NewScriptManager creates a new ScriptManager and loads the embedded scripts into Redis.
func NewScriptManager(client redis.UniversalClient) (*ScriptManager, error) {
	if client == nil {
		return nil, errors.New("redis client cannot be nil")
	}

	sm := &ScriptManager{
		client: client,
		sources: map[ScriptName]string{
			ScriptFixedWindow:   fixedWindowLua,
			ScriptSlidingWindow: slidingWindowLua,
			ScriptTokenBucket:   tokenBucketLua,
		},
		shas: make(map[ScriptName]string),
	}

	if err := sm.LoadAll(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to preload lua scripts: %w", err)
	}

	return sm, nil
}

// LoadAll preloads all registered scripts into Redis via SCRIPT LOAD and caches their SHAs.
func (sm *ScriptManager) LoadAll(ctx context.Context) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// TODO [Learner Brick 3.2]:
	// Iterate through sm.sources, call sm.client.ScriptLoad(ctx, src).Result(),
	// and record the resulting SHA1 in sm.shas[name].
	// If ScriptLoad returns an error, return it wrapped.

	for name, src := range sm.sources {
		sha, err := sm.client.ScriptLoad(ctx, string(src)).Result()

		if err != nil {
			return fmt.Errorf("failed to compile lua scripts: %w", err)
		}
		sm.shas[name] = sha
	}

	return nil
}

// GetSHA returns the cached SHA1 digest for a given script name.
func (sm *ScriptManager) GetSHA(name ScriptName) (string, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	sha, ok := sm.shas[name]
	return sha, ok
}

// Run executes a script using EVALSHA with automatic NOSCRIPT recovery (reloading and retrying once).
func (sm *ScriptManager) Run(ctx context.Context, name ScriptName, keys []string, args ...any) (any, error) {
	// TODO [Learner Brick 3.2]:
	// 1. Check if the script exists in sm.sources. Return ErrUnknownScript if not found.
	// 2. Fetch the cached SHA from sm.shas (read lock). If not found, load it.
	// 3. Attempt sm.client.EvalSha(ctx, sha, keys, args...).Result().
	// 4. If an error occurs and is a NOSCRIPT error (use isNoScriptErr(err)):
	//    a. Reload the script: sm.client.ScriptLoad(ctx, source).Result()
	//    b. Update sm.shas under write lock.
	//    c. Retry sm.client.EvalSha with the new SHA.
	// 5. Return result and error.

	sm.mu.RLock()
	src, ok := sm.sources[name]
	sm.mu.RUnlock()

	if !ok {
		return nil, ErrUnknownScript
	}

	sha, ok := sm.GetSHA(name)
	if !ok {
		newSha, err := sm.client.ScriptLoad(ctx, src).Result()

		if err != nil {
			return nil, fmt.Errorf("failed to reload script %s: %w", name, err)
		}
		sm.mu.Lock()
		sm.shas[name] = newSha
		sm.mu.Unlock()
		sha = newSha
	}

	res, err := sm.client.EvalSha(ctx, sha, keys, args...).Result()
	if err == nil {
		return res, nil
	}

	if !isNoScriptErr(err) {
		return nil, err
	}

	newSha, err := sm.client.ScriptLoad(ctx, src).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to reload script %s: %w", name, err)
	}

	sm.mu.Lock()
	sm.shas[name] = newSha
	sm.mu.Unlock()

	return sm.client.EvalSha(ctx, newSha, keys, args...).Result()
}

// isNoScriptErr checks if an error returned by Redis is a NOSCRIPT error.
func isNoScriptErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.HasPrefix(err.Error(), "NOSCRIPT")
}
