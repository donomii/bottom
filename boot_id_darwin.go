//go:build darwin

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func readBootID() string {
	bootTime, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return ""
	}
	return fmt.Sprintf("darwin-%d-%d", bootTime.Sec, bootTime.Usec)
}
