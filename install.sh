#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="$HOME/.local/bin"
SERVICE_NAME="simpllm"
CONFIG_DIR="$HOME/.config/simpllm"

echo "Installing simpllm..."

# Build
go build -o simpllm ./cmd/simpllm

# Install binary
mkdir -p "$INSTALL_DIR"
cp simpllm "$INSTALL_DIR/simpllm"
echo "  Binary: $INSTALL_DIR/simpllm"

# Detect OS and install service
OS="$(uname -s)"

if [ "$OS" = "Darwin" ]; then
    # macOS — launchd
    SERVICE_DIR="$HOME/Library/LaunchAgents"
    mkdir -p "$SERVICE_DIR"
    PLIST="$SERVICE_DIR/${SERVICE_NAME}.plist"
    cp simpllm.plist "$PLIST"

    # Substitute placeholders with actual paths
    sed -i '' "s|/Users/YOU/.local/bin/simpllm|$INSTALL_DIR/simpllm|g" "$PLIST"
    sed -i '' "s|/Users/YOU/.config/simpllm/config.yaml|$CONFIG_DIR/config.yaml|g" "$PLIST"
    sed -i '' "s|/Users/YOU/Library/Logs/simpllm.log|$HOME/Library/Logs/simpllm.log|g" "$PLIST"
    sed -i '' "s|/Users/YOU/Library/Sockets/simpllm.sock|$HOME/Library/Sockets/simpllm.sock|g" "$PLIST"
    sed -i '' "s|/Users/YOU|$HOME|g" "$PLIST"

    # Ensure directories exist
    mkdir -p "$HOME/Library/Logs"
    mkdir -p "$HOME/Library/Sockets"

    echo "  Service: $PLIST"
    echo ""
    echo "To enable and start:"
    echo "  launchctl bootout gui/\$(id -u) $PLIST 2>/dev/null || true"
    echo "  launchctl bootstrap gui/\$(id -u) $PLIST"
else
    # Linux — systemd
    SERVICE_DIR="$HOME/.config/systemd/user"
    mkdir -p "$SERVICE_DIR"
    cp simpllm.service "$SERVICE_DIR/${SERVICE_NAME}.service"
    cp simpllm.socket "$SERVICE_DIR/${SERVICE_NAME}.socket"

    # Update paths in service file
    sed -i "s|%h/.local/bin/simpllm|$INSTALL_DIR/simpllm|g" "$SERVICE_DIR/${SERVICE_NAME}.service"
    sed -i "s|%h/.config/simpllm|$CONFIG_DIR|g" "$SERVICE_DIR/${SERVICE_NAME}.service"

    echo "  Service: $SERVICE_DIR/${SERVICE_NAME}.service"
    echo "  Socket:  $SERVICE_DIR/${SERVICE_NAME}.socket"
    echo ""
    echo "To enable and start:"
    echo "  systemctl --user daemon-reload"
    echo "  systemctl --user enable --now ${SERVICE_NAME}"
fi
