# Multi-stage build for Hush Go API with embedded admin dashboard

# --- Stage 1: Build admin SPA ---
FROM node:22-alpine AS admin-builder
WORKDIR /admin
COPY admin/package.json admin/package-lock.json ./
RUN npm ci
COPY admin/ .
RUN npm run build

# --- Stage 2: Build Go binary ---
# Run the Go toolchain natively on the build host and cross-compile to the
# target platform via TARGETOS/TARGETARCH. Pure-Go binary (CGO_ENABLED=0)
# so no QEMU emulation is needed during the build itself — buildx still
# emulates the runtime stage but the slow part (Go compile) stays native.
# Required for HUSHHQ-82 multi-arch publish without ~20 min arm64 runs.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG BUILD_VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Replace dev placeholder with real admin build output
COPY --from=admin-builder /admin/dist ./admin/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags "-X github.com/hushhq/hush-server/internal/version.ServerVersion=${BUILD_VERSION}" \
    -o /hush ./cmd/hush

# --- Stage 3: Runtime ---
# HUSHHQ-85: bumped from alpine:3.19 to alpine:3.21 to clear CVE-2026-40200
# (musl libc arbitrary-code-execution; fixed in 1.2.4_git20230717-r6 which
# ships on 3.20+). 3.21 picked over 3.20 for the longer maintenance window.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /hush .
COPY --from=builder /build/migrations ./migrations
EXPOSE 8080
ENTRYPOINT ["./hush"]
