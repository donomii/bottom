//go:build darwin

package main

import "fmt"

func NewPlatformEventBackend(config Config) (LifecycleBackend, error) {
	return newMacOSEndpointBackend(config)
}

func NewNamedEventBackend(config Config) (LifecycleBackend, error) {
	switch config.Backend {
	case BackendMacOSEndpoint:
		return newMacOSEndpointBackend(config)
	case BackendLinuxProcConnector:
		return nil, fmt.Errorf("linux-proc-connector backend is only available in Linux builds; this build target is macOS")
	case BackendWindowsETW:
		return nil, fmt.Errorf("windows-etw backend is only available in Windows builds; this build target is macOS")
	default:
		return nil, fmt.Errorf("unknown event backend %q", config.Backend)
	}
}
