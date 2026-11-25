#!/bin/sh
# Serves dist/ at https://localhost:${PORT:-8000}/ (HOST=0.0.0.0 to listen on all addresses)
cd "$(dirname "$0")" || exit 1
exec go run ./cmd/serve -addr "${HOST:-127.0.0.1}:${PORT:-8000}"
