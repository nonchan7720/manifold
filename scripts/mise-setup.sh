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

# dl.google.com may be blocked by the environment's network policy. Fall back to
# fetching the Go toolchain module from proxy.golang.org and linking it into mise.
go_version="$(mise config get tools.go 2>/dev/null || true)"
if [ -n "$go_version" ] && ! mise where "go@$go_version" >/dev/null 2>&1; then
  if ! mise install "go@$go_version" >&2; then
    system_go="$(PATH="/usr/local/go/bin:/usr/bin:$PATH" command -v go || true)"
    if [ -n "$system_go" ]; then
      goroot="$(GOTOOLCHAIN="go$go_version" "$system_go" env GOROOT)" &&
        mise link --force "go@$go_version" "$goroot" >&2
    fi
  fi
fi

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
