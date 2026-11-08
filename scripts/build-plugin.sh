#!/bin/sh
# Refreshes the Claude Code plugin's bundled CLI and shared references, then validates
# the plugin and its marketplace entry when the `claude` CLI is available.
#
# Usage: scripts/build-plugin.sh [--with-agents]   (--with-agents rebuilds both device agents first)
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

references=.agents/skills/tb/references
cp "$references/cli.md" "$references/flows.md" "$references/multi-device.md" plugin/skills/tb/

if [ "${1:-}" = "--with-agents" ]; then
  scripts/build.sh
else
  echo "building tb (agents from the source tree)"
fi
mkdir -p plugin/bin
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o plugin/bin/tb ./cmd/tb
chmod +x plugin/bin/tb
echo "plugin/bin/tb ($(du -h plugin/bin/tb | cut -f1), darwin/arm64)"

if command -v claude >/dev/null 2>&1; then
  claude plugin validate ./plugin
  claude plugin validate .
fi
