# syntax=docker/dockerfile:1
FROM golang:latest AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o main ./cmd/

FROM alpine:latest
# ffmpeg strips a reel's soundtrack out of its video before transcription — see
# internal/service/video_transcript.go. Without it that fallback logs and gives up,
# which is degraded, not broken.
RUN apk add --no-cache ffmpeg
WORKDIR /root/
COPY --from=builder /app/main .
EXPOSE 8080
CMD ["./main"]
