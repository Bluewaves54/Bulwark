# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /src

# Copy common module first (needed for replace directive)
COPY common/ ./common/

# Copy pypi-pkguard module
COPY pypi-pkguard/ ./pypi-pkguard/

# Build binary
WORKDIR /src/pypi-pkguard
RUN go build -trimpath -ldflags="-s -w" -o /bin/pypi-pkguard .

# Runtime stage — distroless for minimal attack surface
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/pypi-pkguard /pypi-pkguard
COPY pypi-pkguard/config.yaml /config.yaml

# Run as non-root user (UID 1001 is the distroless nonroot user)
USER 1001

EXPOSE 18000

ENTRYPOINT ["/pypi-pkguard", "-config", "/config.yaml"]
