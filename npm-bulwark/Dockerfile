# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /src

# Copy common module first (needed for replace directive)
COPY common/ ./common/

# Copy npm-bulwark module
COPY npm-bulwark/ ./npm-bulwark/

# Build binary
WORKDIR /src/npm-bulwark
RUN go build -trimpath -ldflags="-s -w" -o /bin/npm-bulwark .

# Runtime stage — distroless for minimal attack surface
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/npm-bulwark /npm-bulwark
COPY npm-bulwark/config.yaml /config.yaml

# Run as non-root user (UID 1001 is the distroless nonroot user)
USER 1001

EXPOSE 18001

ENTRYPOINT ["/npm-bulwark", "-config", "/config.yaml"]
