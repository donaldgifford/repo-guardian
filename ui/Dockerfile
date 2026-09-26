# syntax=docker/dockerfile:1.7
# repo-guardian-ui: the Bun BFF serving the React SPA (DESIGN-0027). One
# process, no nginx: the BFF serves the build, proxies /api and holds the
# session.

# Build the SPA with the full dependency tree. Vite's output is plain
# static files, so this stage runs on the build platform only.
FROM --platform=$BUILDPLATFORM oven/bun:1.3.14 AS build
WORKDIR /src
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY . .
RUN bun run build

# Production dependencies only, installed for the target platform.
FROM oven/bun:1.3.14 AS deps
WORKDIR /src
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile --production

FROM oven/bun:1.3.14-distroless
WORKDIR /app
COPY --from=deps /src/node_modules ./node_modules
COPY --from=build /src/dist ./dist
COPY package.json tsconfig.json ./
COPY server ./server

# The root filesystem is read-only in the chart: Bun must not try to
# write its transpiler cache.
ENV NODE_ENV=production \
    BUN_RUNTIME_TRANSPILER_CACHE_PATH=0

EXPOSE 3000

USER 65532:65532

# As with the Go image: no shell for an in-image probe. Kubernetes probes
# /healthz and /readyz.
HEALTHCHECK NONE

ENTRYPOINT ["/usr/local/bin/bun", "server/main.ts"]
