BINARY := custos

.PHONY: all build test docker dev-server clean web-build web-typecheck

all: build

## build: build the binary (builds the frontend first, embedding it)
build: web-build
	go build -o $(BINARY) ./cmd/custos

## web-build: build the frontend and copy it into internal/webui/dist for go:embed
web-build:
	cd web && npm ci && npm run build
	rm -rf internal/webui/dist && cp -r web/dist internal/webui/dist

## test: vet and run all tests, including both SDKs and the frontend type check (needs git, docker, python3 and node/npm on PATH)
test: web-typecheck
	go vet ./...
	go test ./...
	cd sdk/go && go vet ./... && go test ./...
	python3 -m unittest discover -s sdk/python

## web-typecheck: type-check the frontend with vue-tsc (no build output)
web-typecheck:
	cd web && npm ci && npm run type-check

## docker: build the container image custos:dev
docker:
	docker build -t $(BINARY):dev .

## dev-server: build and serve repositories from ./tmp/data on 127.0.0.1:8080
## (run `make build` first so the embedded UI is current; for frontend
## development, run `npm run dev` in web/ against this server instead,
## which proxies /api, /auth and /git and hot-reloads)
dev-server: build
	./$(BINARY) serve --data-dir ./tmp/data

clean:
	rm -f $(BINARY)
