//go:build darwin && (!cgo || !endpointsecurity)

package main

import "fmt"

func newMacOSEndpointBackend(config Config) (LifecycleBackend, error) {
	return nil, fmt.Errorf("macOS Endpoint Security support is unavailable in this build; expected a build made with CGO_ENABLED=1 and the endpointsecurity build tag")
}
