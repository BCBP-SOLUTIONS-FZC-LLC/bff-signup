# syntax=docker/dockerfile:1

# ── Build stage ───────────────────────────────────────────────────────────────
# golang:1.26.10-alpine — matches go.mod toolchain (go 1.26.10).
# Digest-pinned: run `make pin-base-images` to refresh.
FROM golang:1.26.10-alpine@sha256:9ce231b28998b32e116927c61aa5c406183061d4f3f2d61b292c88e7f6bdf7ca AS builder

WORKDIR /src

# git is not included in the golang:alpine image but is required for private module auth
RUN apk add --no-cache git

# Cache module downloads separately from source
COPY go.mod go.sum ./
RUN --mount=type=secret,id=go_private_token \
    --mount=type=cache,target=/go/pkg/mod \
    GOPRIVATE="github.com/BCBP-SOLUTIONS-FZC-LLC/*" \
    GONOSUMDB="github.com/BCBP-SOLUTIONS-FZC-LLC/*" \
    git config --global credential.helper store && \
    echo "https://x-access-token:$(cat /run/secrets/go_private_token)@github.com" > ~/.git-credentials && \
    chmod 600 ~/.git-credentials && \
    go mod download

COPY . .

RUN --mount=type=secret,id=go_private_token \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOPRIVATE="github.com/BCBP-SOLUTIONS-FZC-LLC/*" \
    GONOSUMDB="github.com/BCBP-SOLUTIONS-FZC-LLC/*" \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.buildVersion=${BUILD_VERSION:-dev}" \
      -o /bin/server \
      ./cmd/server

# ── Runtime stage ─────────────────────────────────────────────────────────────
# gcr.io/distroless/static-debian12:nonroot — no shell, no package manager,
# runs as uid 65532 (nonroot). Digest-pinned: run `make pin-base-images` to refresh.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=builder /bin/server /server

EXPOSE 8080 9090

ENTRYPOINT ["/server"]
