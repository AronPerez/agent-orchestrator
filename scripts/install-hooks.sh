#!/usr/bin/env bash
# Point git at the repo's tracked hooks (.githooks/). Idempotent; run once per
# clone. scripts/dev-setup.sh calls this for you.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
git config core.hooksPath .githooks
chmod +x .githooks/* 2>/dev/null || true
echo "Enabled git hooks: core.hooksPath = .githooks"
