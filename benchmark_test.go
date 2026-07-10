package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkSnapshotDiffBurst(b *testing.B) {
	for _, processCount := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("processes-%d", processCount), func(b *testing.B) {
			previous, next := benchmarkSnapshots(processCount)
			events := make(chan Event, processCount*2)
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				emitSnapshotDiff(context.Background(), BackendPoll, previous, next, events)
				for eventIndex := 0; eventIndex < processCount; eventIndex++ {
					<-events
				}
			}
		})
	}
}

func BenchmarkChurnHighCardinality(b *testing.B) {
	detector := newChurnDetector(10*time.Second, 5, 10*time.Second, 4096, 0)
	base := time.Unix(1, 0)
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		event := Event{Kind: EventStart, Time: base.Add(time.Duration(iteration) * time.Millisecond), PID: iteration + 1, Exe: fmt.Sprintf("/bin/worker-%d", iteration%8192)}
		detector.Observe(event)
	}
}

func benchmarkSnapshots(processCount int) (ProcessSnapshot, ProcessSnapshot) {
	previous := ProcessSnapshot{}
	next := ProcessSnapshot{}
	now := time.Unix(1, 0)
	for pid := 1; pid <= processCount; pid++ {
		id := fmt.Sprintf("%d:1", pid)
		next[id] = capturedProcess(id, pid, 1, fmt.Sprintf("worker-%d", pid), "/bin/worker", "/work", "1000", now, now)
	}
	return previous, next
}
