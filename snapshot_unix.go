//go:build !linux && !windows && !darwin

package main

import (
	"fmt"
	"os/exec"
	"time"
)

func ReadProcessSnapshot() (ProcessSnapshot, error) {
	command := exec.Command("ps", "-axo", "pid=,ppid=,user=,command=")
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("run ps for process snapshot: expected process table output, command failed with %w after returning %d bytes", err, len(output))
	}
	return parsePSOutput(output, time.Now(), command.Process.Pid), nil
}
