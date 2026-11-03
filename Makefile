# SPDX-License-Identifier: GPL-3.0-only
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

# The javac worker jar is embedded into the binary and must run on every JDK jv supports,
# so it is compiled for Java 17 regardless of the JDK that builds it.
.PHONY: worker-jar
worker-jar:
	rm -rf .worker-build && mkdir .worker-build
	javac --release 17 -d .worker-build compiler/worker/server/Server.java
	jar --create --date=2026-01-01T00:00:00Z --file compiler/worker/server.jar --main-class worker.server.Server -C .worker-build .
	rm -rf .worker-build
