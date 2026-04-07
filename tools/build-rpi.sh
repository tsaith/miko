#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少必要指令: $1" >&2
    exit 1
  fi
}

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "此腳本需要在 Linux 上執行。" >&2
  exit 1
fi

case "$(uname -m)" in
  aarch64|arm64) ;;
  *)
    echo "此腳本預期執行於 Linux ARM64（例如 Raspberry Pi）。目前架構: $(uname -m)" >&2
    exit 1
    ;;
esac

require_cmd go
require_cmd npm
require_cmd node
require_cmd wails
require_cmd pkg-config

if ! pkg-config --exists gtk+-3.0; then
  echo "缺少 GTK 3 開發套件，請先安裝 gtk+-3.0 對應的 dev package。" >&2
  exit 1
fi

BUILD_TAGS="raspi"
if pkg-config --exists webkit2gtk-4.1; then
  BUILD_TAGS="${BUILD_TAGS},webkit2_41"
elif ! pkg-config --exists webkit2gtk-4.0; then
  echo "缺少 WebKitGTK 開發套件，請先安裝 webkit2gtk-4.1 或 webkit2gtk-4.0 對應的 dev package。" >&2
  exit 1
fi

echo "使用 build tags: ${BUILD_TAGS}"
echo "開始建置 Raspberry Pi 執行檔..."

wails build -clean -tags "${BUILD_TAGS}"

echo "建置完成: ${ROOT_DIR}/build/bin/miko"
