# syntax=docker/dockerfile:1

# The web stage builds the real Vue frontend (the equivalent of the
# Makefile's web-build target: npm ci && npm run build) on the build
# platform — no cross-compiling needed for JS. Without this stage, a
# plain `go build` after `COPY . .` would embed whatever happened to
# already be on disk at build time: the committed "UI not built"
# placeholder on a fresh clone, or stale local build output on a dev
# machine (final whole-branch review finding; ruling, spec §11.4). This
# stage guarantees the final image always embeds a freshly-built UI
# regardless of host state.
FROM --platform=$BUILDPLATFORM node:22-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

# The build stage runs on the build platform and cross-compiles, so
# multi-arch images need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# Overwrite the committed placeholder with the real build from the web
# stage, the same way the Makefile's web-build target does for a local
# `make build`, so go:embed picks up the real frontend.
COPY --from=web /src/web/dist internal/webui/dist
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/custos ./cmd/custos

# custos stores everything in git repositories and serves them through
# git http-backend (Alpine package git-daemon), so the runtime image needs git and git http-backend.
FROM alpine:3.22
RUN apk add --no-cache git git-daemon \
 && adduser -D -u 65532 -h /home/custos custos \
 && mkdir -p /data && chown 65532:65532 /data
COPY --from=build /out/custos /usr/local/bin/custos
ENV CUSTOS_DATA_DIR=/data \
    CUSTOS_ADDR=:8080
VOLUME ["/data"]
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/custos"]
CMD ["serve"]
