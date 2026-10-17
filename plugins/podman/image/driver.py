#!/usr/bin/env python3
"""Run through docker exec only; CDP is never published to the host/network."""

import json, sys, urllib.request
import websocket

pages = json.load(urllib.request.urlopen("http://127.0.0.1:9222/json"))
page = next(p for p in pages if p.get("type") == "page")
ws = websocket.create_connection(page["webSocketDebuggerUrl"], suppress_origin=True, timeout=15)
method = sys.argv[1] if len(sys.argv) > 1 else "Runtime.evaluate"
params = (
    json.loads(sys.argv[2])
    if len(sys.argv) > 2
    else {"expression": "JSON.stringify(window.potState)", "returnByValue": True}
)
ws.send(json.dumps({"id": 1, "method": method, "params": params}))
while True:
    result = json.loads(ws.recv())
    if result.get("id") == 1:
        print(json.dumps(result, ensure_ascii=False))
        break
ws.close()
if "error" in result or "exceptionDetails" in result.get("result", {}):
    sys.exit(1)
