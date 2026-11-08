package config

import (
	"os"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// Watcher manages atomic loading and hot-reloading of rate limit rules.
type Watcher struct {
	filePath string
	current  atomic.Pointer[RuleSet]
}

// NewWatcher initializes a rule watcher with the provided config file path.
func NewWatcher(filePath string) (*Watcher, error) {
	w := &Watcher{
		filePath: filePath,
	}

	if err := w.Reload(); err != nil {
		return nil, err
	}
	return w, nil
}

// Get returns the current active immutable RuleSet snapshot.
func (w *Watcher) Get() *RuleSet {
	return w.current.Load()
}

// Reload parses, validates, and atomically swaps the active rules.
func (w *Watcher) Reload() error {
	data, err := os.ReadFile(w.filePath)

	if err != nil {
		return err
	}

	var rs RuleSet
	if err := yaml.Unmarshal(data, &rs); err != nil {
		return err
	}

	if err := rs.Validate(); err != nil {
		return err
	}

	//All the writes before this are committed first to the main memory
	w.current.Store(&rs)

	return nil
}
