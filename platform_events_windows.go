//go:build windows

package main

import "fmt"

func NewPlatformEventBackend(config Config) (LifecycleBackend, error) {
	return windowsETWBackend{interval: config.PollInterval}, nil
}

func NewNamedEventBackend(config Config) (LifecycleBackend, error) {
	switch config.Backend {
	case BackendWindowsETW:
		return windowsETWBackend{interval: config.PollInterval}, nil
	case BackendLinuxProcConnector:
		return nil, fmt.Errorf("linux-proc-connector backend is only available in Linux builds; this build target is Windows")
	case BackendMacOSEndpoint:
		return nil, fmt.Errorf("macos-endpoint-security backend is only available in macOS builds; this build target is Windows")
	default:
		return nil, fmt.Errorf("unknown event backend %q", config.Backend)
	}
}
