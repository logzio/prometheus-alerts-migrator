# Golang base image
FROM golang:1.24-alpine AS build

# Set working directory
WORKDIR /app

# Copy controller code to container
COPY . .

# Build the Go binary
RUN go build -o main .

# Create a new image from scratch
FROM alpine:3.14

# Copy the Go binary from the previous stage
COPY --from=build /app/main /app/main


# Start the controller
CMD ["/app/main"]