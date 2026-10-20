import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { test } from 'node:test';
import { read } from './provider.mjs';

async function fixture(t, handler) {
  const server = createServer(handler);
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => {
    server.close(resolve);
    server.closeAllConnections();
  }));
  return `http://127.0.0.1:${server.address().port}`;
}

function request(endpoint, method = 'list_models') {
  return { version: 1, method, connection: { id: 'connection-a', type: 'magpie', revision: 1, endpoint, protocol: 'openai_responses' } };
}

function json(res, data) {
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify({ object: 'list', data }));
}

function credential(t, key) {
  const old = process.env.MAGPIE_API_KEY;
  if (key == null) delete process.env.MAGPIE_API_KEY;
  else process.env.MAGPIE_API_KEY = key;
  t.after(() => {
    if (old == null) delete process.env.MAGPIE_API_KEY;
    else process.env.MAGPIE_API_KEY = old;
  });
}

test('reads enabled models and groups with the exact opaque IDs and conservative capabilities', async t => {
  credential(t, null);
  const paths = [];
  const endpoint = await fixture(t, (req, res) => {
    paths.push(req.url);
    assert.equal(req.headers.authorization, 'Bearer magpie-pot');
    assert.match(req.headers['user-agent'], /^Pot\//);
    json(res, [
      { id: 'anthropic/claude-test', display_name: 'Claude Test', context_window: 200000, max_output_tokens: 64000, modalities: ['text', 'image', 'unknown', 'image'], secret: 'never-return' },
      { id: 'my routing group' },
      { id: 'audio/model', modalities: { input: ['text', 'audio'], output: ['audio'] } },
    ]);
  });
  for (const base of [endpoint, `${endpoint}/v1`, `${endpoint}/v1/`, `${endpoint}/v1/responses`, `${endpoint}/v1/chat/completions`, `${endpoint}/v1/messages`, `${endpoint}/v1beta/models/{model}:streamGenerateContent`]) {
    const result = await read(request(base));
    assert.equal(result.catalog.models[0].id, 'anthropic/claude-test');
    assert.deepEqual(result.catalog.models[0].input_modalities, ['text', 'image']);
    assert.equal(result.catalog.models[0].max_output, 64000);
    assert.equal(result.catalog.models[0].context_window, 200000);
    assert.equal(result.catalog.models[1].id, 'my routing group');
    assert.equal(result.catalog.models[1].input_modalities, undefined);
    assert.deepEqual(result.catalog.models[2].output_modalities, ['audio']);
    assert.ok(Number.isFinite(Date.parse(result.catalog.read_at)));
    assert.ok(!JSON.stringify(result).includes('never-return'));
  }
  assert.deepEqual(paths, Array(7).fill('/v1/models'));
});

test('each exchange resolves the current scoped credential and respects a reverse proxy prefix', async t => {
  credential(t, 'first-secret');
  const received = [];
  const endpoint = await fixture(t, (req, res) => {
    received.push([req.url, req.headers.authorization]);
    json(res, []);
  });
  await read(request(`${endpoint}/proxy/v1`));
  process.env.MAGPIE_API_KEY = 'second-secret';
  await read(request(`${endpoint}/proxy`));
  assert.deepEqual(received, [
    ['/proxy/v1/models', 'Bearer first-secret'],
    ['/proxy/v1/models', 'Bearer second-secret'],
  ]);
});

test('preserves independent quota windows, shared pools, source freshness and auto-renewal', async t => {
  const endpoint = await fixture(t, (req, res) => {
    assert.equal(req.url, '/v1/magpie/quotas');
    json(res, [{
      provider: 'codex', name: 'Work', kind: 'subscription', plan: 'Pro', user: 'person@example.com',
      readAt: '2026-10-20T01:00:00Z', asOf: '2026-10-20T01:00:00Z',
      until: '2026-11-20T00:00:00Z', renew: 'auto',
      credential: 'must-not-leak', windows: [
        { name: '5 hours', used: 90, remaining: 10, resetsAt: '2026-10-20T06:00:00Z', pool: 'shared' },
        { name: 'Weekly', used: 42.5, remaining: 57.5, resetsAt: '2026-10-27T00:00:00Z', pool: 'shared' },
      ],
    }]);
  });
  const { allowances } = await read(request(endpoint, 'read_allowances'));
  assert.equal(allowances.accounts.length, 1);
  const account = allowances.accounts[0];
  assert.equal(account.plan, 'Pro');
  assert.equal(account.renew, 'auto');
  assert.equal(account.until, '2026-11-20T00:00:00.000Z');
  assert.equal(account.read_at, '2026-10-20T01:00:00.000Z');
  assert.equal(account.identity_quality, 'ambiguous');
  assert.equal(account.id, undefined);
  assert.deepEqual(account.windows.map(w => w.used), [90, 42.5]);
  assert.ok(account.windows.every(w => w.pool === 'shared' && w.identity_quality === 'ambiguous' && w.id === undefined));
  assert.ok(!JSON.stringify(allowances).includes('must-not-leak'));
});

test('unknown, unlimited, deferred, and errored readings never become zero or active use', async t => {
  const endpoint = await fixture(t, (_req, res) => json(res, [
    { windows: [{ name: 'Unknown' }, { name: 'Unlimited', used: 100, unlimited: true }, { name: 'Known empty', used: 0 }] },
    { error: 'private upstream credential', windows: [{ name: 'Failed', used: 100 }] },
    { readDeferred: true, windows: [{ name: 'Deferred', used: 100 }] },
  ]));
  const result = await read(request(endpoint, 'read_allowances'));
  assert.equal(result.allowances.accounts[0].windows[0].used, undefined);
  assert.equal(result.allowances.accounts[0].windows[1].used, undefined);
  assert.equal(result.allowances.accounts[0].windows[1].unlimited, true);
  assert.equal(result.allowances.accounts[0].windows[2].used, 0);
  assert.equal(result.allowances.accounts[1].windows[0].used, undefined);
  assert.equal(result.allowances.accounts[2].windows[0].used, undefined);
  assert.ok(!JSON.stringify(result).includes('private upstream'));
});

test('quota permission failures do not affect model discovery or expose upstream bodies', async t => {
  credential(t, 'super-secret');
  const endpoint = await fixture(t, (req, res) => {
    if (req.url === '/v1/models') return json(res, [{ id: 'group' }]);
    res.writeHead(403);
    res.end('super-secret user@example.com');
  });
  assert.deepEqual(await read(request(endpoint, 'read_allowances')), { error: { code: 'permission' } });
  assert.equal((await read(request(endpoint))).catalog.models[0].id, 'group');
});

test('does not follow redirects carrying credentials to another endpoint', async t => {
  credential(t, 'super-secret');
  let leaked = false;
  const target = await fixture(t, (_req, res) => {
    leaked = true;
    json(res, []);
  });
  const endpoint = await fixture(t, (_req, res) => {
    res.writeHead(302, { Location: target });
    res.end();
  });
  assert.deepEqual(await read(request(endpoint)), { error: { code: 'unavailable' } });
  assert.equal(leaked, false);
});

test('rejects malformed catalog/allowances rather than silently publishing false metadata', async t => {
  const cases = [
    ['list_models', { data: {} }],
    ['list_models', { data: [null] }],
    ['list_models', { data: [{ id: 'same' }, { id: 'same' }] }],
    ['list_models', { data: [{ id: 'x', context_window: -1 }] }],
    ['list_models', { data: [{ id: 'x', modalities: ['text', 4] }] }],
    ['read_allowances', { data: [{ windows: {} }] }],
    ['read_allowances', { data: [{ windows: [{ used: -1 }] }] }],
    ['read_allowances', { data: [{ windows: [{ used: '90' }] }] }],
    ['read_allowances', { data: [{ windows: [], until: 'not-a-date' }] }],
  ];
  let payload;
  const endpoint = await fixture(t, (_req, res) => res.end(JSON.stringify(payload)));
  for (const [method, value] of cases) {
    payload = value;
    assert.deepEqual(await read(request(endpoint, method)), { error: { code: 'invalid_response' } });
  }
});

test('bounds the response body before JSON decoding', async t => {
  const endpoint = await fixture(t, (_req, res) => res.end(' '.repeat(1024 * 1024 + 1)));
  assert.deepEqual(await read(request(endpoint)), { error: { code: 'overflow' } });
});

test('cancels in-flight requests without leaking the endpoint or fetch diagnostics', async t => {
  const endpoint = await fixture(t, (_req, _res) => {});
  assert.deepEqual(await read(request(endpoint), { signal: AbortSignal.timeout(30) }), { error: { code: 'unavailable' } });
});

test('rejects unsafe endpoints before making network requests', async () => {
  for (const endpoint of ['file:///tmp/config', 'http://user:password@localhost', 'http://localhost/?key=secret', 'http://localhost/#secret']) {
    assert.deepEqual(await read(request(endpoint)), { error: { code: 'invalid_response' } });
  }
});
