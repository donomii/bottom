#!/bin/sh
set -eu
go run . trace -parent-exe -ppid -- go version
