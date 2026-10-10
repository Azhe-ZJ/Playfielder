GO ?= go

.PHONY: build run test browser-test wasm notices clean

build:
	$(GO) build -trimpath -o bin/playfielder ./cmd/playfielder

run:
	$(GO) run ./cmd/playfielder --addr 127.0.0.1:4177 --data ./data

test:
	$(GO) test -race ./...
	$(GO) vet ./...

browser-test:
	PLAYFIELDER_BROWSER_TEST=1 $(GO) test -timeout 120s -v ./internal/browser ./internal/httpapi

wasm:
	GOOS=js GOARCH=wasm $(GO) build -trimpath -ldflags='-s -w' -o internal/document/story.wasm ./cmd/storywasm
	cp "$$($(GO) env GOROOT)/lib/wasm/wasm_exec.js" internal/document/wasm_exec.js
	cp "$$($(GO) env GOROOT)/LICENSE" internal/legal/GO_LICENSE
	chmod 644 internal/document/story.wasm

notices:
	cp LICENSE NOTICE internal/legal/

clean:
	rm -rf bin
