# syntax=docker/dockerfile:1

# The build stage runs on the build platform and cross-compiles, so
# multi-arch images need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
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
