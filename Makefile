BINARY := custos

.PHONY: all build test docker dev-server clean

all: build

## build: build the binary
build:
	go build -o $(BINARY) ./cmd/custos

## test: vet and run all tests, including both SDKs (needs git, docker and python3 on PATH)
test:
	go vet ./...
	go test ./...
	cd sdk/go && go vet ./... && go test ./...
	python3 -m unittest discover -s sdk/python

## docker: build the container image custos:dev
docker:
	docker build -t $(BINARY):dev .

## dev-server: build and serve repositories from ./tmp/data on 127.0.0.1:8080
dev-server: build
	./$(BINARY) serve --data-dir ./tmp/data

clean:
	rm -f $(BINARY)
