#!/bin/sh
set -eu
if [ "$(go env GOOS)" = darwin ]; then
    go build -tags endpointsecurity -o bottom .
else
    go build -o bottom .
fi
