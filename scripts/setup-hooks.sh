#!/usr/bin/env bash
# Script to set up project git hooks by configuring core.hooksPath.
set -euo pipefail

cd "$(dirname "$0")/.."

echo "==> Configuring git hooks path..."
git config core.hooksPath .githooks

echo "==> Making hook scripts executable..."
chmod +x .githooks/pre-commit .githooks/pre-push

echo "✅ Git hooks configured successfully!"
