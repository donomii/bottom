package main

import (
	"context"
	"fmt"
)

type LifecycleBackend interface {
	Name() string
	Watch(context.Context, chan<- Event) error
}

func validBackendName(name string) bool {
	return name == BackendAuto || name == BackendPoll || name == BackendLinuxProcConnector || name == BackendWindowsETW || name == BackendMacOSEndpoint
}

func selectBackend(config Config) (LifecycleBackend, bool, error) {
	switch config.Backend {
	case BackendAuto:
		backend, err := NewPlatformEventBackend(config)
		if err != nil {
			return NewPollingBackend(config.PollInterval), true, err
		}
		return backend, true, nil
	case BackendPoll:
		return NewPollingBackend(config.PollInterval), false, nil
	case BackendLinuxProcConnector:
		backend, err := NewNamedEventBackend(config)
		if err != nil {
			return nil, false, err
		}
		return backend, false, nil
	case BackendWindowsETW, BackendMacOSEndpoint:
		backend, err := NewNamedEventBackend(config)
		if err != nil {
			return nil, false, err
		}
		return backend, false, nil
	default:
		return nil, false, fmt.Errorf("backend must be auto, poll, linux-proc-connector, windows-etw, or macos-endpoint-security, received %q", config.Backend)
	}
}
