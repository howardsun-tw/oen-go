#!/bin/sh
set -eu

# Use the selected toolchain without rewriting either checked-in go.mod file.
# A stale inherited GOROOT must not mix another installation with PATH's go.
unset GOROOT
export GOTOOLCHAIN=local
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
consumer_dir=$(mktemp -d "${TMPDIR:-/tmp}/oen-consumer.XXXXXX")
trap 'rm -rf -- "$consumer_dir"' EXIT HUP INT TERM
cp "$repo_root/integration/consumer/go.mod" "$repo_root/integration/consumer/"*.go "$consumer_dir/"
cd "$consumer_dir"

consumer_go_version=$(go env GOVERSION)
go version
go mod edit "-go=${consumer_go_version#go}" "-replace=github.com/howardsun-tw/oen-go=$repo_root"
go test -race "$@" ./...
