FROM golang:1.25-alpine AS builder

ENV CGO_ENABLED=0

WORKDIR /workspace

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN go build -trimpath -ldflags "-s -w" -o /sealos-state-metrics .

# Final image with LVM tools
FROM alpine:3.18.4

# Install LVM tools and dependencies
RUN apk add --no-cache \
    lvm2 \
    lvm2-extra \
    util-linux \
    device-mapper \
    btrfs-progs \
    xfsprogs \
    xfsprogs-extra \
    e2fsprogs \
    e2fsprogs-extra \
    ca-certificates \
    libc6-compat

WORKDIR /

# Copy binary from builder
COPY --from=builder /sealos-state-metrics /sealos-state-metrics

# Expose metrics port
EXPOSE 9090

# Run as root for LVM operations
USER 0

ENTRYPOINT ["/sealos-state-metrics"]
