#!/bin/sh
# Run Go inside Docker (Go is not installed on the host). Usage: scripts/go.sh test ./...
# The image is the pinned build image of the Dockerfile (golang:1.27.3-bookworm,
# matching go.mod's toolchain line); scripts/update-pins.sh refreshes the digest.
exec docker run --rm -v "$PWD":/src -v allister-gomod:/go/pkg/mod -v allister-gocache:/root/.cache/go-build -w /src -e CGO_ENABLED=0 golang:1.27.3-bookworm@sha256:dc0963f32053e4bc451d24d87e3892d42d01021b61f093e98eb95c0352bb84b0 go "$@"
