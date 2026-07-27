#!/bin/sh
set -eu
cd "$(dirname "$0")"
python3 -m venv work/build-venv-macos
work/build-venv-macos/bin/python -m pip install -r requirements-build.txt
work/build-venv-macos/bin/python -m unittest discover -s tests -v
PYINSTALLER_CONFIG_DIR="${TMPDIR:-/tmp}/space-sheriff-pyinstaller" \
  work/build-venv-macos/bin/pyinstaller --noconfirm --clean SpaceSheriff.spec
echo "Build complete: dist/SpaceSheriff.app"
