#!/bin/sh
set -eu

PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export SPACE_SHERIFF_DATA_DIR="${SPACE_SHERIFF_DATA_DIR:-$PROJECT_DIR/portable/.local-data}"
cd "$PROJECT_DIR/portable"
exec go run .
