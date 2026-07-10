//go:build windows

package main

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

func readBootID() string {
	bootedAt := time.Now().Add(-windows.DurationSinceBoot()).UTC().Truncate(time.Second)
	return fmt.Sprintf("windows-%d", bootedAt.Unix())
}
