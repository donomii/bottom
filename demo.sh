#!/bin/sh
set -eu
go run . trace -ppid -- go version
