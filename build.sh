#!/bin/sh
# Builds the page into dist/: manager.wasm, compute.wasm and the Go wasm runtime (wasm_exec.js) first, then the esbuild bundle.
set -e
cd "$(dirname "$0")"
rm -rf dist
mkdir dist
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o dist/manager.wasm ./cmd/manager
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o dist/compute.wasm ./cmd/compute
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" dist/wasm_exec.js
go run ./cmd/build
