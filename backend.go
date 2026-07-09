package main

import (
	"context"
	"fmt"
)

type LifecycleBackend interface {
	Name() string
	Watch(context.Context, chan<- Event) error
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
	case BackendLinuxProcConnector, BackendLinuxEBPF, BackendWindowsWMI, BackendMacOSEndpointSecurity:
		backend, err := NewNamedEventBackend(config)
		if err != nil {
			return nil, false, err
		}
		return backend, false, nil
	default:
		return nil, false, fmt.Errorf("backend must be auto, poll, linux-proc-connector, linux-ebpf, windows-wmi, or macos-endpoint-security, received %q", config.Backend)
	}
}
