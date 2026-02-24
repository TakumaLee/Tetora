#!/usr/bin/env bash
# Tetora installer — safe for both `curl | bash` and `source <(curl ...)`.

_tetora_install() {
    local VERSION="${TETORA_VERSION:-1.4.1}"
    local INSTALL_DIR="${TETORA_INSTALL_DIR:-$HOME/.tetora/bin}"
    local BASE_URL="${TETORA_BASE_URL:-https://github.com/TakumaLee/Tetora/releases/download/v${VERSION}}"

    # Detect OS and architecture.
    local OS ARCH
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"

    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        *) echo "Unsupported architecture: $ARCH"; return 1 ;;
    esac

    case "$OS" in
        darwin|linux) ;;
        *) echo "Unsupported OS: $OS"; return 1 ;;
    esac

    local BINARY="tetora-${OS}-${ARCH}"
    local URL="${BASE_URL}/${BINARY}"

    echo "Tetora v${VERSION} installer"
    echo "  OS:   ${OS}"
    echo "  Arch: ${ARCH}"
    echo ""

    # Create install directory.
    mkdir -p "$INSTALL_DIR"

    # Download binary.
    echo "Downloading ${URL}..."
    if command -v curl &>/dev/null; then
        curl -fSL -o "${INSTALL_DIR}/tetora" "$URL" || { echo "Download failed"; return 1; }
    elif command -v wget &>/dev/null; then
        wget -q -O "${INSTALL_DIR}/tetora" "$URL" || { echo "Download failed"; return 1; }
    else
        echo "Error: curl or wget required"
        return 1
    fi

    chmod +x "${INSTALL_DIR}/tetora"

    echo ""
    echo "Installed to ${INSTALL_DIR}/tetora"
    echo ""

    # Add to PATH if not already present.
    if ! echo "$PATH" | tr ':' '\n' | grep -qF "$INSTALL_DIR"; then
        local EXPORT_LINE="export PATH=\"${INSTALL_DIR}:\$PATH\""
        local SHELL_RC=""

        case "$(basename "${SHELL:-/bin/bash}")" in
            zsh)  SHELL_RC="$HOME/.zshrc" ;;
            bash)
                if [ -f "$HOME/.bash_profile" ]; then
                    SHELL_RC="$HOME/.bash_profile"
                else
                    SHELL_RC="$HOME/.bashrc"
                fi
                ;;
            fish) SHELL_RC="$HOME/.config/fish/config.fish" ;;
        esac

        if [ -n "$SHELL_RC" ]; then
            if ! grep -qF ".tetora/bin" "$SHELL_RC" 2>/dev/null; then
                echo "" >> "$SHELL_RC"
                echo "# Tetora" >> "$SHELL_RC"
                echo "$EXPORT_LINE" >> "$SHELL_RC"
                echo "Added PATH to ${SHELL_RC}"
            fi
        fi

        # Apply to current shell immediately.
        export PATH="${INSTALL_DIR}:$PATH"
    fi

    echo ""
    echo "Get started:"
    echo "  tetora init      Setup wizard"
    echo "  tetora doctor    Health check"
    echo "  tetora serve     Start daemon"
}

_tetora_install
unset -f _tetora_install
