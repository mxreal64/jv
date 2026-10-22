VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test vet fmt
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o jv .
test:
	go test -race -count=1 ./...
vet:
	go vet ./...
fmt:
	gofmt -l -w .
