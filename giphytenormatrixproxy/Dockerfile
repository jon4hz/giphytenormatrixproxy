FROM golang:1-alpine AS builder

RUN apk add --no-cache ca-certificates
WORKDIR /build/giphyproxy

# Copy go.mod and go.sum first to leverage cache
COPY go.* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source and build with cache
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /usr/bin/giphyproxy

FROM alpine:latest

RUN apk add --no-cache ca-certificates libwebp

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/bin/giphyproxy /usr/bin/giphyproxy

VOLUME /data
WORKDIR /data
CMD ["/usr/bin/giphyproxy"]
