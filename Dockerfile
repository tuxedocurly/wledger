# --- Stage 1: The Builder ---
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the application
# UPDATED: Point to the new entrypoint in ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-w -s" -o /app/server ./cmd/server

# --- Stage 2: The Final Image ---
FROM alpine:latest

WORKDIR /app

# Create the data directory
RUN mkdir -p /app/data/uploads

# Copy the binary
COPY --from=builder /app/server .

# UPDATED: Copy the 'ui' folder (templates & static)
COPY --from=builder /app/ui ./ui

# Expose the port
EXPOSE 3000

# Run the server
CMD ["./server"]