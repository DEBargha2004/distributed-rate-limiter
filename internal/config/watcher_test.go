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

const sampleUnversionedYAML = `
rules:
  login_limit:
    id: "login_limit"
    dimension: "ip"
    algorithm: "fixed_window"
    limit: 5
    window: 10s
    failure_policy: "fail_closed"
`

const sampleModifiedUnversionedYAML = `
rules:
  login_limit:
    id: "login_limit"
    dimension: "ip"
    algorithm: "fixed_window"
    limit: 10
    window: 10s
    failure_policy: "fail_closed"
`

func TestWatcher_VersionStamping(t *testing.T) {
	// 1. Initial load without explicit version field should generate content hash stamp
	f := writeTempConfigFile(t, sampleUnversionedYAML)
	w, err := NewWatcher(f)
	if err != nil {
		t.Fatalf("failed to init watcher: %v", err)
	}

	initialRS := w.Get()
	if initialRS == nil {
		t.Fatalf("expected non-nil ruleset")
	}
	if initialRS.Version == "" {
		t.Fatalf("expected auto-generated content hash version for unversioned config, got empty string")
	}

	// 2. Reloading identical content produces identical version hash
	if err := w.Reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	reloadedRS := w.Get()
	if reloadedRS.Version != initialRS.Version {
		t.Fatalf("expected identical content hash on unchanged reload, got %s vs %s", reloadedRS.Version, initialRS.Version)
	}

	// 3. Modifying file content produces a different version hash
	if err := os.WriteFile(f, []byte(sampleModifiedUnversionedYAML), 0644); err != nil {
		t.Fatalf("failed to update config: %v", err)
	}
	if err := w.Reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	modifiedRS := w.Get()
	if modifiedRS.Version == "" || modifiedRS.Version == initialRS.Version {
		t.Fatalf("expected new version hash after content change, got %s (initial was %s)", modifiedRS.Version, initialRS.Version)
	}

	// 4. Failed reload keeps previous version intact
	if err := os.WriteFile(f, []byte("rules: [broken yaml"), 0644); err != nil {
		t.Fatalf("failed to write broken config: %v", err)
	}
	if err := w.Reload(); err == nil {
		t.Fatalf("expected error on broken config reload, got nil")
	}
	afterFailedReload := w.Get()
	if afterFailedReload.Version != modifiedRS.Version {
		t.Fatalf("version changed after failed reload! got %s, want %s", afterFailedReload.Version, modifiedRS.Version)
	}

	// 5. Explicit version in config is preserved
	if err := os.WriteFile(f, []byte(sampleValidYAML), 0644); err != nil {
		t.Fatalf("failed to write valid versioned config: %v", err)
	}
	if err := w.Reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	explicitRS := w.Get()
	if explicitRS.Version != "v1.0" {
		t.Fatalf("expected explicit version v1.0, got %s", explicitRS.Version)
	}
}

