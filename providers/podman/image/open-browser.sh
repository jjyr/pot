#!/bin/sh
# Launch the optional browser from the desktop panel/menu or viewer action.
set -eu
pid_file=/data/profile/browser.pid
old_pid=$(cat "$pid_file" 2>/dev/null || true)
if [ -z "$old_pid" ] || ! kill -0 "$old_pid" 2>/dev/null; then
  echo $$ >"$pid_file"
fi
exec env DISPLAY=:99 chromium --user-data-dir=/data/profile --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --no-first-run --disable-dev-shm-usage --disable-gpu --start-maximized about:blank
