//go:build linux

package main

import (
	"os"
	"strings"
)

func readBootID() string {
	value, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}
