# ==============================================================================
# PDF Forge - Production Dockerfile
# Multi-stage build for minimal image size
# ==============================================================================

# Stage 1: Build the Go binary
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy go mod files first for better caching
COPY go.mod ./

# Copy source code
COPY . .

# Download dependencies and generate go.sum
RUN go mod tidy && go mod download

# Build with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=$(date +%Y%m%d)" \
    -o pdf-forge \
    ./cmd/server

# ==============================================================================
# Stage 2: Runtime image with Chrome
# ==============================================================================
FROM debian:bookworm-slim

# Labels
LABEL maintainer="Your Name <your.email@example.com>"
LABEL description="High-Performance PDF Conversion Microservice"
LABEL version="2.0.0"

# Environment
ENV DEBIAN_FRONTEND=noninteractive
ENV CHROME_BIN=/usr/bin/chromium
ENV CHROME_PATH=/usr/bin/chromium

# Install dependencies in a single layer
RUN apt-get update && apt-get install -y --no-install-recommends \
    # Chrome/Chromium
    chromium \
    chromium-sandbox \
    # PDF tools
    qpdf \
    ghostscript \
    # Fonts
    fonts-liberation \
    fonts-noto \
    fonts-noto-cjk \
    fonts-noto-color-emoji \
    fonts-dejavu \
    fonts-freefont-ttf \
    # Utils
    dumb-init \
    ca-certificates \
    # Cleanup
    && rm -rf /var/lib/apt/lists/* \
    && rm -rf /var/cache/apt/* \
    && rm -rf /tmp/*

# Create non-root user for security
RUN groupadd -r pdfforge && useradd -r -g pdfforge -G audio,video pdfforge \
    && mkdir -p /home/pdfforge/Downloads \
    && chown -R pdfforge:pdfforge /home/pdfforge

# Copy binary from builder
COPY --from=builder /build/pdf-forge /usr/local/bin/pdf-forge
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Set working directory
WORKDIR /home/pdfforge

# Switch to non-root user
USER pdfforge

# Default configuration
ENV ADDRESS=:8080
ENV MAX_WORKERS=4
ENV LOG_LEVEL=info

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Expose port
EXPOSE 8080

# Use dumb-init to handle signals properly and reap zombie processes
ENTRYPOINT ["/usr/bin/dumb-init", "--"]

# Run the service
CMD ["pdf-forge"]
