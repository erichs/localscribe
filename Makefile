GO ?= go

.PHONY: build test

build:
	$(GO) build ./cmd/localscribe

test:
	$(GO) test ./...
