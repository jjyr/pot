#!/bin/sh
set -eu
exec >/build/build.log 2>&1
export LANG=C.UTF-8 LC_ALL=C.UTF-8
mkdir -p /cache/apk /cache/npm
# Each lockfile entry is intentionally passed as a separate package argument.
# shellcheck disable=SC2046
apk --cache-dir /cache/apk add $(cat /build/assets/packages.lock)
adduser -D -H -h /data/engine-home potdesktop
mkdir -p /opt/pot /opt/agents /opt/pot/novnc /data
cp /build/assets/* /opt/pot/
tar xzf /opt/pot/novnc.tar.gz -C /opt/pot/novnc --strip-components=1
rm /opt/pot/novnc.tar.gz
cp /opt/pot/agents-package.json /opt/agents/package.json
cp /opt/pot/agents-package-lock.json /opt/agents/package-lock.json
if ! NODE_OPTIONS=--max-old-space-size=512 npm ci --maxsockets=2 --cache /cache/npm --prefix /opt/agents --ignore-scripts --omit=dev --no-audit --no-fund; then
  free -m
  dmesg | tail -40
  exit 1
fi
rm -rf /root/.npm /var/cache/apk/*
chmod -R a+rX /opt/pot /opt/agents
chmod +x /opt/pot/pot-server /opt/pot/open-browser.sh
ln -sf /opt/agents/node_modules/.bin/codex /usr/local/bin/codex
node - <<'JS'
const fs = require('node:fs');
const zlib = require('node:zlib');
const binary = '/opt/agents/node_modules/@xai-official/grok-linux-arm64/bin/grok';
fs.writeFileSync(binary, zlib.brotliDecompressSync(fs.readFileSync(binary + '.br')), { mode: 0o755 });
JS
ln -sf /opt/agents/node_modules/@xai-official/grok-linux-arm64/bin/grok /usr/local/bin/grok
ln -sf /opt/pot/open-browser.sh /usr/local/bin/pot-browser
fc-cache -f
apk info -v | sort >/build/installed-packages.txt
python3 - <<'PY'
import json,pathlib
blocks=pathlib.Path('/lib/apk/db/installed').read_text().split('\n\n')
packages=[dict(l.split(':',1) for l in b.splitlines() if ':' in l) for b in blocks]
pathlib.Path('/build/package-sizes.json').write_text(json.dumps({p['P']:{'version':p['V'],'installed_bytes':int(p.get('I',0)),'archive_bytes':int(p.get('S',0))} for p in packages if 'P' in p},indent=2))
PY
# Export a clean root only. No host credentials, user sessions or mounted /build.
tar -C / -cf /build/rootfs.tar bin etc home lib media mnt opt root sbin srv tmp usr var data
