package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeTempConfigFile(t *testing.T, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "rules.yaml")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return filePath
}

const sampleValidYAML = `
version: "v1.0"
rules:
  test_rule:
    id: "test_rule"
    dimension: "user"
    algorithm: "token_bucket"
    capacity: 100
    refill_rate: 10.0
    default_cost: 1
    failure_policy: "fail_open"
`

const sampleUpdatedYAML = `
version: "v2.0"
rules:
  test_rule:
    id: "test_rule"
    dimension: "user"
    algorithm: "token_bucket"
    capacity: 200
    refill_rate: 20.0
    default_cost: 1
    failure_policy: "fail_open"
  new_ip_rule:
    id: "new_ip_rule"
    dimension: "ip"
    algorithm: "fixed_window"
    limit: 50
    window: 30s
    default_cost: 1
    failure_policy: "fail_closed"
`

func TestWatcher_InitialLoad(t *testing.T) {
	t.Run("non-existent file", func(t *testing.T) {
		_, err := NewWatcher("/non/existent/path/rules.yaml")
		if err == nil {
			t.Fatalf("expected error for non-existent file, got nil")
		}
	})

	t.Run("invalid YAML syntax", func(t *testing.T) {
		f := writeTempConfigFile(t, "rules: [broken yaml:::")
		_, err := NewWatcher(f)
		if err == nil {
			t.Fatalf("expected error for invalid YAML syntax, got nil")
		}
	})

	t.Run("valid file loads snapshot", func(t *testing.T) {
		f := writeTempConfigFile(t, sampleValidYAML)
		w, err := NewWatcher(f)
		if err != nil {
			t.Fatalf("failed to create watcher: %v", err)
		}
		rs := w.Get()
		if rs == nil {
			t.Fatalf("expected non-nil RuleSet snapshot")
		}
		if rs.Version != "v1.0" {
			t.Fatalf("expected version v1.0, got %s", rs.Version)
		}
		if len(rs.Rules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(rs.Rules))
		}
	})
}

func TestWatcher_ReloadAndFaultTolerance(t *testing.T) {
	f := writeTempConfigFile(t, sampleValidYAML)
	w, err := NewWatcher(f)
	if err != nil {
		t.Fatalf("failed to init watcher: %v", err)
	}

	initialRS := w.Get()

	// 1. Overwrite with invalid configuration
	if err := os.WriteFile(f, []byte("rules: { invalid_yaml"), 0644); err != nil {
		t.Fatalf("failed to write bad config: %v", err)
	}

	// Reload should return an error
	if err := w.Reload(); err == nil {
		t.Fatalf("expected reload error on invalid config, got nil")
	}

	// The existing snapshot MUST NOT be corrupted or replaced
	currentRS := w.Get()
	if currentRS != initialRS {
		t.Fatalf("expected existing snapshot to remain untouched on reload failure")
	}

	// 2. Overwrite with valid updated configuration
	if err := os.WriteFile(f, []byte(sampleUpdatedYAML), 0644); err != nil {
		t.Fatalf("failed to write updated config: %v", err)
	}

	if err := w.Reload(); err != nil {
		t.Fatalf("expected successful reload, got: %v", err)
	}

	updatedRS := w.Get()
	if updatedRS.Version != "v2.0" {
		t.Fatalf("expected version v2.0, got %s", updatedRS.Version)
	}
	if len(updatedRS.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(updatedRS.Rules))
	}
}

func TestWatcher_ConcurrentReadsAndReload(t *testing.T) {
	f := writeTempConfigFile(t, sampleValidYAML)
	w, err := NewWatcher(f)
	if err != nil {
		t.Fatalf("failed to init watcher: %v", err)
	}

	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	// Launch 20 concurrent readers hammering w.Get()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					rs := w.Get()
					if rs == nil || len(rs.Rules) == 0 {
						t.Errorf("read invalid or nil ruleset during concurrent reload")
						return
					}
				}
			}
		}()
	}

	// Simulate multiple reloads while reads are happening
	for i := 0; i < 5; i++ {
		time.Sleep(10 * time.Millisecond)
		if err := w.Reload(); err != nil {
			t.Fatalf("failed reload during concurrent test: %v", err)
		}
	}

	close(stopCh)
	wg.Wait()
}
