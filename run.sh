#!/bin/sh
set -eu
if [ "$(go env GOOS)" = darwin ]; then
    go run -buildvcs=true -tags endpointsecurity . "$@"
else
    go run -buildvcs=true . "$@"
fi
