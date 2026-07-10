#!/bin/sh
set -eu
go run -buildvcs=true . "$@"
