.PHONY: build test vet demo
build:
	go build -o bin/jevguard ./cmd/jevguard
test:
	go test -race ./...
vet:
	go vet ./...
demo:
	go run ./cmd/jevguard demo
