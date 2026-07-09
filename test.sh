#!/bin/sh
set -eu
go test ./...
go run . -test
