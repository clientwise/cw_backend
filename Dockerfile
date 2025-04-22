# Build stage
FROM golang:1.24.2 AS build

# Set the working directory inside the container
WORKDIR /app

# Copy the Go module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire project source code
COPY . .

RUN go build -o app .

# Expose the port on which the Go application listens (replace with your app's port)
EXPOSE 8080

# Set the command to run your Go application (replace with your app's entry point)
CMD ["./app"]