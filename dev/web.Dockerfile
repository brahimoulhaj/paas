# Development image for the web app: Vite with hot module replacement. `docker compose
# watch` syncs edits into /app/web and Vite picks them up at once.
FROM node:22-alpine

ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
WORKDIR /app/web

COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./

CMD ["pnpm", "exec", "vite", "--host", "0.0.0.0", "--port", "5173", "--strictPort"]
