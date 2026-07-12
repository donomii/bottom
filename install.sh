#!/bin/sh
set -eu
if [ "$(go env GOOS)" = darwin ]; then
    go install -tags endpointsecurity .
else
    go install .
fi
