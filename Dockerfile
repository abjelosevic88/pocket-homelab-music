# syntax=docker/dockerfile:1.7
# ---------- 1. build the web UI ----------
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 2. build the Go binary ----------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/pocket-core ./third_party/pocket-core
RUN cd third_party/pocket-core && sha256sum -c SHA256SUMS
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pocket-music ./cmd/pocket-music

# ---------- 3. runtime ----------
FROM alpine:3.21
# "From a link" downloads: yt-dlp (python3; our own copy in /data/bin is updated daily), ffmpeg (audio
# remux, square covers), mutagen (tags + embedded pictures), node (YouTube's signature puzzles)
RUN apk add --no-cache ca-certificates tzdata curl ffmpeg python3 py3-mutagen nodejs \
 && addgroup -g 1000 app && adduser -u 1000 -G app -D -h /data app \
 && mkdir -p /usr/local/share/pocket-music \
 && curl -fsSL -o /usr/local/share/pocket-music/yt-dlp https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp \
 && chmod 755 /usr/local/share/pocket-music/yt-dlp
COPY --from=build /out/pocket-music /usr/local/bin/pocket-music
ENV DATA_DIR=/data PORT=8080 HOST=0.0.0.0
VOLUME ["/data"]
EXPOSE 8080
USER app
WORKDIR /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD curl -fsS http://localhost:8080/healthz || exit 1
LABEL org.opencontainers.image.title="Pocket Music" \
      org.opencontainers.image.description="Your music library, played anywhere: a player for the household on top of Navidrome (or any Subsonic server) with listening history and stats, daily mixes and radio, smart playlists, synced lyrics, Lidarr requests and downloads from YouTube links into the library." \
      org.opencontainers.image.source="https://github.com/abjelosevic88/pocket-homelab-music" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["pocket-music"]
