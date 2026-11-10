#!/bin/sh
# Run Go inside Docker (Go is not installed on the host). Usage: scripts/go.sh test ./...
# The image is the pinned build image of the Dockerfile (golang:1.23.12-bookworm,
# matching go.mod's toolchain line); scripts/update-pins.sh refreshes the digest.
exec docker run --rm -v "$PWD":/src -v allister-gomod:/go/pkg/mod -v allister-gocache:/root/.cache/go-build -w /src -e CGO_ENABLED=0 golang:1.23.12-bookworm@sha256:167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db go "$@"
