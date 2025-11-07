#!/bin/bash
# Copyright 2025 Spruce Health
# SPDX-License-Identifier: gpl-3.0-or-later

# Install git hooks for the repository

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
HOOKS_DIR="$REPO_ROOT/.git/hooks"

echo "Installing git hooks..."

# Install pre-commit hook
cp "$SCRIPT_DIR/pre-commit" "$HOOKS_DIR/pre-commit"
chmod +x "$HOOKS_DIR/pre-commit"
echo "✓ Installed pre-commit hook"

# Check if addlicense is installed
if ! command -v addlicense &> /dev/null; then
    echo ""
    echo "⚠️  WARNING: addlicense is not installed"
    echo "The pre-commit hook requires addlicense to function."
    echo ""
    echo "Install it with:"
    echo "  go install github.com/google/addlicense@latest"
    echo ""
    exit 1
fi

echo ""
echo "✓ All hooks installed successfully!"
echo ""
echo "The pre-commit hook will now check that all Go, HTML, and CSS files"
echo "have the GPL-3.0-or-later license header before allowing commits."
