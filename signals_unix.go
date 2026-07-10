//go:build !windows

package main

import (
	"os"
	"syscall"
)

func notifiedSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
