package test

import (
	"testing"
)

func TestConcurrentHammering(t *testing.T) {
	// TODO:
	// 1. Launch N goroutines (e.g. 100) hitting the exact same key concurrently.
	// 2. Assert that the total number of admitted requests strictly matches the configured quota.
	// 3. Assert zero race conditions occur.
}
