#!/usr/bin/env bash
set -euo pipefail

# Determine plugin root: prefer env var set by Claude Code plugin system,
# fall back to deriving from script location (local dev / direct invocation).
PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"

# Platform detection
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
esac

# Environment defaults
export LUMEN_BACKEND="${LUMEN_BACKEND:-ollama}"
export LUMEN_EMBED_MODEL="${LUMEN_EMBED_MODEL:-ordis/jina-embeddings-v2-base-code}"

# Find binary: check bin/ first, then goreleaser dist/ output, then download
BINARY=""
for candidate in \
  "${PLUGIN_ROOT}/bin/lumen" \
  "${PLUGIN_ROOT}/bin/lumen-${OS}-${ARCH}"; do
  if [ -x "$candidate" ]; then
    BINARY="$candidate"
    break
  fi
done

# Download on first run if no binary found
if [ -z "$BINARY" ]; then
  BINARY="${PLUGIN_ROOT}/bin/lumen-${OS}-${ARCH}"
  REPO="ory/lumen"

  # Always use the version pinned in the manifest — keeps plugin and binary in sync
  MANIFEST="${PLUGIN_ROOT}/.release-please-manifest.json"
  if [ ! -f "$MANIFEST" ]; then
    echo "Error: .release-please-manifest.json not found in ${PLUGIN_ROOT}" >&2
    exit 1
  fi
  VERSION="v$(grep '"[.]"' "$MANIFEST" | sed 's/.*"[^"]*"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')"
  if [ -z "$VERSION" ] || [ "$VERSION" = "v" ]; then
    echo "Error: could not read version from ${MANIFEST}" >&2
    exit 1
  fi

  ASSET="lumen-${VERSION#v}-${OS}-${ARCH}"
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"

  echo "Downloading lumen ${VERSION} for ${OS}/${ARCH}..." >&2
  mkdir -p "$(dirname "$BINARY")"

  curl -sfL "$URL" -o "$BINARY"
  chmod +x "$BINARY"
  echo "Installed lumen to ${BINARY}" >&2
fi

exec "$BINARY" "$@"
