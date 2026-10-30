import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFileSync, spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';
import { createWSSession } from '../../web/src/adapters/ws-client.mjs';

const dir = mkdtempSync(join(tmpdir(), 'pot-ws-'));
const binary = join(dir, 'native-bridge');
let child;
let deadline;
let session;
const stops = [];
try {
  execFileSync('go', ['build', '-o', binary, './scripts/tests/native-bridge'], { stdio: 'inherit' });
  child = spawn(binary, [], { stdio: ['pipe', 'pipe', 'inherit'] });
  const exited = new Promise((resolve) => {
    child.once('error', (error) => resolve({ error }));
    child.once('exit', (code, signal) => resolve({ code, signal }));
  });
  child.once('error', (error) => console.error(error));
  deadline = setTimeout(() => child.kill(), 10000);
  deadline.unref();

  const rl = createInterface({ input: child.stdout });
  const url = await Promise.race([
    new Promise((resolve) => rl.once('line', (line) => resolve(JSON.parse(line).url))),
    delay(5000, undefined, { ref: false }).then(() => { throw new Error('fixture line timeout'); }),
    exited.then((result) => { throw new Error(`fixture exited early: ${JSON.stringify(result)}`); }),
  ]);
  const endpoint = url.replace(/^http/, 'ws');

  const frames = { a: [], b: [] };
  const statuses = [];
  session = createWSSession({ endpoint: async () => ({ url: endpoint }), host: globalThis });
  const runtime = await session.request('/api/v1/runtime', { method: 'GET' });
  assert.equal(runtime.runtime_id, 'control');

  const echoes = await Promise.all(
    Array.from({ length: 16 }, (_, index) =>
      session.request('/echo', { method: 'POST', body: { index } }),
    ),
  );
  echoes.forEach((body, index) => assert.deepEqual(body, { index }));

  const sub = (key) =>
    session.subscribe('/api/v1/events?assistant_id=' + key, {
      events: {
        conversation: (event) => {
          const data = JSON.parse(event.data);
          return () => frames[key].push(data);
        },
      },
      onStatus: (s) => statuses.push([key, s]),
    });

  const unsubA = sub('a');
  const unsubB = sub('b');
  stops.push(unsubA, unsubB);

  const waitFrames = async (key) => {
    const start = Date.now();
    while (frames[key].length < 1) {
      if (Date.now() - start > 3000) throw new Error(`frames ${key} timeout`);
      await delay(5);
    }
  };
  await Promise.all([waitFrames('a'), waitFrames('b')]);
  assert.equal(frames.a[0].kind, 'reset');
  assert.equal(frames.b[0].kind, 'reset');

  unsubA();
  const echoAfterA = await session.request('/echo', { method: 'POST', body: { index: 99 } });
  assert.deepEqual(echoAfterA, { index: 99 });

  unsubB();
  child.stdin.end();

  const exit = await Promise.race([exited, delay(2000, undefined, { ref: false }).then(() => ({ pending: true }))]);
  if (exit.pending) throw new Error('fixture did not exit');
  if (exit.code !== 0) throw new Error(`fixture exit code ${exit.code} signal ${exit.signal}`);

  console.log('native-bridge-integration: PASS');
} finally {
  if (deadline) clearTimeout(deadline);
  for (const stop of stops) stop();
  session?.close();
  if (child && child.exitCode === null && child.signalCode === null) {
    child.kill();
    await Promise.race([new Promise((r) => child.once('exit', r)), delay(2000, undefined, { ref: false })]);
  }
  rmSync(dir, { recursive: true, force: true });
}
