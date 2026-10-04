#!/bin/sh
set -eu
exec >/data/check.log 2>&1
/bin/sh /opt/pot/desktop.sh &
desktop=$!
trap 'kill -TERM "$desktop" 2>/dev/null || true' EXIT
python3 - <<'PY'
import json,os,pathlib,time,urllib.request,subprocess
start=time.monotonic()
for i in range(120):
 try:
  urllib.request.urlopen('http://127.0.0.1:17681/',timeout=1).read()
  urllib.request.urlopen('http://127.0.0.1:9222/json/version',timeout=1).read()
  if pathlib.Path('/data/product/runtime.json').exists():break
 except Exception:pass
 time.sleep(.5)
else:raise RuntimeError('Desktop, Chromium or Runtime not ready offline')
fonts={}
font_env=dict(os.environ,HOME="/data/engine-home",XDG_CONFIG_HOME="/data/engine-home/.config")
for lang,text in {'zh-cn':'简体中文','zh-tw':'繁體中文','ja':'日本語かなカナ','ko':'한국어漢字'}.items():
 pattern='sans-serif:lang='+lang
 spans=[tuple(int(c,16) for c in v.split('-')) for v in subprocess.check_output(['fc-match','-f','%{charset}',pattern],text=True,env=font_env).split()]
 assert all(any(a[0]<=ord(c)<=a[-1] for a in spans) for c in text),lang
 fonts[lang]=subprocess.check_output(['fc-match','-f','%{family}',pattern],text=True,env=font_env)
 expected={'zh-cn':'SC','zh-tw':'TC','ja':'JP','ko':'KR'}[lang]
 assert 'CJK '+expected in fonts[lang],(lang,fonts[lang])
pi=subprocess.check_output(['pi','--version'],text=True).strip()
assert pi=='0.87.1',pi
codex=subprocess.check_output(['codex','--version'],text=True).strip()
assert codex.startswith('codex-cli '),codex
cmds=[]
for p in pathlib.Path('/proc').glob('[0-9]*/cmdline'):
 try:cmds.append(p.read_bytes().replace(b'\0',b' ').decode(errors='replace'))
 except FileNotFoundError:pass
for program in ['xfwm4','xfdesktop','xfsettingsd','xfce4-panel']:assert any(program in c for c in cmds),program
assert not any('apk add' in c or 'npm ci' in c for c in cmds)
pathlib.Path('/data/offline-result.json').write_text(json.dumps({'networking':False,'ready_seconds':round(time.monotonic()-start,2),'pi':pi,'codex':codex,'fonts':fonts},indent=2))
print('Desktop + Runtime + Pi + CJK ready without a network interface')
PY
kill -TERM "$desktop"
wait "$desktop"
test -f /data/browser-stopped
trap - EXIT
