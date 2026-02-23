#!/usr/bin/env bash
set -euo pipefail

VERSION="${TETORA_VERSION:-1.0.0}"
INSTALL_DIR="${TETORA_INSTALL_DIR:-$HOME/.tetora/bin}"
BASE_URL="${TETORA_BASE_URL:-https://github.com/TakumaLee/Tetora/releases/download/v${VERSION}}"

# Detect OS and architecture.
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
    darwin|linux) ;;
    *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

BINARY="tetora-${OS}-${ARCH}"
URL="${BASE_URL}/${BINARY}"

echo "Tetora v${VERSION} installer"
echo "  OS:   ${OS}"
echo "  Arch: ${ARCH}"
echo ""

# Create install directory.
mkdir -p "$INSTALL_DIR"

# Download binary.
echo "Downloading ${URL}..."
if command -v curl &>/dev/null; then
    curl -fSL -o "${INSTALL_DIR}/tetora" "$URL"
elif command -v wget &>/dev/null; then
    wget -q -O "${INSTALL_DIR}/tetora" "$URL"
else
    echo "Error: curl or wget required"
    exit 1
fi

chmod +x "${INSTALL_DIR}/tetora"

echo ""
echo "Installed to ${INSTALL_DIR}/tetora"
echo ""

# Check PATH.
if ! echo "$PATH" | tr ':' '\n' | grep -qF "$INSTALL_DIR"; then
    echo "Add to your PATH:"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    echo ""
    echo "Add this line to ~/.zshrc or ~/.bashrc for persistence."
    echo ""
fi

echo "Get started:"
echo "  tetora init      Setup wizard"
echo "  tetora doctor    Health check"
echo "  tetora serve     Start daemon"
