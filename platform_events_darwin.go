//go:build darwin

package main

import "fmt"

func NewPlatformEventBackend(config Config) (LifecycleBackend, error) {
	return nil, fmt.Errorf("macOS Endpoint Security requires a signed entitled build; expected poll backend in this unsigned build")
}

func NewNamedEventBackend(config Config) (LifecycleBackend, error) {
	switch config.Backend {
	case BackendMacOSEndpointSecurity:
		return nil, fmt.Errorf("macOS Endpoint Security requires a signed entitled build; expected poll backend in this unsigned build")
	case BackendLinuxProcConnector:
		return nil, fmt.Errorf("linux-proc-connector backend is only available in Linux builds; this build target is macOS")
	case BackendLinuxEBPF:
		return nil, fmt.Errorf("linux-ebpf backend is only available in Linux builds; this build target is macOS")
	case BackendWindowsWMI:
		return nil, fmt.Errorf("windows-wmi backend is only available in Windows builds; this build target is macOS")
	default:
		return nil, fmt.Errorf("unknown event backend %q", config.Backend)
	}
}
