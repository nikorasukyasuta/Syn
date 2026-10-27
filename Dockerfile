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

# Pre-create writable runtime directories with open permissions
RUN mkdir -p /build/logs /build/assets/panels /build/config && \
    chmod -R 0777 /build/logs /build/assets/panels /build/config

# Stage 2: Enterprise Distroless Runtime
FROM gcr.io/distroless/static-debian12:latest

WORKDIR /app

# Copy timezone data from builder stage
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy pre-configured runtime directories
COPY --from=builder /build/logs /app/logs
COPY --from=builder /build/assets/panels /app/assets/panels
COPY --from=builder /build/config /app/config

# Copy the compiled binary from the builder stage
COPY --from=builder /build/syn /app/syn

EXPOSE 8080

ENTRYPOINT ["/app/syn"]