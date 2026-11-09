FROM debian:bookworm-slim AS css
ARG TARGETARCH
ARG TAILWIND_VERSION=v3.4.17
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates && rm -rf /var/lib/apt/lists/*
RUN ARCH=$([ "$TARGETARCH" = "arm64" ] && echo arm64 || echo x64) && \
    curl -fsSL -o /usr/local/bin/tailwindcss \
    "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-${ARCH}" && \
    chmod +x /usr/local/bin/tailwindcss
WORKDIR /src
COPY tailwind.config.js ./
COPY internal/web/templates ./internal/web/templates
COPY internal/web/static/input.css ./internal/web/static/input.css
RUN tailwindcss -c tailwind.config.js -i internal/web/static/input.css -o app.css --minify

FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=css /src/app.css internal/web/static/app.css
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /allister ./cmd/allister

FROM gcr.io/distroless/static-debian12
COPY --from=build /allister /allister
ENV PORT=8080 DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/allister"]
