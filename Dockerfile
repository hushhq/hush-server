# Multi-stage build for Hush Go API (Phase A+)
# Build from repo root: docker build -f server/Dockerfile server/

FROM golang:1.22-alpine AS builder
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /hush ./cmd/hush

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /hush .
EXPOSE 8080
ENTRYPOINT ["./hush"]
