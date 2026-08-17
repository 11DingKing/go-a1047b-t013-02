# Build stage — compiles a static binary for amd64 and arm64.
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/arcticexpress ./cmd/server

# Runtime stage — minimal image with only the binary and config.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/arcticexpress /app/arcticexpress
COPY config.json /app/config.json
RUN mkdir -p /app/data
ENV CONFIG_PATH=/app/config.json
EXPOSE 58594
ENTRYPOINT ["/app/arcticexpress"]
