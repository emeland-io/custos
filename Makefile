BINARY := custos

.PHONY: all build web go test docker dev-server dev-web clean

all: build

## build: build the web UI and embed it into the binary
build: web go

## web: install frontend dependencies and build the SPA into web/dist
web:
	cd web && npm ci && npm run build

## go: build the binary with whatever web/dist contains
go:
	go build -o $(BINARY) ./cmd/custos

## test: run the Go tests and type-check the frontend
test:
	go vet ./...
	go test ./...
	cd web && npm run typecheck

## docker: build the container image custos:dev
docker:
	docker build -t $(BINARY):dev .

## dev-server: run the API on :8080 with data below ./tmp
dev-server:
	CUSTOS_ROOT_DIR=./tmp/root CUSTOS_WORK_DIR=./tmp/work go run ./cmd/custos

## dev-web: run the Vite dev server, proxying /api to :8080
dev-web:
	cd web && npm run dev

clean:
	rm -f $(BINARY)
	find web/dist -mindepth 1 ! -name .gitkeep -delete
