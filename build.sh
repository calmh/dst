#!/bin/sh
set -euo pipefail
set -x

go test -race -timeout 30s -short -v
go install ./cmd/...
# -coverprofile cover.prof -tags debug

