#!/bin/sh
set -eu
go test -run '^$' -bench 'BenchmarkSnapshotDiffBurst$' -benchmem
