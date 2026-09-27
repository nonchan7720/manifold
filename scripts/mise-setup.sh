#!/bin/bash
# SessionStart hook: install tools declared in mise.toml for Claude Code on the web.
set -uo pipefail

[ "${CLAUDE_CODE_REMOTE:-}" != "true" ] && exit 0
cd "$CLAUDE_PROJECT_DIR" || exit 0

if ! command -v mise >/dev/null 2>&1; then
  curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh >&2 || exit 0
fi

export MISE_YES=1
mise trust >&2

mise install >&2 || echo "mise install failed; some tools may be missing" >&2

# Expose mise-managed tools (go, golangci-lint, ...) to Claude's Bash tool.
if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
  shims="${MISE_DATA_DIR:-$HOME/.local/share/mise}/shims"
  {
    echo "export MISE_YES=1"
    echo "export PATH=\"$shims:\$PATH\""
  } >> "$CLAUDE_ENV_FILE"
fi
exit 0
