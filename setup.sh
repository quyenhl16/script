#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="$PROJECT_DIR/bin/syssetup"

if [[ ! -x "$BINARY" ]]; then
  if ! command -v go >/dev/null 2>&1; then
    echo "syssetup binary is missing and Go is not installed." >&2
    echo "Copy a release binary to $BINARY or install Go 1.24+." >&2
    exit 1
  fi
  mkdir -p "$PROJECT_DIR/bin"
  (cd "$PROJECT_DIR" && go build -o "$BINARY" ./cmd/syssetup)
fi

cd "$PROJECT_DIR"
if [[ "$#" -eq 0 ]]; then
  set -- tui
fi
exec "$BINARY" "$@"
