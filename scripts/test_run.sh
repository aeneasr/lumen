#!/usr/bin/env bash
# Tests for run.sh URL construction and OS/arch detection logic.
# Runs entirely offline — no real HTTP calls are made.
set -euo pipefail

PASS=0
FAIL=0

ok() {
  echo "  PASS: $1"
  PASS=$((PASS + 1))
}

fail() {
  echo "  FAIL: $1"
  echo "        expected: $2"
  echo "        got:      $3"
  FAIL=$((FAIL + 1))
}

assert_eq() {
  local desc="$1" expected="$2" got="$3"
  if [ "$expected" = "$got" ]; then
    ok "$desc"
  else
    fail "$desc" "$expected" "$got"
  fi
}

# ---------------------------------------------------------------------------
# asset_name <version_tag> <os> <arch>
# Mirrors the logic in run.sh: strip leading 'v', build asset filename.
# ---------------------------------------------------------------------------
asset_name() {
  local version="$1" os="$2" arch="$3"
  local ver_no_v="${version#v}"
  case "$os" in
    windows) echo "lumen-${ver_no_v}-${os}-${arch}.zip" ;;
    *)       echo "lumen-${ver_no_v}-${os}-${arch}.tar.gz" ;;
  esac
}

# download_url <repo> <version_tag> <os> <arch>
download_url() {
  local repo="$1" version="$2" os="$3" arch="$4"
  local asset
  asset="$(asset_name "$version" "$os" "$arch")"
  echo "https://github.com/${repo}/releases/download/${version}/${asset}"
}

# ---------------------------------------------------------------------------
# arch normalisation (mirrors run.sh case statement)
# ---------------------------------------------------------------------------
normalise_arch() {
  case "$1" in
    x86_64)  echo "amd64" ;;
    aarch64) echo "arm64" ;;
    *)       echo "$1" ;;
  esac
}

echo "=== asset name tests ==="
assert_eq "macOS arm64 asset" \
  "lumen-0.0.1-alpha.4-darwin-arm64.tar.gz" \
  "$(asset_name "v0.0.1-alpha.4" "darwin" "arm64")"

assert_eq "macOS amd64 asset" \
  "lumen-0.0.1-alpha.4-darwin-amd64.tar.gz" \
  "$(asset_name "v0.0.1-alpha.4" "darwin" "amd64")"

assert_eq "Linux amd64 asset" \
  "lumen-0.0.1-alpha.4-linux-amd64.tar.gz" \
  "$(asset_name "v0.0.1-alpha.4" "linux" "amd64")"

assert_eq "Linux arm64 asset" \
  "lumen-0.0.1-alpha.4-linux-arm64.tar.gz" \
  "$(asset_name "v0.0.1-alpha.4" "linux" "arm64")"

assert_eq "Windows amd64 asset (zip)" \
  "lumen-0.0.1-alpha.4-windows-amd64.zip" \
  "$(asset_name "v0.0.1-alpha.4" "windows" "amd64")"

echo ""
echo "=== download URL tests ==="
REPO="ory/lumen"
VERSION="v0.0.1-alpha.4"

assert_eq "macOS arm64 URL" \
  "https://github.com/ory/lumen/releases/download/v0.0.1-alpha.4/lumen-0.0.1-alpha.4-darwin-arm64.tar.gz" \
  "$(download_url "$REPO" "$VERSION" "darwin" "arm64")"

assert_eq "Linux amd64 URL" \
  "https://github.com/ory/lumen/releases/download/v0.0.1-alpha.4/lumen-0.0.1-alpha.4-linux-amd64.tar.gz" \
  "$(download_url "$REPO" "$VERSION" "linux" "amd64")"

assert_eq "Windows amd64 URL" \
  "https://github.com/ory/lumen/releases/download/v0.0.1-alpha.4/lumen-0.0.1-alpha.4-windows-amd64.zip" \
  "$(download_url "$REPO" "$VERSION" "windows" "amd64")"

echo ""
echo "=== arch normalisation tests ==="
assert_eq "x86_64 → amd64"  "amd64" "$(normalise_arch "x86_64")"
assert_eq "aarch64 → arm64" "arm64" "$(normalise_arch "aarch64")"
assert_eq "arm64 passthrough" "arm64" "$(normalise_arch "arm64")"
assert_eq "amd64 passthrough" "amd64" "$(normalise_arch "amd64")"

echo ""
echo "=== binary candidate priority tests ==="
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

BIN_DIR="${TMP_DIR}/bin"
mkdir -p "$BIN_DIR"

# Simulate: only downloaded binary present → should pick lumen-os-arch
touch "${BIN_DIR}/lumen-linux-amd64"
chmod +x "${BIN_DIR}/lumen-linux-amd64"

FOUND=""
for candidate in "${BIN_DIR}/lumen" "${BIN_DIR}/lumen-linux-amd64"; do
  if [ -x "$candidate" ]; then FOUND="$candidate"; break; fi
done
assert_eq "picks lumen-linux-amd64 when lumen absent" \
  "${BIN_DIR}/lumen-linux-amd64" "$FOUND"

# Simulate: both present → local dev build wins
touch "${BIN_DIR}/lumen"
chmod +x "${BIN_DIR}/lumen"

FOUND=""
for candidate in "${BIN_DIR}/lumen" "${BIN_DIR}/lumen-linux-amd64"; do
  if [ -x "$candidate" ]; then FOUND="$candidate"; break; fi
done
assert_eq "prefers bin/lumen (dev build) over downloaded binary" \
  "${BIN_DIR}/lumen" "$FOUND"

echo ""
echo "=== summary ==="
echo "  passed: $PASS"
echo "  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
