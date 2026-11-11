# Development image for the Go panel. `docker compose watch` syncs source edits into
# /src and restarts the container, which recompiles with `go run` from a warm cache.
FROM golang:1.26-alpine

ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/
# The web app is served by Vite in development; the embed only needs the directory to exist.
RUN mkdir -p internal/webui/dist && touch internal/webui/dist/.gitkeep \
    && go build -o /dev/null ./cmd/panel   # fills the build cache so restarts compile fast

CMD ["go", "run", "./cmd/panel", "serve"]
