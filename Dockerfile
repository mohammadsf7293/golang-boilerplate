# Use the official Golang image to create a build artifact.
FROM golang:1.22 AS builder

# Create and change to the app directory.
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed.
RUN go mod download

# Copy the source from the current directory to the working directory inside the container.
COPY . .

# Build the Go app
RUN make build

# Use a leaner image for running the application
FROM alpine:latest

# Install ca-certificates to allow communication with https endpoints
RUN apk --no-cache add ca-certificates

# Copy the binary from the builder stage
COPY --from=builder /app/bin /bin/