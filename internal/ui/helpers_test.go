package ui

import (
	"os"
	"testing"
	"time"
)

// waitForFile blocks until path exists on disk. Tests that trigger async
// persistence (safeGo → Save) into a t.TempDir must call this before
// returning: the test's TempDir cleanup otherwise races the goroutine and
// fails with "unlinkat … directory not empty" (flaky under -count / -race).
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("%s never landed on disk", path)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
