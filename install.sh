#!/bin/bash
# =============================================================================
# Open Turkey - Install Script
# Productivity blocker for Linux
#
# This script automates compiling and installing Open Turkey.
# It must be run as root (or via sudo).
# =============================================================================

set -euo pipefail

# --- Check that the script is running as root ---
# Open Turkey needs root privileges to:
#   - copy the binary to /usr/local/bin
#   - install the systemd service
#   - create the database directory
if [ "$(id -u)" -ne 0 ]; then
    echo "Error: this script must be run as root."
    echo "Usage: sudo ./install.sh"
    exit 1
fi

# --- Change to the project directory ---
# Ensures the make commands find the Makefile
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# --- Compile the binary ---
echo "==> Step 1/2: Compiling Open Turkey..."
make build

# --- Install the binary and service, and configure systemd ---
echo "==> Step 2/2: Installing Open Turkey..."
make install

# --- Success message with usage instructions ---
echo ""
echo "============================================="
echo "  Open Turkey installed successfully!"
echo "============================================="
echo ""
echo "Useful commands:"
echo "  open-turkey block create <block>                    - Create a block"
echo "  open-turkey block add-site <block> <domains...>     - Add sites to the block"
echo "  open-turkey block remove-site <block> <domains...>  - Remove sites from the block"
echo "  open-turkey start <block>                           - Activate a block"
echo "  open-turkey stop <block>                            - Deactivate a block (if not locked)"
echo "  open-turkey status                                  - Show the status of the blocks"
echo ""
echo "Service management:"
echo "  systemctl status open-turkey   - View service status"
echo "  systemctl restart open-turkey  - Restart the service"
echo "  journalctl -u open-turkey -f   - Follow logs in real time"
echo ""
