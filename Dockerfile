# Use the official Golang image. Using alpine makes this build stage smaller.
FROM golang:1.25-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the dependency files
COPY go.mod go.sum ./
# Download dependencies. This is cached by Docker.
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application, creating a static binary.
# CGO_ENABLED=0 is CRITICAL: It ensures a pure-Go binary with no C dependencies.
# -w -s strips debug info, making the binary smaller.
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-w -s" -o /app/server .

# Use a minimal 'base image'. alpine is tiny and secure.
FROM alpine:latest

# Set the working directory
WORKDIR /app

# Create the data directory for our database
RUN mkdir /app/data

# Copy the static binary from the 'builder' stage
COPY --from=builder /app/server .

# Copy the templates
COPY --from=builder /app/templates ./templates

# Expose port 3000 to the outside world
EXPOSE 3000

# The command to run when the container starts
CMD ["./server"]