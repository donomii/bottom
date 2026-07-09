#!/bin/sh
set -eu
go run . -test
printf '\nDemo command:\n'
printf './bottom -format jsonl -include compiler\n'
