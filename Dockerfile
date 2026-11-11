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
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/panel /usr/local/bin/panel
# The data dir defaults to /var/lib/<slug> (internal/brand); the installer mounts it.
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/panel"]
CMD ["serve"]
