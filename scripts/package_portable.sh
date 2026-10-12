#!/bin/sh
set -eu

PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PORTABLE_DIR="$PROJECT_DIR/portable"
OUT_DIR="$PROJECT_DIR/dist/portable-v1.0.0"

command -v go >/dev/null 2>&1 || { echo "需要 Go 1.25 或更高版本。" >&2; exit 1; }
command -v zip >/dev/null 2>&1 || { echo "需要 zip 命令。" >&2; exit 1; }
command -v shasum >/dev/null 2>&1 || { echo "需要 shasum 命令。" >&2; exit 1; }

VERSION=$(sed -n 's/^var version = "\(.*\)"/\1/p' "$PORTABLE_DIR/main.go")
if [ "$VERSION" != "1.0.0" ]; then
  echo "版本不匹配：main.go 是 $VERSION，脚本预期 1.0.0。" >&2
  exit 1
fi

STAGE_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/space-sheriff-package.XXXXXX")
trap 'rm -rf "$STAGE_ROOT"' EXIT INT TERM

mkdir -p "$OUT_DIR"
rm -f "$OUT_DIR"/*.zip "$OUT_DIR/SHA256SUMS.txt"

package_one() {
  goos=$1
  goarch=$2
  label=$3
  stage="$STAGE_ROOT/$label"
  binary="SpaceSheriff"
  if [ "$goos" = "windows" ]; then
    binary="SpaceSheriff.exe"
  fi

  mkdir -p "$stage"
  (
    cd "$PORTABLE_DIR"
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 GOCACHE="${GOCACHE:-$STAGE_ROOT/gocache}" \
      go build -trimpath -ldflags="-s -w" -o "$stage/$binary" .
  )
  cp "$PROJECT_DIR/distribution/README-FIRST.txt" "$stage/README-FIRST.txt"
  cp "$PROJECT_DIR/LICENSE" "$stage/LICENSE"
  cp "$PROJECT_DIR/docs/releases/v1.0.0.md" "$stage/RELEASE-NOTES.md"
  (
    cd "$STAGE_ROOT"
    zip -qr "$OUT_DIR/SpaceSheriff-v$VERSION-$label.zip" "$label"
  )
}

package_one darwin amd64 macos-amd64
package_one darwin arm64 macos-arm64
package_one windows amd64 windows-amd64
package_one windows arm64 windows-arm64

(
  cd "$OUT_DIR"
  shasum -a 256 SpaceSheriff-v"$VERSION"-*.zip > SHA256SUMS.txt
)

echo "已生成：$OUT_DIR"
cat "$OUT_DIR/SHA256SUMS.txt"
