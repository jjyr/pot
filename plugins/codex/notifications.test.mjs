import {test} from 'node:test';
import assert from 'node:assert/strict';
import {Readable} from 'node:stream';
import {frames} from './client.mjs';
import {stream} from './notifications.mjs';

const pair = {id: 'pair', route: 'control/main'};
const event = (cursor, kind = 'terminal') => ({cursor, id: `notice-${cursor}`, kind, at: '2026-01-01T00:00:00Z', data: {id: 'task', status: 'completed', result: 'external result'}});
const wire = events => Readable.from(events.map(e => `id: ${e.cursor}\nevent: update\ndata: ${JSON.stringify(e)}\n\n`));

test('SSE parser handles chunks, heartbeat and bounds', async () => {
  const e = event(1); const text = `: heartbeat\n\nid: 1\nevent: update\ndata: ${JSON.stringify(e)}\n\n`;
  const response = Readable.from([...text].map(c => Buffer.from(c))); const got = [];
  await assert.rejects(async () => { for await (const value of frames(response)) got.push(value); }, /disconnected/);
  assert.deepEqual(got, [e]);
  await assert.rejects(async () => { for await (const value of frames(Readable.from(['x'.repeat(300000)]))) void value; }, /frame_limit/);
});

test('progress is checkpoint-only; duplicate/out-of-order replay never wakes assistant twice', async () => {
  const commits = []; const receipts = [];
  const controller = new AbortController(); let connected = 0;
  await stream({state: {pair: 'pair', cursor: 1, acks: []}}, async frame => {
    commits.push(structuredClone(frame));
    if (frame.state.cursor === 4) controller.abort();
  }, {pair, signal: controller.signal, delay: async () => {}, request: async (_, path) => {
    if (path.includes('/receipts/')) { receipts.push(path); return {}; }
    if (++connected > 1) throw Error('stop');
    return wire([event(2, 'progress'), event(2), event(1), event(3), event(4, 'progress')]);
  }});
  assert.deepEqual(commits.filter(f => f.events.length).flatMap(f => f.events.map(e => e.id)), ['pair:notice-3']);
  assert.equal(receipts.length, 1);
});

test('reconnect resumes committed cursor and drains persisted ACK after a crash', async () => {
  const calls = []; const commits = []; const controller = new AbortController();
  await stream({state: {pair: 'pair', cursor: 7, acks: ['notice-7']}}, async frame => {commits.push(frame); if (frame.state.cursor === 8) controller.abort();}, {
    pair, signal: controller.signal, delay: async () => {}, request: async (_, path, options) => {
      calls.push([path, options]); if (path.includes('/receipts/')) return {};
      return wire([event(7), event(8)]);
    },
  });
  assert.equal(calls[0][0], '/v1/receipts/notice-7');
  assert.equal(calls[1][1].headers['Last-Event-ID'], '7');
  assert.equal(commits.flatMap(f => f.events).length, 1);
});

test('expired cursors recover unaccepted critical notices from snapshot before subscribing', async () => {
  const frames = []; const controller = new AbortController(); let connections = 0;
  await stream({state: {pair: 'pair', cursor: 2, acks: []}}, async frame => {frames.push(structuredClone(frame));}, {
    pair, signal: controller.signal, delay: async () => {}, request: async (_, path) => {
      if (path.includes('/receipts/')) return {};
      if (path === '/v1/snapshot') return {pair: 'pair', route: 'control/main', cursor: 50, notices: [event(12, 'approval'), event(40)]};
      if (++connections === 1) {const error = Error('expired'); error.status = 409; throw error;}
      assert.match(path, /after=50/); controller.abort(); throw Error('stop');
    },
  });
  assert.equal(frames.flatMap(f => f.events).length, 2);
  assert.equal(frames.at(-1).state.cursor, 50);
});

test('acceptance rejection cannot ACK or advance the bridge cursor', async () => {
  const calls = []; const controller = new AbortController();
  await assert.rejects(stream({state: {pair: 'pair', cursor: 1, acks: []}}, async () => { throw Error('commit_failed'); }, {
    pair, signal: controller.signal, request: async (_, path) => {calls.push(path); return wire([event(2)]);},
  }), /commit_failed/);
  assert.equal(calls.length, 1);
});
