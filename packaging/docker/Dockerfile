# syntax=docker/dockerfile:1.7
#
# ventd — fan control daemon, containerised.
#
# Multi-stage build. The production image is alpine:3.20 (musl libc),
# which means the Go binary is built with `-tags nonvidia` — NVIDIA
# GPU temperature monitoring via NVML is not available in the Docker
# image. This matches the musl variant in `.goreleaser.yml`. Use the
# native install (install.sh, .deb, .rpm, or AUR) on a glibc host if
# you need NVML.
#
# Runtime requirements (documented in packaging/docker/README.md):
#   - /sys must be bind-mounted read-write for PWM writes to work.
#   - The host needs `deploy/90-ventd-hwmon.rules` installed so the
#     in-container ventd user can write pwm<N>/pwm<N>_enable.
#   - network_mode: host (or equivalent) is required for uevent hot-
#     plug detection; without it, ventd falls back to a 5-minute
#     periodic rescan.

# ─── Builder ────────────────────────────────────────────────────────
# Pin to alpine-based golang so CGO_ENABLED=0 + -tags nonvidia produces
# a static musl binary compatible with the alpine runtime stage.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /src

# ca-certificates + git cover go mod download over HTTPS.
RUN apk add --no-cache ca-certificates git

# Cache go.mod/go.sum separately so source edits don't invalidate the
# module download layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# -tags nonvidia: drops the purego NVML loader that would dlopen
#   libnvidia-ml.so.1 against glibc on an alpine/musl host.
# -trimpath / -ldflags: mirror .goreleaser.yml.
ENV CGO_ENABLED=0 GOOS=linux
RUN GOARCH=${TARGETARCH} go build \
      -tags nonvidia \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
      -o /out/ventd \
      ./cmd/ventd

# ─── Runtime ────────────────────────────────────────────────────────
FROM alpine:3.20

LABEL org.opencontainers.image.title="ventd"
LABEL org.opencontainers.image.description="Fan control daemon for Linux with a zero-terminal web UI."
LABEL org.opencontainers.image.source="https://github.com/ventd/ventd"
LABEL org.opencontainers.image.licenses="GPL-3.0-or-later"
LABEL org.opencontainers.image.url="https://github.com/ventd/ventd"
LABEL org.opencontainers.image.documentation="https://github.com/ventd/ventd/blob/main/packaging/docker/README.md"

# ca-certificates: outbound HTTPS (release check, future features).
# tzdata: timestamps in the web UI / journal.
# wget: used by HEALTHCHECK below (busybox wget is in base).
#
# The ventd group GID is fixed at 472 so host-side installs can align
# the udev-rule group (deploy/90-ventd-hwmon.rules) with the in-
# container user. See packaging/docker/README.md § "Host prerequisites"
# for the mapping runbook.
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 472 ventd \
 && adduser -S -D -H -u 472 -G ventd -s /sbin/nologin ventd \
 && mkdir -p /etc/ventd \
 && chown ventd:ventd /etc/ventd

COPY --from=builder /out/ventd /usr/local/bin/ventd

USER ventd:ventd
EXPOSE 9999
VOLUME ["/etc/ventd"]

# Docker does not speak systemd's sd_notify / WATCHDOG_USEC protocol,
# so the in-binary Type=notify + WatchdogSec=2s loop from
# deploy/ventd.service is not reachable here. The container runtime's
# restart policy + this HEALTHCHECK are the equivalent loop:
# /api/ping is unauthenticated (see internal/web/server.go:274) and
# returns 200 once the HTTP server is listening.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:9999/api/ping >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/usr/local/bin/ventd", "-config", "/etc/ventd/config.yaml"]
