#!/bin/sh
set -eu
demo_dir=$(mktemp -d "${TMPDIR:-/tmp}/bottom-demo.XXXXXX")
go run . trace -output "$demo_dir/trace.sqlite" -perfetto "$demo_dir/trace.json" -- go run . -test
go run . report -input "$demo_dir/trace.sqlite"
printf '\nDemo recording: %s\n' "$demo_dir/trace.sqlite"
printf 'Perfetto timeline: %s\n' "$demo_dir/trace.json"
