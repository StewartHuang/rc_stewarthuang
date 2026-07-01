.PHONY: build run test

build:
	go build -o bin/delivery ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./... -v
