# Build stage
FROM golang:1.25 AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source
COPY . .

# Build (TARGETOS and TARGETARCH are set by Docker Buildx)
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o s3router ./cmd/s3router

# Runtime stage
FROM gcr.io/distroless/static:debug AS debug

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/s3router .

EXPOSE 8080 9090

ENTRYPOINT ["/app/s3router"]
CMD ["-config", "/app/config.yaml", "-listen", ":8080", "-admin", ":9090"]

# Runtime stage
FROM gcr.io/distroless/static:nonroot

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/s3router .

# Run as nonroot user
USER nonroot:nonroot

EXPOSE 8080 9090

ENTRYPOINT ["/app/s3router"]
CMD ["-config", "/app/config.yaml", "-listen", ":8080", "-admin", ":9090"]
