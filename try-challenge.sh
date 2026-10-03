#!/bin/sh
# Practice/test the unlock typing challenge without touching any block.
# Needs no root. Usage: ./try-challenge.sh [length]   (default 300 characters)
cd "$(dirname "$0")" || exit 1
export PATH="/usr/local/go/bin:$PATH"
CGO_ENABLED=0 go build -o try-challenge ./cmd/try-challenge || exit 1
exec ./try-challenge "$@"
