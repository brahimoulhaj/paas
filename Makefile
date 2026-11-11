# The product name lives in internal/brand/brand.go; everything here derives from it.
SLUG    := $(shell sed -n 's/^[[:space:]]*Slug = "\([^"]*\)".*/\1/p' internal/brand/brand.go)
PREFIX  := $(shell echo $(SLUG) | tr a-z A-Z)_
VERSION ?= dev
NAME    := $(shell sed -n 's/^[[:space:]]*Name = "\([^"]*\)".*/\1/p' internal/brand/brand.go)
IMAGE   ?= ghcr.io/brahimoulhaj/paas

.PHONY: help deps web build test dev dev-down dev-import-data dev-api dev-web image installer clean

help:
	@echo "make deps     - download Go modules and pnpm packages"
	@echo "make dev      - run everything in Docker with live reload (compose watch); open :5173"
	@echo "make dev-down - stop the compose dev stack"
	@echo "make dev-import-data - copy ./.data into the compose data volume (replaces what is there)"
	@echo "make dev-api  - run the Go server on :8080 with data in ./.data (proxy on :8880 / :8443); live reload with air if installed"
	@echo "make dev-web  - run the Vite dev server (proxies /api to :8080)"
	@echo "make web      - build the web app into internal/webui/dist"
	@echo "make build    - build bin/$(SLUG) with the web app embedded"
	@echo "make test     - Go tests and web typecheck"
	@echo "make image    - build the Docker image $(IMAGE):$(VERSION)"
	@echo "make installer - write bin/install.sh for $(IMAGE):$(VERSION) (IMAGE=registry/name to change)"

deps:
	go mod tidy
	cd web && pnpm install

web:
	cd web && pnpm install && pnpm run build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/$(SLUG) ./cmd/panel

test:
	go vet ./...
	go test ./...
	cd web && pnpm run typecheck

# Everything in containers, with live reload. The shared network must exist before
# compose starts, because compose.yaml declares it external.
dev:
	docker network inspect $(SLUG) >/dev/null 2>&1 || docker network create $(SLUG)
	docker compose watch

dev-down:
	docker compose down

# One-time move from `make dev-api` (data in ./.data) to the compose stack (data in a
# volume). Stops the API container first so the database is not open while it is copied.
dev-import-data:
	@test -d .data || { echo "no ./.data to import"; exit 1; }
	docker compose stop api
	docker compose run --rm --no-deps -v ./.data:/import:ro api \
		sh -c 'rm -rf /var/lib/$(SLUG)/* && cp -a /import/. /var/lib/$(SLUG)/ && ls -la /var/lib/$(SLUG)'

# Dev keeps the proxy off 80/443 so it does not fight a local web server.
dev-api:
	$(PREFIX)DATA_DIR=./.data $(PREFIX)PUBLIC_URL=http://localhost:5173 \
	$(PREFIX)PROXY_HTTP_PORT=8880 $(PREFIX)PROXY_HTTPS_PORT=8443 \
	$(if $(shell command -v air),air,go run ./cmd/panel serve)

dev-web:
	cd web && pnpm run dev

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

# The installer is a template filled from the brand constants and the release image.
installer:
	@mkdir -p bin
	sed -e 's|@@NAME@@|$(NAME)|g' -e 's|@@SLUG@@|$(SLUG)|g' -e 's|@@PREFIX@@|$(PREFIX)|g' \
	    -e 's|@@DATA_DIR@@|/var/lib/$(SLUG)|g' -e 's|@@IMAGE@@|$(IMAGE)|g' -e 's|@@VERSION@@|$(VERSION)|g' \
	    install/install.sh.tmpl > bin/install.sh
	chmod 755 bin/install.sh
	@! grep -n '@@' bin/install.sh || { echo "unfilled placeholder in bin/install.sh"; exit 1; }

clean:
	rm -rf bin tmp .data
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
