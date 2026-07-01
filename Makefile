.PHONY: build run test e2e-test

build:
	go build -o bin/delivery ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./... -v

e2e-test:
	scripts/e2e_test.sh
