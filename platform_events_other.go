//go:build !linux && !darwin && !windows

package main

import "fmt"

func NewPlatformEventBackend(config Config) (LifecycleBackend, error) {
	return nil, fmt.Errorf("no event backend is available for this platform; expected poll backend")
}

func NewNamedEventBackend(config Config) (LifecycleBackend, error) {
	return nil, fmt.Errorf("event backend %q is not available for this platform; expected poll backend", config.Backend)
}
