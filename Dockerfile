# Multi-stage Dockerfile for Necly Services Backend (Go Fiber)
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go module files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o necly-backend ./cmd/server

# Final stage - minimal image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 1001 -S necly && \
    adduser -u 1001 -S necly -G necly

WORKDIR /app

COPY --from=builder /app/necly-backend .
COPY --from=builder /app/migrations ./migrations

USER necly

EXPOSE 8080

ENV APP_ENV=production
ENV APP_PORT=8080

ENTRYPOINT ["./necly-backend"]