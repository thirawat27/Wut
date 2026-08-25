# Build stage
FROM golang:1.27 AS builder

# Install build dependencies
RUN apk add --no-cache git make ca-certificates

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo \
    -ldflags "-w -s -X main.Version=$(git describe --tags --always) -X main.BuildTime=$(date -u '+%Y-%m-%d_%H:%M:%S') -X main.Commit=$(git rev-parse --short HEAD)" \
    -o wut .

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN addgroup -g 1000 -S wut && \
    adduser -u 1000 -S wut -G wut

# Set working directory
WORKDIR /home/wut

# Copy binary from builder
COPY --from=builder /app/wut /usr/local/bin/wut

# Create necessary directories
RUN mkdir -p /home/wut/.wut/data /home/wut/.wut/models /home/wut/.wut/logs /home/wut/.config/wut && \
    chown -R wut:wut /home/wut

# Switch to non-root user
USER wut

# Set environment variables
ENV WUT_CONFIG=/home/wut/.config/wut/config.yaml \
    WUT_DATA=/home/wut/.wut/data \
    WUT_LOGS=/home/wut/.wut/logs

# Expose metrics port (optional)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wut --help || exit 1

# Run the application
ENTRYPOINT ["wut"]
CMD ["--help"]
