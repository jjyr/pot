import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const current = 'pot-ui-v4', retired = 'retired-product-ui';
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
const installation=new Map();const displayed=[];const focused=[];const opened=[];
const context = vm.createContext({
 notificationInstallation:async work=>work({get:async key=>installation.get(key),put:(value,key)=>installation.set(key,value),remove:key=>installation.delete(key)}),
 validNotificationTarget:event=>event&&typeof event.id==='string'&&['space','control','owner'].every(key=>event[key])&&['reply','task-complete','task-failed','approval'].includes(event.kind),
  URL, Promise, caches,
  fetch: async () => { if (!network) throw Error('offline fixture'); return new Response(network.body, {status: network.status}); },
  self: {registration:{showNotification:async(title,options)=>displayed.push({title,options})},clients:{matchAll:async()=>[],openWindow:async url=>opened.push(url)},location: {origin: 'https://pot.test'}, addEventListener: (name, handler) => listeners.set(name, handler)},
});
vm.runInContext(fs.readFileSync(new URL('../../web/src/service-worker.js', import.meta.url), 'utf8').replace(/^import[^\n]*\n/,''), context);
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

const target={id:'reply',space:'node',control:'node',owner:'alpha',task:'task-one',kind:'reply'};
const payload={version:1,event:target,device:'device',title:'Pot · Alpha',body:'Synthetic reply'};
installation.set('device',{schema_version:1,id:'device',control:'node',enabled:true,channel:'push'});
async function dispatch(name,event){let pending;listeners.get(name)({...event,waitUntil:value=>pending=value});await pending}
await dispatch('push',{data:{json:()=>({...payload,device:'other'})}});assert.equal(displayed.length,0);
await dispatch('push',{data:{json:()=>payload}});assert.equal(displayed.length,1);assert.equal(displayed[0].options.tag,'reply');
const notification={data:{device:'device',event:{...target,url:'https://attacker.test/'}},close(){}};
await dispatch('notificationclick',{notification});assert.deepEqual(opened,['/'],'worker opens only its own fixed root');
assert.equal(installation.get('click').event.task,'task-one');
context.self.clients.matchAll=async()=>[{url:'https://pot.test/',focus:async()=>focused.push('focus'),postMessage:message=>focused.push(message.type)}];
await dispatch('notificationclick',{notification});assert.deepEqual(focused,['focus','pot:notification-click']);
installation.get('device').enabled=false;await dispatch('push',{data:{json:()=>payload}});assert.equal(displayed.length,1);
await dispatch('notificationclick',{notification});assert.equal(focused.length,2,'disabled installation cannot restore a target');
console.log('Push display, device fencing, fixed click destination and pending exact Task target passed');
