#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="$HOME/.local/bin"
SERVICE_DIR="$HOME/.config/systemd/user"
SERVICE_NAME="simpllm"

CONFIG_DIR="$HOME/.config/simpllm"

echo "Installing simpllm..."

# Build
go build -o simpllm ./cmd/simpllm

# Install binary
mkdir -p "$INSTALL_DIR"
cp simpllm "$INSTALL_DIR/simpllm"
echo "  Binary: $INSTALL_DIR/simpllm"

# Install systemd service
mkdir -p "$SERVICE_DIR"
cp simpllm.service "$SERVICE_DIR/${SERVICE_NAME}.service"

# Update paths in service file
sed -i "s|%h/.local/bin/simpllm|$INSTALL_DIR/simpllm|g" "$SERVICE_DIR/${SERVICE_NAME}.service"
sed -i "s|%h/.config/simpllm|$CONFIG_DIR|g" "$SERVICE_DIR/${SERVICE_NAME}.service"

echo "  Service: $SERVICE_DIR/${SERVICE_NAME}.service"
echo ""
echo "To enable and start:"
echo "  systemctl --user daemon-reload"
echo "  systemctl --user enable --now ${SERVICE_NAME}"
