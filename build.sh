#!/bin/sh
# Builds the page into dist/: shotdb.wasm and the Go wasm runtime (wasm_exec.js) first, then the esbuild bundle.
set -e
cd "$(dirname "$0")"
rm -rf dist
mkdir dist
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o dist/shotdb.wasm ./cmd/wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" dist/wasm_exec.js
go run ./cmd/build
