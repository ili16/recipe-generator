# syntax=docker/dockerfile:1
FROM golang:latest AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o main ./cmd/ && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o backfill-aliases ./cmd/backfill-aliases && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o goose github.com/pressly/goose/v3/cmd/goose

FROM alpine:latest
# ffmpeg strips a reel's soundtrack out of its video before transcription — see
# internal/service/video_transcript.go. Without it that fallback logs and gives up,
# which is degraded, not broken.
RUN apk add --no-cache ffmpeg
WORKDIR /root/
COPY --from=builder /app/main .
# Live (live/docker-compose.yml) has no source checkout, so it migrates and backfills
# from the image itself: `./goose -dir migrations ...`, `./backfill-aliases`.
COPY --from=builder /app/backfill-aliases /app/goose ./
COPY db/migrations ./migrations
EXPOSE 8080
CMD ["./main"]
