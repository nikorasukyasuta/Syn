# Stage 1: Build the Go binary
FROM golang:1.25-alpine AS builder

# Install git and SSL root certificates
RUN apk update && apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Cache dependencies before copying source code
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

# Target platform arguments (automatically supplied by Docker BuildKit)
ARG TARGETOS
ARG TARGETARCH

# Compile a statically linked binary matching the target/host architecture
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-} go build \
    -ldflags="-w -s" \
    -o /build/syn ./cmd/bot/main.go

# Pre-create writable runtime directories with open permissions for nonroot (UID 65532)
RUN mkdir -p /build/logs /build/assets/panels && \
    chmod -R 0777 /build/logs /build/assets/panels

# Stage 2: Enterprise Distroless Runtime (Non-Root)
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

# Copy timezone data from builder stage
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy pre-configured runtime directories with nonroot ownership
COPY --from=builder --chown=nonroot:nonroot /build/logs /app/logs
COPY --from=builder --chown=nonroot:nonroot /build/assets/panels /app/assets/panels

# Copy the compiled binary from the builder stage
COPY --from=builder /build/syn /app/syn

# Run as unprivileged nonroot user (UID 65532)
USER nonroot:nonroot

ENTRYPOINT ["/app/syn"]