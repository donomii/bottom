//go:build windows

package main

import "os"

func notifiedSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
