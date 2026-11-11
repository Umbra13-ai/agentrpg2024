FROM golang:1.27.3-alpine AS builder
# Cache bust: 2026-02-28-09-00-force-copy
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY docs/ ./docs/
COPY game/ ./game/
COPY tools/ ./tools/
COPY *.md ./
COPY *.json ./
COPY .env.example ./
COPY LICENSE ./
RUN ls -la && ls -la cmd/ && ls -la cmd/server/
# the API reference (cmd/server/docs/swagger/swagger.json) is built by tools/build_swagger.py and committed; swag init would overwrite it
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X 'main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" -o server ./cmd/server

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata && adduser -D -H -u 10001 agentrpg
WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/docs ./docs
# the server only reads its files; it runs as an ordinary user, not root
USER agentrpg
EXPOSE 8080
CMD ["./server"]
