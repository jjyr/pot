#!/bin/sh
set -eu
exec >/data/check.log 2>&1
for cli in agy antigravity claude; do
  if command -v "$cli" >/dev/null 2>&1; then
    echo "Removed CLI is still installed: $cli"
    exit 1
  fi
done
test ! -e /opt/pot/agy
test ! -e /opt/pi/node_modules/@anthropic-ai/claude-code-linux-arm64-musl
su potdesktop -c 'pi --version && codex --version && grok --version'
echo "Supported engines present; removed CLI binaries absent."
