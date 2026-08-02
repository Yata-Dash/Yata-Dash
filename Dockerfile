# Multi-arch note: the frontend and Go compile always run on the BUILD machine's
# native architecture (fast — no emulation), and the Go build cross-compiles to
# the TARGET arch. Only the tiny runtime stage is pulled per-arch. This lets one
# amd64 CI runner produce a linux/amd64 + linux/arm64 image quickly.

# ── Stage 1: frontend (arch-independent) ─────────────────────────────────────
FROM --platform=$BUILDPLATFORM node:20-alpine AS web
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
# `npm ci`, not `npm install`: it installs exactly what the lockfile pins into a
# clean tree. `npm install` can resolve differently from the lockfile, so the
# image could ship dependency versions nobody tested — and after a major bump it
# is what leaves a half-updated tree (esbuild's JS wrapper and its platform
# binary on different versions, which refuses to start).
# The lockfile is required rather than optional for the same reason.
RUN npm ci
COPY web/ ./
COPY static/ /build/static/
RUN npm run build

# ── Stage 2: backend (cross-compiled to $TARGETOS/$TARGETARCH) ───────────────
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend
WORKDIR /build
ARG TARGETOS TARGETARCH
# The commit this image was built from, shown in Settings -> Updates. It has to
# be passed in: the build context has no .git directory, so the Go toolchain's
# own VCS stamping (which covers a plain `go build`) finds nothing here.
# Unset is fine — the field is simply omitted.
ARG YATA_COMMIT=""
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
# modernc.org/sqlite is pure Go — no CGO needed, so cross-compiling is trivial.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w \
      -X github.com/Yata-Dash/Yata-Dash/internal/version.commit=${YATA_COMMIT} \
      -X github.com/Yata-Dash/Yata-Dash/internal/version.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o yata ./cmd/yata

# ── Stage 3: runtime ─────────────────────────────────────────────────────────
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

COPY --from=backend /build/yata /app/yata
COPY --from=web /build/static /app/static
COPY templates/ /app/templates/
COPY defs/ /app/defs/
COPY test_data.json /app/test_data.json

# /data holds config.json + the SQLite database (mount a volume here).
# defs/ and static/themes/ can also be mounted to customise without rebuilds.
ENV YATA_CONFIG=/data/config.json \
    YATA_DATA=/data/yata.db \
    YATA_DEFS=/app/defs \
    YATA_BASE=/app \
    YATA_PORT=8420
VOLUME ["/data"]
EXPOSE 8420

ENTRYPOINT ["/app/yata"]
