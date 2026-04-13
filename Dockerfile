# Multi-stage build for Hush Go API with embedded admin dashboard

# --- Stage 1: Build admin SPA ---
FROM node:22-alpine AS admin-builder
WORKDIR /admin
COPY admin/package.json admin/package-lock.json ./
RUN npm ci
COPY admin/ .
RUN npm run build

# --- Stage 2: Build Go binary ---
FROM golang:1.25-alpine AS builder
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Replace dev placeholder with real admin build output
COPY --from=admin-builder /admin/dist ./admin/dist
RUN CGO_ENABLED=0 go build -o /hush ./cmd/hush

# --- Stage 3: Runtime ---
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /hush .
COPY --from=builder /build/migrations ./migrations
EXPOSE 8080
ENTRYPOINT ["./hush"]
