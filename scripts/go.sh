#!/bin/sh
# Run Go inside Docker (Go is not installed on the host). Usage: scripts/go.sh test ./...
# The image is the pinned build image of the Dockerfile (golang:1.27.4-bookworm,
# matching go.mod's toolchain line); scripts/update-pins.sh refreshes the digest.
exec docker run --rm -v "$PWD":/src -v allister-gomod:/go/pkg/mod -v allister-gocache:/root/.cache/go-build -w /src -e CGO_ENABLED=0 golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea go "$@"
