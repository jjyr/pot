import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./helpers/browser-ws-fixture.js', import.meta.url), 'utf8');

const sources = [];
class FakeCloseEvent extends Event {
  constructor(type, init = {}) {
    super(type);
    this.code = init.code ?? 1000;
    this.reason = init.reason ?? '';
    this.wasClean = init.wasClean ?? true;
  }
}
class FakeEventSource extends EventTarget {
  constructor(url) {
    super();
    this.url = url;
    this.readyState = 0;
    sources.push(this);
  }
  close() {
    this.readyState = 2;
  }
}

const context = {
  window: {},
  EventTarget,
  Event,
  MessageEvent,
  CloseEvent: FakeCloseEvent,
  AbortController,
  setTimeout,
  clearTimeout,
  setInterval,
  clearInterval,
  fetch: async () => ({ ok: true, status: 200, json: async () => ({ ok: true }) }),
  EventSource: FakeEventSource,
  console,
};
context.globalThis = context;

vm.runInNewContext(source, context, { filename: 'browser-ws-fixture.js' });

const ws = new context.window.WebSocket('/api/v1/changes');
const messages = [];
ws.onmessage = (ev) => {
  messages.push(JSON.parse(ev.data));
};

try {
await new Promise((r) => setTimeout(r, 0));

ws.send(JSON.stringify({ type: 'subscribe', id: 's', path: '/api/v1/changes' }));
assert.equal(messages.length, 0, 'no synchronous messages expected on subscribe');

assert.equal(sources.length, 1, 'one EventSource created');
sources[0].dispatchEvent(
  new MessageEvent('changed', { data: '{"tasks":0}', lastEventId: 'boot:1' })
);
await new Promise((r) => setTimeout(r, 10));

assert.equal(messages.length, 2, 'expected response and event frames');
assert.equal(messages[0].type, 'response');
assert.equal(messages[0].id, 's');
assert.equal(messages[1].type, 'event');
assert.equal(messages[1].id, 's');
assert.equal(messages[1].event_id, 'boot:1');

sources[0].dispatchEvent(
  new MessageEvent('changed', { data: '{"tasks":1}', lastEventId: 'boot:2' })
);
ws.close();
await new Promise((r) => setTimeout(r, 10));

assert.equal(messages.length, 2, 'queued delivery must be dropped after close');

console.log('PASS synthetic WS queues frames and drops closed deliveries');
} finally { ws.close(); }
