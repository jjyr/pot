import http from 'node:http';
import {createRequire} from 'node:module';
import {dirname} from 'node:path';

export async function installFixtureWebSocket(server, {heartbeatMs = 15000} = {}) {
  const playwrightCorePkg = createRequire(process.env.POT_PLAYWRIGHT_MODULE || import.meta.url).resolve('playwright-core/package.json');
  const utilsBundlePath = dirname(playwrightCorePkg) + '/lib/utilsBundle.js';
  const { wsServer: WebSocketServer } = createRequire(import.meta.url)(utilsBundlePath);
  const clients = new Set();
  const wss = new WebSocketServer({
    noServer: true,
    maxPayload: 16 * 1024 * 1024,
    handleProtocols: (protos) => (protos && protos.has && protos.has('pot.v1') ? 'pot.v1' : false),
  });

  const heartbeatTimers = new Set();

  const onUpgrade = (req, socket, head) => {
    const protos = req.headers['sec-websocket-protocol'];
    const list = protos ? String(protos).split(',').map((s) => s.trim()) : [];
    if (!list.includes('pot.v1')) {
      socket.write('HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n');
      socket.destroy();
      return;
    }
    if (req.url !== '/api/v1/ws') {
      socket.write('HTTP/1.1 404 Not Found\r\nConnection: close\r\n\r\n');
      socket.destroy();
      return;
    }
    wss.handleUpgrade(req, socket, head, (ws) => {
      wss.emit('connection', ws, req);
    });
  };

  server.on('upgrade', onUpgrade);

  wss.on('connection', (ws, upgradeReq) => {
    clients.add(ws);
    const upgradeCookie = upgradeReq && upgradeReq.headers ? upgradeReq.headers.cookie : undefined;
    const inflight = new Map(); // id -> {req, res, op}

    const send = (msg) => {
      if (ws.readyState === 1) {
        try { ws.send(JSON.stringify(msg)); } catch {}
      }
    };

    const startHeartbeat = () => {
      if (!heartbeatMs) return;
      const t = setInterval(() => send({type: 'heartbeat', ts: Date.now()}), heartbeatMs);
      heartbeatTimers.add(t);
      ws.on('close', () => { clearInterval(t); heartbeatTimers.delete(t); });
    };
    startHeartbeat();

    ws.on('message', (raw) => {
      let msg;
      try { msg = JSON.parse(raw.toString()); } catch {
        send({type: 'response', id: null, status: 400, error: {code: 'bad_json', message: 'invalid json'}});
        return;
      }
      const {type, id, ...rest} = msg || {};
if ((type === 'request' || type === 'subscribe') && (inflight.size >= 64 || inflight.has(id))) {
  send({ type: 'response', id, status: 429, error: { code: 'too_many_requests', message: 'Too many inflight requests' } });
  return;
}
      if (type === 'request') {
        handleHttp(id, rest, inflight, send, upgradeCookie);
      } else if (type === 'subscribe') {
        handleSubscribe(id, rest, inflight, send, ws, upgradeCookie);
      } else if (type === 'cancel' || type === 'unsubscribe') {
        const entry = inflight.get(id);
        if (entry) {
          inflight.delete(id);
          try { entry.req.destroy(); } catch {}
          try { entry.res.destroy(); } catch {}
        }
      } else {
        send({type: 'response', id: id ?? null, status: 400, error: {code: 'bad_type', message: `unknown type ${type}`}});
      }
    });

    ws.on('close', () => {
      clients.delete(ws);
      const entries = Array.from(inflight.values());
      inflight.clear();
      for (const entry of entries) {
        try { entry.req.destroy(); } catch {}
        try { entry.res && entry.res.destroy(); } catch {}
      }
    });
    ws.on('error', () => {});
  });

  function handleHttp(id, spec, inflight, send, upgradeCookie) {
    const port = server.address().port;
    const method = (spec.method || 'GET').toUpperCase();
    const path = spec.path || '/';
    const headers = {host: `127.0.0.1:${port}`, ...(upgradeCookie ? {cookie: upgradeCookie} : {})};
    let bodyBuf = null;
    if (spec.body !== undefined) {
      const s = typeof spec.body === 'string' ? JSON.stringify(spec.body) : JSON.stringify(spec.body);
      bodyBuf = Buffer.from(s, 'utf8');
      headers['content-type'] = headers['content-type'] || 'application/json';
      headers['content-length'] = String(bodyBuf.length);
    }
    const req = http.request({host: '127.0.0.1', port, method, path, headers});
    inflight.set(id, {req, res: null, op: 'request'});
    req.on('response', (res) => {
      const entry = inflight.get(id);
      if (!entry || entry.req !== req) { res.destroy(); return; }
      entry.res = res;
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => {
        if (inflight.get(id) !== entry) return;
        inflight.delete(id);
        const buf = Buffer.concat(chunks).toString('utf8');
        const status = res.statusCode || 500;
        if (status >= 200 && status < 300) {
          let result;
          try { result = buf.length ? JSON.parse(buf) : undefined; }
          catch { result = buf; }
          send({type: 'response', id, status, result});
        } else {
          let err;
          try {
            const obj = buf.length ? JSON.parse(buf) : undefined;
            if (obj && typeof obj === 'object') {
              err = {code: obj.code || obj.error || 'http_error', message: obj.message || obj.error_description || `HTTP ${status}`, ...obj};
            } else err = {code: 'http_error', message: buf || `HTTP ${status}`};
          } catch { err = {code: 'http_error', message: buf || `HTTP ${status}`}; }
          send({type: 'response', id, status, error: err});
        }
      });
      res.on('error', () => {
        if (inflight.get(id) !== entry) return;
        inflight.delete(id);
        send({type: 'response', id, status: 503, error: {code: 'http_error', message: 'response error'}});
      });
    });
    req.on('error', () => {
      const entry = inflight.get(id);
      if (!entry || entry.req !== req) return;
      inflight.delete(id);
      send({type: 'response', id, status: 503, error: {code: 'http_error', message: 'request error'}});
    });
    if (bodyBuf) req.write(bodyBuf);
    req.end();
  }

  function handleSubscribe(id, spec, inflight, send, ws, upgradeCookie) {
    const port = server.address().port;
    const method = 'GET';
    const path = spec.path || '/api/v1/ws';
    const headers = {host: `127.0.0.1:${port}`, accept: 'text/event-stream', ...(upgradeCookie ? {cookie: upgradeCookie} : {})};
    const req = http.request({host: '127.0.0.1', port, method, path, headers});
    const entry = {req, res: null, op: 'subscribe'};
    inflight.set(id, entry);
    req.on('response', (res) => {
      if (inflight.get(id) !== entry) { res.destroy(); return; }
      entry.res = res;
      if (res.statusCode < 200 || res.statusCode >= 300) {
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => {
          if (inflight.get(id) !== entry) return;
          inflight.delete(id);
          const buf = Buffer.concat(chunks).toString('utf8');
          let err;
          try {
            const obj = buf.length ? JSON.parse(buf) : undefined;
            if (obj && typeof obj === 'object') err = {code: obj.code || obj.error || 'http_error', message: obj.message || `HTTP ${res.statusCode}`, ...obj};
            else err = {code: 'http_error', message: buf || `HTTP ${res.statusCode}`};
          } catch { err = {code: 'http_error', message: buf || `HTTP ${res.statusCode}`}; }
          send({type: 'response', id, status: res.statusCode, error: err});
        });
        return;
      }
      send({type: 'response', id, status: 200, result: {ok: true}});
      res.setEncoding('utf8');
      let buf = '';
      const block = {event: null, id: null, data: []};
      const flush = () => {
        if (inflight.get(id) !== entry) return;
        if (!block.event && !block.data.length) { block.event = null; block.id = null; return; }
        let parsed;
        try { parsed = JSON.parse(block.data.join('\n')); }
catch {
  inflight.delete(id);
  send({ type: 'end', id, status: 500, error: { code: 'bad_event', message: 'Invalid event JSON' } });
  req.destroy();
  res.destroy();
  return;
}
        send({type: 'event', id, event: block.event || 'message', event_id: block.id, data: parsed});
        block.event = null; block.id = null; block.data = [];
      };
      res.on('data', (chunk) => {
        if (inflight.get(id) !== entry) return;
        try {
          buf += chunk;
          let nl;
          while ((nl = buf.indexOf('\n')) !== -1) {
            const line = buf.slice(0, nl).replace(/\r$/, '');
            buf = buf.slice(nl + 1);
            if (line.startsWith(':')) {
              if (inflight.get(id) === entry) send({type: 'heartbeat', id, ts: Date.now()});
              continue;
            }
            if (line === '') { flush(); continue; }
            const idx = line.indexOf(':');
            const field = idx === -1 ? line : line.slice(0, idx);
            let val = idx === -1 ? '' : line.slice(idx + 1);
            if (val.startsWith(' ')) val = val.slice(1);
            if (field === 'event') block.event = val;
            else if (field === 'id') block.id = val;
            else if (field === 'data') block.data.push(val);
          }
        } catch (e) {
          if (inflight.get(id) !== entry) return;
          inflight.delete(id);
          send({type: 'end', id, status: 500, error: {code: 'sse_parse', message: 'sse parse error'}});
          try { req.destroy(); } catch {}
          try { res.destroy(); } catch {}
        }
      });
      res.on('end', () => {
        if (buf.length) {
          const line = buf.replace(/\r$/, '');
          if (line && !line.startsWith(':')) {
            const idx = line.indexOf(':');
            const field = idx === -1 ? line : line.slice(0, idx);
            let val = idx === -1 ? '' : line.slice(idx + 1);
            if (val.startsWith(' ')) val = val.slice(1);
            if (field === 'event') block.event = val;
            else if (field === 'id') block.id = val;
            else if (field === 'data') block.data.push(val);
          }
          buf = '';
        }
        if (inflight.get(id) !== entry) return;
        flush();
        if (inflight.get(id) !== entry) return;
        inflight.delete(id);
        send({type: 'end', id, status: 200});
      });
      res.on('error', () => {
        if (inflight.get(id) !== entry) return;
        inflight.delete(id);
        send({type: 'end', id, status: 503, error: {code: 'sse_error', message: 'sse error'}});
      });
    });
    req.on('error', () => {
      const cur = inflight.get(id);
      if (cur !== entry) return;
      inflight.delete(id);
      send({type: 'response', id, status: 503, error: {code: 'http_error', message: 'subscribe error'}});
    });
    req.end();
  }

  const disconnect = () => {
    for (const ws of clients) {
      try { ws.terminate(); } catch {}
    }
    clients.clear();
  };

  const close = async () => {
    server.removeListener('upgrade', onUpgrade);
    for (const t of heartbeatTimers) clearInterval(t);
    heartbeatTimers.clear();
    disconnect();
    await new Promise((resolve) => wss.close(() => resolve()));
  };

  return {clients, disconnect, close};
}
