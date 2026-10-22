.PHONY: build test vet race

build:
	mkdir -p bin
	go build -o bin/webdock ./cmd/webdock

test:
	go test ./...

vet:
	go vet ./...

race:
	go test -race ./...
