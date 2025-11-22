# The Builder
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-w -s" -o /app/server ./cmd/server

# Final Image
FROM alpine:latest

WORKDIR /app

# Create the data directory
RUN mkdir -p /app/data/uploads

# Copy the binary
COPY --from=builder /app/server .

COPY --from=builder /app/ui ./ui

# Expose the port
EXPOSE 3000

# Run the server
CMD ["./server"]
