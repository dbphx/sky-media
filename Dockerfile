FROM golang:1.25-bookworm AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/engine ./cmd/engine

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/engine /usr/local/bin/engine
COPY config/config.yaml /app/config/config.yaml

RUN mkdir -p /data/hls

EXPOSE 1935 8080

ENTRYPOINT ["/usr/local/bin/engine", "-config", "/app/config/config.yaml"]
