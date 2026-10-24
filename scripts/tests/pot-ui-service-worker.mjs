import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const current = 'pot-ui-v3', retired = 'retired-product-ui';
const entries = new Map([
  [retired, new Map([['/index.html', 'retired UI'], ['/app.js', 'retired JS']])],
  [current, new Map([['/index.html', 'current UI'], ['/app.js', 'current JS']])],
]);
const listeners = new Map(), reads = [], writes = [], pendingWrites = [];
const pathOf = value => new URL(typeof value === 'string' ? value : value.url, 'https://pot.test').pathname;
async function stored(name, input) {
  reads.push(name);
  const value = entries.get(name)?.get(pathOf(input));
  return value === undefined ? undefined : new Response(value);
}
const caches = {
  async open(name) {
    assert.equal(name, current, 'only the current UI cache may be opened');
    return {
      match: input => stored(name, input),
      put(input, response) {
        writes.push([name, pathOf(input)]);
        const done = response.text().then(value => entries.get(name).set(pathOf(input), value));
        pendingWrites.push(done);
        return done;
      },
    };
  },
  // Faithful CacheStorage lookup order: the pre-existing cache wins when code
  // accidentally searches the whole origin instead of its own namespace.
  async match(input) {
    for (const name of entries.keys()) {
      const found = await stored(name, input);
      if (found) return found;
    }
  },
};
let network;
const context = vm.createContext({
  URL, Promise, caches,
  fetch: async () => { if (!network) throw Error('offline fixture'); return new Response(network.body, {status: network.status}); },
  self: {location: {origin: 'https://pot.test'}, addEventListener: (name, handler) => listeners.set(name, handler)},
});
vm.runInContext(fs.readFileSync(new URL('../../web/src/service-worker.js', import.meta.url), 'utf8'), context);
async function request(path, mode = 'cors', method = 'GET') {
  let response;
  listeners.get('fetch')({request: {method, url: new URL(path, 'https://pot.test').href, mode}, respondWith(value) {response = value;}});
  return response;
}
for (const [path, mode, expected] of [['/index.html', 'navigate', 'current UI'], ['/app.js', 'cors', 'current JS']]) {
  assert.equal(await (await request(path, mode)).text(), expected);
  entries.get(current).delete(path);
  assert.equal(await request(path, mode), undefined, 'a miss must not fall back to a retired cache');
  entries.get(current).set(path, expected);
}
assert.ok(reads.every(name => name === current), 'retired/other caches must never be read');
for (const [path, mode, method] of [
  ['/api/v1/messages', 'cors', 'GET'], ['/access/token', 'navigate', 'GET'],
  ['/wails/runtime.js', 'cors', 'GET'], ['https://other.test/app.js', 'cors', 'GET'],
  ['/app.js', 'cors', 'POST'], ['/unrelated.txt', 'cors', 'GET'],
]) {
  const before = reads.length;
  assert.equal(await request(path, mode, method), undefined);
  assert.equal(reads.length, before, 'excluded traffic must not consult the UI cache');
}
network = {status: 200, body: 'fresh network JS'};
assert.equal(await (await request('/app.js')).text(), network.body, 'network remains preferred');
await Promise.all(pendingWrites);
assert.deepEqual(writes, [[current, '/app.js']]);
assert.equal(entries.get(current).get('/app.js'), network.body);
network = {status: 404, body: 'network missing'};
assert.equal(await (await request('/app.js')).text(), network.body, 'HTTP errors are not replaced by stale cache');
await Promise.resolve();
assert.equal(writes.length, 1, 'only successful responses may be cached');
assert.equal(entries.get(retired).get('/app.js'), 'retired JS', 'other cache data is untouched');
console.log('Current-cache offline boundaries, misses, excluded traffic and successful network writes passed');
