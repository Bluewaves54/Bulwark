# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /src

# Copy common module first (needed for replace directive)
COPY common/ ./common/

# Copy npm-pkguard module
COPY npm-pkguard/ ./npm-pkguard/

# Build binary
WORKDIR /src/npm-pkguard
RUN go build -trimpath -ldflags="-s -w" -o /bin/npm-pkguard .

# Runtime stage — distroless for minimal attack surface
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/npm-pkguard /npm-pkguard
COPY npm-pkguard/config.yaml /config.yaml

# Run as non-root user (UID 1001 is the distroless nonroot user)
USER 1001

EXPOSE 18001

ENTRYPOINT ["/npm-pkguard", "-config", "/config.yaml"]
