#!/bin/sh
set -eu
go test -run '^$' -bench 'Benchmark(SnapshotDiffBurst|ChurnHighCardinality)$' -benchmem
