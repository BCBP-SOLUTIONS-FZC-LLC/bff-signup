# syntax=docker/dockerfile:1

# ── Build stage ───────────────────────────────────────────────────────────────
# golang:1.26.11-alpine — matches go.mod toolchain (go 1.26.11).
# Digest-pinned: run `make pin-base-images` to refresh.
FROM golang:1.26.11-alpine@sha256:ae52b5bc6ed045a9ed26ab03dc19327a802a9cc9aa8b27ed57f5c6b60579b0bb AS builder

WORKDIR /build

# GOPRIVATE/GONOSUMDB set as ENV so they apply to every subsequent RUN step
# (both go mod download and go build) without repeating inline environment.
# hadolint ignore=DL3064
ENV GOPRIVATE="github.com/BCBP-SOLUTIONS-FZC-LLC/*"
# hadolint ignore=DL3064
ENV GONOSUMDB="github.com/BCBP-SOLUTIONS-FZC-LLC/*"

# git is required for private module auth via git config url.insteadOf.
# DL3018 suppressed: builder image is digest-pinned (line above), which pins
# the apk package index and therefore the git version transitively.
# hadolint ignore=DL3018
RUN apk add --no-cache git

ARG BUILD_VERSION=dev
ARG SOURCE_DATE_EPOCH=0

# Download modules before copying source so this layer is cached independently
# of source changes. Private modules are accessed via a git URL rewrite that
# injects the token and is unset immediately after download (no credential file
# left in the image layer).
COPY go.mod go.sum ./
RUN --mount=type=secret,id=go_private_token \
    TOKEN=$(cat /run/secrets/go_private_token 2>/dev/null || true) && \
    if [ -z "$TOKEN" ]; then \
      echo "ERROR: go_private_token secret is missing — pass --secret id=go_private_token,src=<token-file>"; \
      exit 1; \
    fi && \
    git config --global url."https://x-access-token:${TOKEN}@github.com/".insteadOf "https://github.com/" && \
    go mod download && \
    git config --global --unset url."https://x-access-token:${TOKEN}@github.com/".insteadOf

COPY --link . .

RUN CGO_ENABLED=0 GOOS=linux \
    SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH} \
    go build -trimpath \
      -ldflags="-s -w -X main.buildVersion=${BUILD_VERSION}" \
      -o bin/server \
      ./cmd/server

# ── Certs stage ───────────────────────────────────────────────────────────────
# Debian bookworm's current CA bundle for the runtime image. The pinned
# distroless static-debian12 image ships ca-certificates 20250419~deb12u1;
# deb12u2 (DLA-4845-1) removes two distrusted roots — Comodo "AAA Certificate
# Services" and "emSign ECC Root CA - G3" — that Go binaries consult for every
# outbound TLS connection (Turnstile, RP-1, I-1, Catalog). The Alpine builder's
# bundle is no substitute: it still carries the emSign root.
# Drop this stage, the COPY below and the .trivyignore entry once distroless
# ships deb12u2 (run `make pin-base-images` to refresh and check).
# Mirrors iam-org-membership/Dockerfile.
FROM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8 AS certs
# hadolint ignore=DL3008
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# ── Runtime stage ─────────────────────────────────────────────────────────────
# gcr.io/distroless/static-debian12:nonroot — no shell, no package manager,
# runs as uid 65532 (nonroot). Digest-pinned: run `make pin-base-images` to refresh.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

ARG BUILD_VERSION=dev
ENV BUILD_VERSION=${BUILD_VERSION}

LABEL org.opencontainers.image.title="bff-signup" \
      org.opencontainers.image.source="https://github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup" \
      org.opencontainers.image.vendor="BCBP Solutions FZC LLC" \
      org.opencontainers.image.revision="${BUILD_VERSION}"

COPY --from=builder /build/bin/server /server
# Patched CA bundle (deb12u2) over distroless's deb12u1 — see the certs stage.
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

EXPOSE 8080 9090

ENTRYPOINT ["/server"]
