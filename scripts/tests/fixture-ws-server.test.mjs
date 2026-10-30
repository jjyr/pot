import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { createRequire } from 'node:module';
import { dirname } from 'node:path';
import { installFixtureWebSocket } from './helpers/ws-server.mjs';

const require = createRequire(process.env.POT_PLAYWRIGHT_MODULE || import.meta.url);
const playwrightPkg = require.resolve('playwright-core/package.json');
const utilsBundle = require(dirname(playwrightPkg) + '/lib/utilsBundle.js');
const WebSocket = utilsBundle.ws;
const LF = String.fromCharCode(10);

function withTimeout(promise, ms, label) {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error('timeout: ' + (label || 'operation') + ' after ' + ms + 'ms')), ms);
  });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

function createInbox(ws) {
  const queue = [];
  const waiters = [];
  let closed = false;
  ws.on('message', (data) => {
    let frame;
    try { frame = JSON.parse(data.toString()); } catch (err) { frame = { parseError: String(err) }; }
    if (waiters.length > 0) waiters.shift()(frame);
    else queue.push(frame);
  });
  ws.on('close', () => {
    closed = true;
    while (waiters.length > 0) waiters.shift()(undefined);
  });
  return {
    wait(timeoutMs = 2000, label = 'ws frame') {
      if (queue.length > 0) return Promise.resolve(queue.shift());
      if (closed) return Promise.resolve(undefined);
      return new Promise((resolve, reject) => {
        let timer;
        const waiter = (frame) => { clearTimeout(timer); resolve(frame); };
        timer = setTimeout(() => {
          const i = waiters.indexOf(waiter);
          if (i >= 0) waiters.splice(i, 1);
          reject(new Error('timeout: ' + label + ' after ' + timeoutMs + 'ms'));
        }, timeoutMs);
        waiters.push(waiter);
      });
    },
    size() { return queue.length; }
  };
}

function openWs(url, opts = {}) {
  const ws = new WebSocket(url, ['pot.v1'], { headers: opts.headers || {} });
  const inbox = createInbox(ws);
  return withTimeout(new Promise((resolve, reject) => {
    const onError = (err) => { ws.off('open', onOpen); reject(err); };
    const onOpen = () => { ws.off('error', onError); resolve({ ws, inbox }); };
    ws.once('open', onOpen);
    ws.once('error', onError);
  }), 2000, 'ws open');
}

function startServer(handler) {
  const server = http.createServer(handler);
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      server.removeListener('error', reject);
      resolve(server);
    });
  });
}

function wsUrl(server) {
  const addr = server.address();
  return 'ws://127.0.0.1:' + addr.port + '/api/v1/ws';
}

function makeDeferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

function readJsonBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on('data', (c) => chunks.push(c));
    req.on('end', () => {
      const text = Buffer.concat(chunks).toString('utf8');
      try { resolve(text.length ? JSON.parse(text) : undefined); } catch (err) { reject(err); }
    });
    req.on('error', reject);
  });
}

function sseComment(text) { return ': ' + text + LF + LF; }
function sseEvent(name, data) { return 'event: ' + name + LF + 'data: ' + JSON.stringify(data) + LF + LF; }

function trackResponseClose(res) {
  const state = { closed: false, headersSentAtClose: null, closePromise: null };
  state.closePromise = new Promise((resolve) => {
    res.once('close', () => {
      state.closed = true;
      state.headersSentAtClose = res.headersSent;
      resolve();
    });
  });
  return state;
}

function registerCleanup(t, server, helper, ws) {
  let helperClosed = false;
  let wsTerminated = false;
  const closeHelper = async () => {
    if (helperClosed || !helper) return;
    helperClosed = true;
    await withTimeout(Promise.resolve().then(() => helper.close()), 2000, 'helper.close');
  };
  t.after(async () => {
    if (ws && !wsTerminated) {
      wsTerminated = true;
      try { ws.terminate(); } catch (err) {}
    }
    await closeHelper();
    if (server) {
      server.closeAllConnections();
      if (server.listening) {
        await withTimeout(new Promise((resolve, reject) => {
          server.close((err) => err ? reject(err) : resolve());
        }), 2000, 'server.close');
      }
    }
  });
  return { closeHelper };
}

async function collectFrames(inbox, shouldStop, maxFrames, timeoutMs, label) {
  const frames = [];
  const deadline = Date.now() + timeoutMs;
  for (let i = 0; i < maxFrames; i++) {
    if (shouldStop(frames)) return frames;
    const remaining = deadline - Date.now();
    if (remaining <= 0) break;
    const frame = await inbox.wait(remaining, label);
    if (frame === undefined) break;
    frames.push(frame);
  }
  if (!shouldStop(frames)) throw new Error('timeout: ' + label);
  return frames;
}

test('cookie POST result/error and SSE cookie', async (t) => {
  const receipts = [];
  const server = await startServer((req, res) => {
    receipts.push({ url: req.url, cookie: req.headers.cookie, method: req.method });
    if (req.method === 'POST' && req.url === '/api/v1/echo') {
      readJsonBody(req).then((body) => {
        res.writeHead(200, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ ok: true, body }));
      }).catch((err) => {
        res.writeHead(400, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ error: String(err) }));
      });
      return;
    }
    if (req.method === 'GET' && req.url === '/api/v1/fail') {
      res.writeHead(422, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ error: 'invalid' }));
      return;
    }
    if (req.method === 'GET' && req.url === '/api/v1/sse-cookie') {
      res.writeHead(200, { 'content-type': 'text/event-stream' });
      res.flushHeaders();
      res.end();
      return;
    }
    res.writeHead(404);
    res.end();
  });
  const helper = await installFixtureWebSocket(server, { heartbeatMs: 0 });
  registerCleanup(t, server, helper);
  const { ws, inbox } = await openWs(wsUrl(server), { headers: { cookie: 'sid=abc123' } });
  ws.send(JSON.stringify({ type: 'request', id: 'r1', method: 'POST', path: '/api/v1/echo', body: { hello: 'world' } }));
  ws.send(JSON.stringify({ type: 'request', id: 'r2', method: 'GET', path: '/api/v1/fail' }));
  ws.send(JSON.stringify({ type: 'subscribe', id: 's1', path: '/api/v1/sse-cookie' }));
  const frames = await collectFrames(inbox, (fs) => {
    const has = (id, type) => fs.some((f) => f && f.id === id && f.type === type);
    return has('r1', 'response') && has('r2', 'response') && has('s1', 'response') && has('s1', 'end');
  }, 10, 2000, 'cookie frames');
  const r1 = frames.find((f) => f && f.id === 'r1' && f.type === 'response');
  const r2 = frames.find((f) => f && f.id === 'r2' && f.type === 'response');
  const s1 = frames.find((f) => f && f.id === 's1' && f.type === 'response');
  const sEnd = frames.find((f) => f && f.id === 's1' && f.type === 'end');
  assert.equal(r1.status, 200);
  assert.deepEqual(r1.result, { ok: true, body: { hello: 'world' } });
  assert.equal(r2.status, 422);
  assert.deepEqual(r2.error, { code: 'invalid', message: 'HTTP 422', error: 'invalid' });
  assert.equal(s1.type, 'response');
  assert.equal(s1.status, 200);
  assert.deepEqual(s1.result, { ok: true });
  assert.equal(sEnd.status, 200);
  const echo = receipts.find((r) => r.url === '/api/v1/echo');
  const fail = receipts.find((r) => r.url === '/api/v1/fail');
  const sse = receipts.find((r) => r.url === '/api/v1/sse-cookie');
  assert.equal(echo.cookie, 'sid=abc123');
  assert.equal(fail.cookie, 'sid=abc123');
  assert.equal(sse.cookie, 'sid=abc123');
});

test('ACK/comment/event with id/EOF', async (t) => {
  const server = await startServer((req, res) => {
    if (req.url === '/api/v1/events') {
      res.writeHead(200, { 'content-type': 'text/event-stream' });
      res.flushHeaders();
      res.write(sseComment('heartbeat'));
      res.write(`id: event-1\n${sseEvent('message', { text: 'hello' })}`);
      res.end();
      return;
    }
    res.writeHead(404);
    res.end();
  });
  const helper = await installFixtureWebSocket(server, { heartbeatMs: 0 });
  registerCleanup(t, server, helper);
  const { ws, inbox } = await openWs(wsUrl(server));
  ws.send(JSON.stringify({ type: 'subscribe', id: 'sub1', path: '/api/v1/events' }));
  const resp = await inbox.wait(2000, 'sse response');
  assert.equal(resp.type, 'response');
  assert.equal(resp.id, 'sub1');
  assert.equal(resp.status, 200);
  assert.deepEqual(resp.result, { ok: true });
  const hb = await inbox.wait(2000, 'sse heartbeat');
  assert.equal(hb.type, 'heartbeat');
  const ev = await inbox.wait(2000, 'sse event');
  assert.equal(ev.type, 'event');
  assert.equal(ev.id, 'sub1');
  assert.equal(ev.event, 'message');
  assert.equal(ev.event_id, 'event-1');
  assert.deepEqual(ev.data, { text: 'hello' });
  const end = await inbox.wait(2000, 'sse end');
  assert.equal(end.type, 'end');
  assert.equal(end.id, 'sub1');
  assert.equal(end.status, 200);
  assert.equal(inbox.size(), 0);
});

test('cancel preheaders then same id', async (t) => {
  const receipt = makeDeferred();
  let heldState = null;
  const server = await startServer((req, res) => {
    if (req.url === '/api/v1/held') {
      heldState = trackResponseClose(res);
      receipt.resolve(heldState);
      return;
    }
    if (req.url === '/api/v1/ok') {
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ second: true }));
      return;
    }
    res.writeHead(404);
    res.end();
  });
  const helper = await installFixtureWebSocket(server, { heartbeatMs: 0 });
  registerCleanup(t, server, helper);
  const { ws, inbox } = await openWs(wsUrl(server));
  ws.send(JSON.stringify({ type: 'request', id: 'held-1', method: 'GET', path: '/api/v1/held' }));
  const state = await withTimeout(receipt.promise, 2000, 'held receipt');
  assert.equal(state.closed, false);
  ws.send(JSON.stringify({ type: 'cancel', id: 'held-1' }));
  await withTimeout(state.closePromise, 2000, 'held close');
  assert.equal(state.closed, true);
  assert.equal(state.headersSentAtClose, false);
  assert.equal(inbox.size(), 0);
  ws.send(JSON.stringify({ type: 'request', id: 'held-1', method: 'GET', path: '/api/v1/ok' }));
  const ok = await inbox.wait(2000, 'reused id response');
  assert.equal(ok.id, 'held-1');
  assert.equal(ok.type, 'response');
  assert.equal(ok.status, 200);
  assert.deepEqual(ok.result, { second: true });
});

test('unsubscribe A keeps B', async (t) => {
  const streams = new Map();
  const allStreams = makeDeferred();
  let count = 0;
  const server = await startServer((req, res) => {
    const u = new URL(req.url, 'http://x');
    if (u.pathname === '/api/v1/sse') {
      res.writeHead(200, { 'content-type': 'text/event-stream' });
      res.flushHeaders();
      const p = u.searchParams.get('p');
      streams.set(p, { res, state: trackResponseClose(res) });
      count += 1;
      if (count === 2) allStreams.resolve();
      return;
    }
    res.writeHead(404);
    res.end();
  });
  const helper = await installFixtureWebSocket(server, { heartbeatMs: 0 });
  registerCleanup(t, server, helper);
  const { ws, inbox } = await openWs(wsUrl(server));
  ws.send(JSON.stringify({ type: 'subscribe', id: 'a', path: '/api/v1/sse?p=A' }));
  ws.send(JSON.stringify({ type: 'subscribe', id: 'b', path: '/api/v1/sse?p=B' }));
  const ackFrames = await collectFrames(inbox, (fs) => fs.filter((f) => f && f.type === 'response' && (f.id === 'a' || f.id === 'b')).length === 2, 4, 2000, 'sub responses');
  for (const f of ackFrames) {
    if (f && f.type === 'response') {
      assert.equal(f.status, 200);
      assert.deepEqual(f.result, { ok: true });
    }
  }
  await withTimeout(allStreams.promise, 2000, 'streams registered');
  assert.ok(streams.has('A'));
  assert.ok(streams.has('B'));
  ws.send(JSON.stringify({ type: 'unsubscribe', id: 'a' }));
  await withTimeout(streams.get('A').state.closePromise, 2000, 'A upstream close');
  assert.equal(streams.get('A').state.closed, true);
  assert.equal(streams.get('B').state.closed, false);
  streams.get('B').res.write(sseEvent('message', { still: 'alive' }));
  const frames = await collectFrames(inbox, (fs) => fs.some((f) => f && f.id === 'b' && f.type === 'event'), 5, 2000, 'b event');
  const ev = frames.find((f) => f && f.id === 'b' && f.type === 'event');
  assert.equal(ev.event, 'message');
  assert.deepEqual(ev.data, { still: 'alive' });
});

test('close two streams and one held request closes all upstreams', async (t) => {
  const upstreams = [];
  const allReceived = makeDeferred();
  const server = await startServer((req, res) => {
    if (req.url === '/api/v1/stream1' || req.url === '/api/v1/stream2') {
      res.writeHead(200, { 'content-type': 'text/event-stream' });
      res.flushHeaders();
      upstreams.push({ path: req.url, res, state: trackResponseClose(res) });
      if (upstreams.length === 3) allReceived.resolve();
      return;
    }
    if (req.url === '/api/v1/held') {
      upstreams.push({ path: req.url, res, state: trackResponseClose(res) });
      if (upstreams.length === 3) allReceived.resolve();
      return;
    }
    res.writeHead(404);
    res.end();
  });
  const helper = await installFixtureWebSocket(server, { heartbeatMs: 0 });
  const cleanup = registerCleanup(t, server, helper);
  const { ws } = await openWs(wsUrl(server));
  ws.send(JSON.stringify({ type: 'subscribe', id: 'x1', path: '/api/v1/stream1' }));
  ws.send(JSON.stringify({ type: 'subscribe', id: 'x2', path: '/api/v1/stream2' }));
  ws.send(JSON.stringify({ type: 'request', id: 'h1', method: 'GET', path: '/api/v1/held' }));
  await withTimeout(allReceived.promise, 2000, 'all upstreams');
  assert.equal(upstreams.length, 3);
  assert.equal(helper.clients.size, 1);
  await cleanup.closeHelper();
  await withTimeout(Promise.all(upstreams.map((u) => u.state.closePromise)), 2000, 'all upstreams close');
  for (const u of upstreams) assert.equal(u.state.closed, true);
  assert.equal(helper.clients.size, 0);
});

test('periodic heartbeat with no subscriptions', async (t) => {
  const server = await startServer((req, res) => {
    res.writeHead(404);
    res.end();
  });
  const helper = await installFixtureWebSocket(server, { heartbeatMs: 15 });
  registerCleanup(t, server, helper);
  const { ws, inbox } = await openWs(wsUrl(server));
  const frames = await collectFrames(inbox, (fs) => fs.some((f) => f && f.type === 'heartbeat'), 3, 2000, 'periodic heartbeat');
  const hb = frames.find((f) => f && f.type === 'heartbeat');
  assert.ok(hb);
  assert.equal(hb.type, 'heartbeat');
});
