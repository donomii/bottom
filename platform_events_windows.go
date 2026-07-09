//go:build windows

package main

import "fmt"

func NewPlatformEventBackend(config Config) (LifecycleBackend, error) {
	return NewPollingBackend(config.PollInterval), nil
}

func NewNamedEventBackend(config Config) (LifecycleBackend, error) {
	switch config.Backend {
	case BackendLinuxProcConnector:
		return nil, fmt.Errorf("linux-proc-connector backend is only available in Linux builds; this build target is Windows")
	default:
		return nil, fmt.Errorf("unknown event backend %q", config.Backend)
	}
}
