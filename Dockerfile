# Build stage
FROM golang:1.22-alpine AS build

WORKDIR /src

# Cache deps
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -extldflags=-static -X main.version=${IMAGE_VERSION:-dev}" -o /out/github-copilot-svcs .

# Runtime stage
FROM alpine:3.20 AS runtime

RUN adduser -D -u 10001 appuser \
    && apk add --no-cache ca-certificates curl

WORKDIR /app

# Copy binary
COPY --from=build /out/github-copilot-svcs /app/github-copilot-svcs

USER appuser

EXPOSE 7071

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD curl -fsSL http://localhost:7071/health || exit 1

ENTRYPOINT ["/app/github-copilot-svcs"]
CMD ["run"]
