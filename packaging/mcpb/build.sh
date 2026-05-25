#!/usr/bin/env bash
# Assemble the kubeleash Claude Desktop .mcpb bundle.
#
# A .mcpb is a plain zip with manifest.json at the archive ROOT plus the bundled
# binaries the manifest references (here: the macOS/Linux `kubeleash` and the
# Windows `kubeleash.exe`). Claude Desktop expands it and launches the binary at
# server.mcp_config.command (`${__dirname}/kubeleash`).
#
# Usage:
#   build.sh [--out <path.mcpb>] [BINARY ...]
#
# BINARY args are copied into the bundle by basename. With no BINARY args the
# script looks for binaries in $DIST_DIR (default: repo dist/), trying common
# GoReleaser layouts. The macOS/Linux artifact is staged as `kubeleash` and any
# Windows artifact as `kubeleash.exe`.
#
# Validation/packing:
#   This script lays out a staging dir and zips it directly (portable, no extra
#   deps). If the official mcpb CLI is available it is ALSO used to validate the
#   manifest and (preferably) to pack, which produces an identical-layout bundle
#   plus schema validation. Install/use via: `npx -y @anthropic-ai/mcpb pack <dir>`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DIST_DIR="${DIST_DIR:-$REPO_ROOT/dist}"
STAGE_DIR="$SCRIPT_DIR/stage"
OUT="$SCRIPT_DIR/kubeleash.mcpb"

bins=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    *) bins+=("$1"); shift ;;
  esac
done

echo "==> staging bundle in $STAGE_DIR"
rm -rf "$STAGE_DIR"
mkdir -p "$STAGE_DIR"

cp "$SCRIPT_DIR/manifest.json" "$STAGE_DIR/manifest.json"
[[ -f "$REPO_ROOT/LICENSE" ]] && cp "$REPO_ROOT/LICENSE" "$STAGE_DIR/LICENSE"
[[ -f "$REPO_ROOT/README.md" ]] && cp "$REPO_ROOT/README.md" "$STAGE_DIR/README.md"
[[ -f "$SCRIPT_DIR/icon.png" ]] && cp "$SCRIPT_DIR/icon.png" "$STAGE_DIR/icon.png"

stage_binary() {
  # $1 = source path; decides target name by extension.
  local src="$1" base
  base="$(basename "$src")"
  if [[ "$base" == *.exe ]]; then
    cp "$src" "$STAGE_DIR/kubeleash.exe"
    echo "    + kubeleash.exe  (from $src)"
  else
    cp "$src" "$STAGE_DIR/kubeleash"
    chmod +x "$STAGE_DIR/kubeleash"
    echo "    + kubeleash      (from $src)"
  fi
}

if [[ ${#bins[@]} -gt 0 ]]; then
  for b in "${bins[@]}"; do stage_binary "$b"; done
else
  echo "==> no binaries passed; discovering in $DIST_DIR"
  # GoReleaser default layout: dist/<build-id>_<os>_<arch>[_v1]/kubeleash[.exe]
  darwin_bin="$(find "$DIST_DIR" -type f -name kubeleash -path '*darwin*' 2>/dev/null | head -n1 || true)"
  linux_bin="$(find "$DIST_DIR" -type f -name kubeleash -path '*linux*' 2>/dev/null | head -n1 || true)"
  win_bin="$(find "$DIST_DIR" -type f -name kubeleash.exe 2>/dev/null | head -n1 || true)"

  # Prefer darwin for the unix `kubeleash` slot (Claude Desktop is macOS/Windows).
  unix_bin="${darwin_bin:-$linux_bin}"
  [[ -n "$unix_bin" ]] && stage_binary "$unix_bin"
  [[ -n "$win_bin" ]] && stage_binary "$win_bin"

  if [[ -z "$unix_bin" && -z "$win_bin" ]]; then
    echo "ERROR: no kubeleash binaries found in $DIST_DIR and none passed as args." >&2
    echo "       Build first, e.g.: GOTOOLCHAIN=local go build -o $STAGE_DIR/kubeleash ./cmd/kubeleash" >&2
    echo "       (or run goreleaser, or pass binary paths as arguments)." >&2
    exit 1
  fi
fi

# Validate the manifest is well-formed JSON regardless of CLI availability.
if command -v jq >/dev/null 2>&1; then
  jq empty "$STAGE_DIR/manifest.json"
  echo "==> manifest.json is valid JSON"
fi

# Prefer the official mcpb CLI for schema validation + packing when present.
MCPB=""
if command -v mcpb >/dev/null 2>&1; then
  MCPB="mcpb"
elif command -v npx >/dev/null 2>&1; then
  MCPB="npx -y @anthropic-ai/mcpb"
fi

if [[ -n "$MCPB" ]]; then
  echo "==> validating manifest with mcpb CLI"
  $MCPB validate "$STAGE_DIR/manifest.json" || {
    echo "WARNING: mcpb validate failed; check manifest against the .mcpb spec." >&2
  }
  echo "==> packing with mcpb CLI"
  $MCPB pack "$STAGE_DIR" "$OUT"
else
  echo "==> mcpb CLI unavailable; packing with zip (manifest.json at archive root)"
  ( cd "$STAGE_DIR" && rm -f "$OUT" && zip -r -q "$OUT" . )
fi

echo "==> built $OUT"
