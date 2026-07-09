//go:build windows

package main

import "fmt"

func NewPlatformEventBackend(config Config) (LifecycleBackend, error) {
	return nil, fmt.Errorf("windows-wmi event subscription is not bundled in this build; expected poll backend")
}

func NewNamedEventBackend(config Config) (LifecycleBackend, error) {
	switch config.Backend {
	case BackendWindowsWMI:
		return nil, fmt.Errorf("windows-wmi event subscription is not bundled in this build; expected poll backend")
	case BackendLinuxProcConnector:
		return nil, fmt.Errorf("linux-proc-connector backend is only available in Linux builds; this build target is Windows")
	case BackendLinuxEBPF:
		return nil, fmt.Errorf("linux-ebpf backend is only available in Linux builds; this build target is Windows")
	case BackendMacOSEndpointSecurity:
		return nil, fmt.Errorf("macos-endpoint-security backend is only available in macOS builds; this build target is Windows")
	default:
		return nil, fmt.Errorf("unknown event backend %q", config.Backend)
	}
}
