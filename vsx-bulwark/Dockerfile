# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /src

# Copy common module first (needed for replace directive)
COPY common/ ./common/

# Copy vsx-bulwark module
COPY vsx-bulwark/ ./vsx-bulwark/

# Build binary
WORKDIR /src/vsx-bulwark
RUN go build -trimpath -ldflags="-s -w" -o /bin/vsx-bulwark .

# Runtime stage — distroless for minimal attack surface
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/vsx-bulwark /vsx-bulwark
COPY vsx-bulwark/config.yaml /config.yaml

# Run as non-root user (UID 1001 is the distroless nonroot user)
USER 1001

EXPOSE 18003

ENTRYPOINT ["/vsx-bulwark", "-config", "/config.yaml"]
