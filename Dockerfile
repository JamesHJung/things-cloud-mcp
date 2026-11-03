FROM golang:1.24-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o things-server ./server/
# The one-off sync-database repair tool. Shipped in the image because the
# database lives in the container's /data volume and the host (e.g. a NAS) has
# no Go toolchain:
#   docker exec things-mcp things-rebuild -diagnose -db /data/things.db
RUN CGO_ENABLED=1 go build -o things-rebuild ./cmd/rebuild/

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/things-server /usr/local/bin/things-server
COPY --from=builder /app/things-rebuild /usr/local/bin/things-rebuild

RUN mkdir -p /data

EXPOSE 8080
CMD ["things-server"]
