#!/bin/sh
set -eu
demo_dir=$(mktemp -d "${TMPDIR:-/tmp}/bottom-demo.XXXXXX")
go run . trace -output "$demo_dir/trace.jsonl" -perfetto "$demo_dir/trace.json" -- go run . -test
go run . report -input "$demo_dir/trace.jsonl"
printf '\nDemo recording: %s\n' "$demo_dir/trace.jsonl"
printf 'Perfetto timeline: %s\n' "$demo_dir/trace.json"
