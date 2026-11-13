# Development image for the Go panel. `docker compose watch` syncs source edits into
# /src and restarts the container, which recompiles with `go run` from a warm cache.
FROM golang:1.26-alpine

ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
# git fetches Git apps (ssh for deploy keys); the docker CLI with buildx builds them on the mounted daemon.
RUN apk add --no-cache git openssh-client docker-cli docker-cli-buildx
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
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/
# The web app is served by Vite in development; the embed only needs the directory to exist.
RUN mkdir -p internal/webui/dist && touch internal/webui/dist/.gitkeep \
    && go build -o /dev/null ./cmd/panel   # fills the build cache so restarts compile fast

CMD ["go", "run", "./cmd/panel", "serve"]
