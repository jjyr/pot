import assert from 'node:assert/strict';
import { createWSSession } from '../../web/src/adapters/ws-client.mjs';
import { wsClock } from './helpers/ws-clock.mjs';

// Block 1: two concurrent requests, out-of-order responses, denial
{
  const clock = wsClock();
  const session = createWSSession({
    host: clock.host,
    endpoint: async () => ({ url: 'ws://fixture/ws', token: 'secret' })
  });
  try {
    const a = session.request('/api/v1/a', { method: 'GET' });
    const b = session.request('/api/v1/b', { method: 'GET' });

    await clock.tick();
    const socket = clock.sockets[0];
    socket.open();

    const reqs = socket.sent.filter((m) => m.type === 'request');
    assert.equal(reqs.length, 2);
    const reqA = reqs.find((m) => m.path === '/api/v1/a');
    const reqB = reqs.find((m) => m.path === '/api/v1/b');
    assert.ok(reqA?.id);
    assert.ok(reqB?.id);

    socket.emit({ type: 'response', id: reqB.id, status: 200, result: { value: 2 } });
    const rb = await b;
    assert.deepEqual(rb, { value: 2 });

    socket.emit({ type: 'response', id: reqA.id, status: 200, result: { value: 1 } });
    const ra = await a;
    assert.deepEqual(ra, { value: 1 });

    const denied = session.request('/api/v1/denied', { method: 'GET' });
    await clock.tick();
    const deniedId = socket.sent.filter((m) => m.type === 'request').at(-1).id;
    socket.emit({
      type: 'response',
      id: deniedId,
      status: 403,
      error: { code: 'denied', message: 'denied' }
    });
    await assert.rejects(denied, (err) => {
      assert.equal(err.status, 403);
      assert.equal(err.code, 'denied');
      return true;
    });
  } finally {
    session.close();
  }
}

{
  const clock = wsClock();
  const statuses = [];
  const session = createWSSession({ host: clock.host, endpoint: async () => ({ url: 'ws://fixture/ws' }) });
  try {
    session.subscribe('/api/v1/changes', { events: { changed: () => () => {} }, onStatus: s => statuses.push(s) });
    await clock.tick();
    const socket = clock.sockets[0];
    socket.open();
    const id = socket.sent.find(f => f.type === 'subscribe').id;
    socket.emit({ type: 'response', id, status: 200, result: {} });
    await clock.advance(15000);
    socket.emit({ type: 'heartbeat' });
    await clock.advance(5000);
    assert.equal(statuses.at(-1), 'offline');
  } finally {
    session.close();
  }
}

// Block 2: abort via signal, and server-close rejection
{
  const clock = wsClock();
  const session = createWSSession({
    host: clock.host,
    endpoint: async () => ({ url: 'ws://fixture/ws', token: 'secret' })
  });
  try {
    const controller = new AbortController();
    const p = session.request('/api/v1/slow', { method: 'GET', signal: controller.signal });

    await clock.tick();
    const socket = clock.sockets[0];
    socket.open();

    const reqId = socket.sent.filter((m) => m.type === 'request').at(-1).id;
    assert.ok(reqId);

    const rej = assert.rejects(p, { name: 'AbortError' });
    controller.abort();
    await rej;

    const cancel = socket.sent.find((m) => m.type === 'cancel' && m.id === reqId);
    assert.ok(cancel, 'cancel sent for aborted request id');

    const mut = session.request('/api/v1/mutate', { method: 'POST', body: { x: 1 } });
    const rej2 = assert.rejects(mut, (err) => {
      assert.equal(err.status, 503);
      assert.equal(err.unknown, true);
      return true;
    });
    socket.serverClose();
    await rej2;
  } finally {
    session.close();
  }
}

// Block 3: subscribe lifecycle
{
  const clock = wsClock();
  const received = [];
  const statuses = [];
  const session = createWSSession({
    host: clock.host,
    endpoint: async () => ({ url: 'ws://fixture/ws', token: 'secret' })
  });
  try {
    const stop = session.subscribe('/api/v1/changes', {
      events: {
        changed: (event, initial) => {
          const data = JSON.parse(event.data);
          return () => received.push({ data, initial });
        }
      },
      onStatus: (state, error) => statuses.push(state)
    });

    await clock.tick();
    const socket = clock.sockets[0];
    socket.open();

    const sub = socket.sent.find((m) => m.type === 'subscribe');
    assert.ok(sub?.id);
    socket.emit({ type: 'response', id: sub.id, status: 200, result: {} });

    assert.ok(statuses.every((s) => s === 'connecting'));
    assert.equal(statuses.at(-1), 'connecting');

    socket.emit({
      type: 'event',
      id: sub.id,
      event: 'changed',
      data: { topics: { x: 1 } },
      event_id: 'boot:1'
    });

    assert.equal(received.length, 1);
    assert.deepEqual(received[0].data, { topics: { x: 1 } });
    assert.equal(received[0].initial, true);
    assert.equal(statuses.at(-1), 'connected');

    stop();
    session.close();
    console.log('summary: 3 blocks passed — concurrency, abort/close, subscribe');
  } finally {
    session.close();
  }
}
{
  const clock = wsClock();
  let deliveries = 0;
  let stop;
  const session = createWSSession({
    host: clock.host,
    endpoint: async () => ({ url: 'ws://fixture/ws' }),
  });

  try {
    stop = session.subscribe('/api/v1/changes', {
      events: {
        changed: () => () => {
          deliveries++;
        },
      },
      onStatus: (state) => {
        if (state === 'connected') stop();
      },
    });

    await clock.tick();
    const socket = clock.sockets[0];
    socket.open();

    const sub = socket.sent.find((f) => f.type === 'subscribe');
    assert.ok(sub, 'subscribe frame sent');
    const id = sub.id;

    socket.emit({ type: 'response', id, status: 200, result: {} });
    socket.emit({
      type: 'event',
      id,
      event: 'changed',
      data: {},
      event_id: 'boot1',
    });

    assert.strictEqual(deliveries, 0, 'no delivery after reentrant stop');
    assert.ok(
      socket.sent.some((f) => f.type === 'unsubscribe' && f.id === id),
      'unsubscribe frame sent for the cancelled subscription'
    );
  } finally {
    session.close();
  }
}

{
const clock = wsClock();
const received = [];
const session = createWSSession({
  host: clock.host,
  endpoint: async () => ({ url: 'ws://fixture/ws' }),
});
try {
session.subscribe('/api/v1/changes', {
  events: {
    changed: (e, initial) => () => received.push(initial),
  },
});

await clock.tick();
const oldSocket = clock.sockets[0];
oldSocket.open();
const oldId = oldSocket.sent.find((f) => f.type === 'subscribe').id;

oldSocket.emit({ type: 'response', id: oldId, status: 200 });
oldSocket.emit({
  type: 'event',
  id: oldId,
  event: 'changed',
  data: {},
  event_id: 'boot:1',
});
assert.deepEqual(received, [true]);

const pending = session.request('/api/v1/messages', {
  method: 'POST',
  body: { request_id: 'same' },
});
const rejection = assert.rejects(
  pending,
  (e) => e.status === 503 && e.unknown === true,
);

const oldHandler = oldSocket.onmessage;
oldSocket.serverClose();
await rejection;
await clock.tick();
await clock.advance(1000);
await clock.tick();

const newSocket = clock.sockets.at(-1);
assert.notEqual(newSocket, oldSocket);
newSocket.open();
const newId = newSocket.sent.find((f) => f.type === 'subscribe').id;
assert.notEqual(newId, oldId);
assert.equal(
  newSocket.sent.filter((f) => f.type === 'request').length,
  0,
);

newSocket.emit({ type: 'response', id: newId, status: 200 });
newSocket.emit({
  type: 'event',
  id: newId,
  event: 'changed',
  data: {},
  event_id: 'boot:2',
});
assert.deepEqual(received, [true, true]);

oldHandler({
  data: JSON.stringify({
    type: 'event',
    id: oldId,
    event: 'changed',
    data: {},
    event_id: 'old:1',
  }),
});
assert.equal(received.length, 2);

} finally {
  // ensure cleanup even if close throws
  try { session.close?.(); } catch {}
}
}
{
  const clock = wsClock();
  const session = createWSSession({
    host: clock.host,
    endpoint: async () => ({ url: 'ws://fixture/ws' })
  });
  try {
    const request = session.request('/api/v1/test');
    const rejected = assert.rejects(
      request,
      err => err.status === 504 && err.code === 'request_timeout' && err.unknown === true
    );
    await clock.tick();
    const socket = clock.sockets[0];
    socket.open();
    const id = socket.sent.find(f => f.type === 'request').id;
    socket.emit({
      type: 'response',
      id,
      status: 504,
      error: { code: 'request_timeout', message: 'Deadline' }
    });
    await rejected;
  } finally {
    session.close();
  }
}
