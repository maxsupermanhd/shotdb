#!/bin/sh
# Serves dist/ at http://localhost:${PORT:-8000}/
cd "$(dirname "$0")" || exit 1
exec go run ./cmd/serve -addr "127.0.0.1:${PORT:-8000}"
