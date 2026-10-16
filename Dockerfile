# Multi-stage Dockerfile for Necly Services Backend (Go Fiber)
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go module files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build static binaries. necly-seed-inventory disertakan supaya pengisian
# kapasitas awal bisa dijalankan di dalam container yang sama dengan server,
# memakai konfigurasi database dan kunci enkripsi kredensial yang sama.
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o necly-backend ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o necly-seed-inventory ./cmd/seed-inventory

# Final stage - minimal image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -g 1001 -S necly && \
    adduser -u 1001 -S necly -G necly

WORKDIR /app

COPY --from=builder /app/necly-backend .
COPY --from=builder /app/necly-seed-inventory .
COPY --from=builder /app/migrations ./migrations

USER necly

EXPOSE 8080

ENV APP_ENV=production
ENV APP_PORT=8080

ENTRYPOINT ["./necly-backend"]