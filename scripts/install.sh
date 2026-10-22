#!/bin/bash

set -e

# Default values
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="iket"
REPO="bhangun/iket"
VERSION=""
ARCH=$(uname -m)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')

# Convert architecture names to match GitHub releases
case $ARCH in
  x86_64|amd64)
    ARCH="amd64"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

# Convert OS names
case $OS in
  darwin|linux)
    # Valid OS names
    ;;
  *)
    echo "Unsupported OS: $OS"
    exit 1
    ;;
esac

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    -d|--dir)
      INSTALL_DIR="$2"
      shift 2
      ;;
    -v|--version)
      VERSION="$2"
      shift 2
      ;;
    -h|--help)
      echo "Install script for iket"
      echo ""
      echo "Usage: $0 [options]"
      echo "Options:"
      echo "  -d, --dir DIR     Installation directory (default: /usr/local/bin)"
      echo "  -v, --version VER Version to install (default: latest)"
      echo "  -h, --help        Show this help message"
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

# Get latest version if not specified
if [ -z "$VERSION" ]; then
  echo "Fetching latest version..."
  VERSION=$(curl -s https://api.github.com/repos/$REPO/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
  if [ -z "$VERSION" ]; then
    echo "Could not determine latest version"
    exit 1
  fi
fi

# Remove 'v' prefix if present
VERSION_CLEAN=${VERSION#v}

echo "Installing iket version $VERSION for $OS/$ARCH to $INSTALL_DIR"

# Construct download URL
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$VERSION/${BINARY_NAME}-${OS}-${ARCH}"

# Create temporary directory
TMP_DIR=$(mktemp -d)
cd "$TMP_DIR"

# Download binary
echo "Downloading from: $DOWNLOAD_URL"
if ! curl -L -o "$BINARY_NAME" "$DOWNLOAD_URL"; then
  echo "Failed to download binary"
  exit 1
fi

# Make binary executable
chmod +x "$BINARY_NAME"

# Create installation directory if it doesn't exist
sudo mkdir -p "$INSTALL_DIR"

# Move binary to installation directory
if ! sudo mv "$BINARY_NAME" "$INSTALL_DIR/"; then
  echo "Failed to move binary to $INSTALL_DIR (requires sudo)"
  exit 1
fi

# Verify installation
if command -v "$BINARY_NAME" &> /dev/null; then
  echo "Successfully installed $(command -v "$BINARY_NAME")"
  echo "Version: $($BINARY_NAME --version 2>/dev/null || echo 'unknown')"
else
  echo "Installation failed: $BINARY_NAME not found in PATH"
  exit 1
fi

# Cleanup
rm -rf "$TMP_DIR"

echo "Installation complete!"
echo "Run 'iket --help' to get started."