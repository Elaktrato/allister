# Every image is pinned by digest; the tag in front of the digest is only for
# humans (Docker pulls the digest). Base-image security updates therefore
# need scripts/update-pins.sh (see PINS.md), as do Tailwind upgrades.

# debian:bookworm-slim
FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc AS css
ARG TARGETARCH
ARG TAILWIND_VERSION=v3.4.17
# sha256 of tailwindcss-linux-x64 and tailwindcss-linux-arm64 of TAILWIND_VERSION
ARG TAILWIND_SHA256_AMD64=7d24f7fa191d2193b78cd5f5a42a6093e14409521908529f42d80b11fde1f1d4
ARG TAILWIND_SHA256_ARM64=69b1378b8133192d7d2feb12a116fa12d035594f58db3eff215879e4ad8cf39b
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates && rm -rf /var/lib/apt/lists/*
RUN case "$TARGETARCH" in \
      amd64) ARCH=x64; SUM="${TAILWIND_SHA256_AMD64}" ;; \
      arm64) ARCH=arm64; SUM="${TAILWIND_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH '$TARGETARCH' (amd64 or arm64)" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /usr/local/bin/tailwindcss \
      "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-${ARCH}" && \
    echo "${SUM}  /usr/local/bin/tailwindcss" | sha256sum -c - && \
    chmod +x /usr/local/bin/tailwindcss
WORKDIR /src
COPY tailwind.config.js ./
COPY internal/appearance/tailwind-map.json ./internal/appearance/tailwind-map.json
COPY internal/web/templates ./internal/web/templates
COPY internal/web/static/input.css ./internal/web/static/input.css
RUN tailwindcss -c tailwind.config.js -i internal/web/static/input.css -o app.css --minify

# golang:1.27.3-bookworm (keep in step with go.mod's toolchain and scripts/go.sh)
FROM golang:1.27.3-bookworm@sha256:dc0963f32053e4bc451d24d87e3892d42d01021b61f093e98eb95c0352bb84b0 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=css /src/app.css internal/web/static/app.css
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /allister ./cmd/allister

# gcr.io/distroless/static-debian12:latest
FROM gcr.io/distroless/static-debian12:latest@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2
COPY --from=build /allister /allister
ENV PORT=8080 DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/allister"]
