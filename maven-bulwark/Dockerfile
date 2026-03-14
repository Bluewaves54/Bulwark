# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /src

# Copy common module first (needed for replace directive)
COPY common/ ./common/

# Copy maven-bulwark module
COPY maven-bulwark/ ./maven-bulwark/

# Build binary
WORKDIR /src/maven-bulwark
RUN go build -trimpath -ldflags="-s -w" -o /bin/maven-bulwark .

# Runtime stage — distroless for minimal attack surface
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/maven-bulwark /maven-bulwark
COPY maven-bulwark/config.yaml /config.yaml

# Run as non-root user (UID 1001 is the distroless nonroot user)
USER 1001

EXPOSE 18002

ENTRYPOINT ["/maven-bulwark", "-config", "/config.yaml"]
