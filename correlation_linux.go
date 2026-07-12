//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type linuxMemoryPressureMonitor struct {
	root          string
	registrations chan string
	mutex         sync.RWMutex
	states        map[string]memoryPressureState
	registered    map[string]bool
	paths         map[string]string
}

func newSystemCorrelationSource(ctx context.Context, interval time.Duration) systemCorrelationSource {
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	monitor := &linuxMemoryPressureMonitor{
		root: "/sys/fs/cgroup", registrations: make(chan string, 1024),
		states: map[string]memoryPressureState{}, registered: map[string]bool{}, paths: map[string]string{},
	}
	go monitor.run(ctx, interval)
	return systemCorrelationSource{register: monitor.register, memoryPressure: monitor.memoryPressure}
}

func (monitor *linuxMemoryPressureMonitor) register(cgroup string) bool {
	path, ok := linuxMemoryEventsPath(monitor.root, cgroup)
	if !ok {
		return false
	}
	monitor.mutex.Lock()
	if monitor.registered[cgroup] {
		monitor.mutex.Unlock()
		return true
	}
	monitor.registered[cgroup] = true
	monitor.mutex.Unlock()
	select {
	case monitor.registrations <- cgroup + "\x00" + path:
		return true
	default:
		monitor.mutex.Lock()
		delete(monitor.registered, cgroup)
		monitor.mutex.Unlock()
		return false
	}
}

func (monitor *linuxMemoryPressureMonitor) memoryPressure(cgroup string) (memoryPressureState, bool) {
	monitor.mutex.RLock()
	defer monitor.mutex.RUnlock()
	state, ok := monitor.states[cgroup]
	return state, ok && state.Known && !state.LastIncreaseAt.IsZero()
}

func (monitor *linuxMemoryPressureMonitor) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case registration := <-monitor.registrations:
			parts := strings.SplitN(registration, "\x00", 2)
			if len(parts) == 2 {
				monitor.paths[parts[0]] = parts[1]
				monitor.refresh(parts[0], parts[1])
			}
		case <-ticker.C:
			for cgroup, path := range monitor.paths {
				monitor.refresh(cgroup, path)
			}
		}
	}
}

func (monitor *linuxMemoryPressureMonitor) refresh(cgroup string, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	count, ok := parseOOMKillCount(data)
	if !ok {
		return
	}
	monitor.mutex.Lock()
	state := monitor.states[cgroup]
	if state.Known && count > state.OOMKills {
		state.LastIncreaseAt = time.Now()
	}
	state.OOMKills = count
	state.Known = true
	monitor.states[cgroup] = state
	monitor.mutex.Unlock()
}

func linuxMemoryEventsPath(root string, cgroup string) (string, bool) {
	clean := filepath.Clean("/" + strings.TrimPrefix(cgroup, "/"))
	if clean == "/" || strings.HasPrefix(clean, "/../") {
		return "", false
	}
	return filepath.Join(root, strings.TrimPrefix(clean, "/"), "memory.events"), true
}

func parseOOMKillCount(data []byte) (uint64, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "oom_kill" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		return value, err == nil
	}
	return 0, false
}
