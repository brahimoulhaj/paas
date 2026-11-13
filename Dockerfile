# syntax=docker/dockerfile:1

# The web app and the Go binary are built on the build machine's own platform and the
# binary is cross-compiled, so a multi-arch build (amd64 + arm64) needs no emulation
# except for the tiny runtime stage.

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY web/ ./
# vite writes to ../internal/webui/dist
RUN pnpm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/internal/webui/dist internal/webui/dist
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/panel ./cmd/panel

FROM alpine:3.22
# git fetches Git apps (ssh for deploy keys); the docker CLI with buildx builds them on the mounted daemon.
RUN apk add --no-cache ca-certificates tzdata git openssh-client docker-cli docker-cli-buildx
# Railpack builds Git apps that have no Dockerfile. Pinned and checksummed; keep the
# version equal to builder.RailpackVersion (its BuildKit frontend must match).
ARG RAILPACK_VERSION=0.43.0
ARG TARGETARCH
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) arch=x86_64; sum=248a8f60f36854aabed6124113631078a802de3461660855fc4de9c26065c1fb ;; \
      arm64) arch=arm64;  sum=732ece14eb4f4676a43ebba19431f954edf7b130acb76159ef0f2ff86ac42ac9 ;; \
      *) echo "no railpack for $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    file="railpack-v${RAILPACK_VERSION}-${arch}-unknown-linux-musl.tar.gz"; \
    wget -q -O "/tmp/$file" "https://github.com/railwayapp/railpack/releases/download/v${RAILPACK_VERSION}/$file"; \
    echo "$sum  /tmp/$file" | sha256sum -c -; \
    tar -xzf "/tmp/$file" -C /usr/local/bin railpack; \
    rm "/tmp/$file"; \
    railpack --version
COPY --from=build /out/panel /usr/local/bin/panel
# The data dir defaults to /var/lib/<slug> (internal/brand); the installer mounts it.
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/panel"]
CMD ["serve"]
