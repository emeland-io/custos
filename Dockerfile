# syntax=docker/dockerfile:1

# Build stages run on the build platform and cross-compile, so multi-arch
# images need no emulation.

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/custos ./cmd/custos
RUN mkdir -p /out/data/root /out/data/work

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/custos /usr/local/bin/custos
COPY --from=build --chown=65532:65532 /out/data /data
ENV CUSTOS_ROOT_DIR=/data/root \
    CUSTOS_WORK_DIR=/data/work \
    CUSTOS_ADDR=:8080
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/custos"]
