#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PROTO_DIR="$ROOT_DIR/proto"
PY_OUT="$ROOT_DIR/backend/app/generated"
GO_OUT="$ROOT_DIR/internal/backendclient/proto"

mkdir -p "$PY_OUT"
mkdir -p "$GO_OUT"

uv run --project "$ROOT_DIR/backend" python -m grpc_tools.protoc \
  --proto_path="$PROTO_DIR" \
  --python_out="$PY_OUT" \
  --grpc_python_out="$PY_OUT" \
  "$PROTO_DIR/assistant.proto"

protoc \
  --proto_path="$PROTO_DIR" \
  --go_out="$GO_OUT" \
  --go_opt=paths=source_relative \
  --go-grpc_out="$GO_OUT" \
  --go-grpc_opt=paths=source_relative \
  "$PROTO_DIR/assistant.proto"
