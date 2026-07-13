//go:build smoke && (linux || windows)

package main

import (
	"testing"
	"time"
)

func TestNativeLifecycleSmoke(t *testing.T) {
	backend, err := NewPlatformEventBackend(Config{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("create native lifecycle smoke backend: %v", err)
	}
	runLifecycleSmoke(t, backend)
}
