#!/bin/bash
# ═══════════════════════════════════════════════════════════
#  Evolution GO — Obfuscated Release Build
#  Generates a protected binary in dist-release/
# ═══════════════════════════════════════════════════════════
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="$SCRIPT_DIR"
LICENSING_URL="${LICENSING_URL:-https://license.evolutionfoundation.com.br}"
VERSION="${VERSION:-$(git -C "$PROJECT_DIR" describe --tags --always 2>/dev/null || echo "release")}"

echo "╔══════════════════════════════════════════════════════════╗"
echo "║        Evolution GO — Protected Release Build            ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""
echo "  URL:     $LICENSING_URL"
echo "  Version: $VERSION"
echo ""

# Step 1: Generate XOR-encoded URL
echo "[1/4] Generating XOR-encoded URL..."
ENCODE_OUTPUT=$(cd "$PROJECT_DIR" && go run ./tools/encode-url "$LICENSING_URL")
ENCODED=$(echo "$ENCODE_OUTPUT" | grep "^ENCODED=" | cut -d= -f2)
XOR_KEY=$(echo "$ENCODE_OUTPUT" | grep "^XOR_KEY=" | cut -d= -f2)

if [ -z "$ENCODED" ] || [ -z "$XOR_KEY" ]; then
    echo "ERROR: Failed to encode URL"
    exit 1
fi
echo "  ✓ URL encoded (${#ENCODED} hex chars)"

# Step 2: Check garble
echo "[2/4] Checking garble..."
if ! command -v garble &> /dev/null; then
    echo "  Installing garble..."
    go install mvdan.cc/garble@latest
fi
echo "  ✓ garble available"

# Step 3: Build with garble + ldflags
LDFLAGS="-s -w"
LDFLAGS="$LDFLAGS -X github.com/EvolutionAPI/evolution-go/pkg/core.encodedEP=$ENCODED"
LDFLAGS="$LDFLAGS -X github.com/EvolutionAPI/evolution-go/pkg/core.xorKey=$XOR_KEY"
LDFLAGS="$LDFLAGS -X main.version=$VERSION"

echo "[3/4] Building with garble (this takes a few minutes)..."
cd "$PROJECT_DIR"

# Detect OS/ARCH
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-amd64}"
EXT=""
if [ "$GOOS" = "windows" ]; then
    EXT=".exe"
fi

OUTPUT_FILE="$OUTPUT_DIR/evolution-go-${GOOS}-${GOARCH}${EXT}"

CGO_ENABLED=1 garble -literals -tiny build \
    -ldflags "$LDFLAGS" \
    -o "$OUTPUT_FILE" \
    ./cmd/evolution-go/

echo "  ✓ Binary built: $(basename "$OUTPUT_FILE") ($(du -h "$OUTPUT_FILE" | cut -f1))"

# Step 4: Verify protection
echo "[4/4] Verifying protection..."
FOUND=$(strings "$OUTPUT_FILE" | grep -ci "license.evolutionfoundation" || true)
if [ "$FOUND" -gt 0 ]; then
    echo "  ⚠ WARNING: URL found in binary strings ($FOUND occurrences)"
else
    echo "  ✓ URL not found in binary strings"
fi

FOUND_CORE=$(strings "$OUTPUT_FILE" | grep -ci "pkg/core" || true)
if [ "$FOUND_CORE" -gt 0 ]; then
    echo "  ⚠ WARNING: pkg/core references found ($FOUND_CORE)"
else
    echo "  ✓ Function names obfuscated"
fi

echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Build complete: $OUTPUT_FILE"
echo "╚══════════════════════════════════════════════════════════╝"
