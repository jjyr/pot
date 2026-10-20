// Deterministic crash reporting contracts; browser smoke tests verify rendering.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../../web/src/errors.js'), 'utf8');

class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.dataset = {}; this.listeners = {}; this.hidden = false; this.textContent = ''; }
  append(...children) { this.children.push(...children); }
  setAttribute(name, value) { this[name] = value; }
  addEventListener(name, callback) { this.listeners[name] = callback; }
  contains(element) { return this === element || this.children.some(child => child.contains(element)); }
  querySelectorAll() { return this.children.flatMap(child => [...(child.dataset.errorText ? [child] : []), ...child.querySelectorAll()]); }
  find(tag) { return this.children.find(child => child.tag === tag) || this.children.map(child => child.find(tag)).find(Boolean); }
}

function fixture({storage = new Map(), blockedStorage = false, loading = false, copyFails = false} = {}) {
  let now = 0, sequence = 0, reloaded = false, copied;
  const timers = new Map(), listeners = {}, consoleErrors = [];
  const body = new Element('body');
  const document = {
    readyState: loading ? 'loading' : 'complete', body, activeElement: null,
    createElement: tag => new Element(tag),
    addEventListener: (name, callback) => { listeners[name] = callback; },
  };
  const context = vm.createContext({
    document, URL, URLSearchParams, Error, WeakSet,
    Date: class extends Date { constructor() { super(now); } static now() { return now; } },
    location: {search: '?page=settings&access_token=do-not-log', reload: () => { reloaded = true; }},
    sessionStorage: {
      getItem: key => { if (blockedStorage) throw Error('blocked'); return storage.get(key); },
      setItem: (key, value) => { if (blockedStorage) throw Error('quota'); storage.set(key, value); },
    },
    navigator: {clipboard: {writeText: async text => { if (copyFails) throw Error('denied'); copied = text; }}},
    console: {error: (...args) => consoleErrors.push(args)},
    setTimeout: (fn, delay) => { const id = ++sequence; timers.set(id, {fn, at: now + delay}); return id; },
    clearTimeout: id => timers.delete(id),
    addEventListener: (name, callback) => { listeners[name] = callback; },
  });
  context.window = context;
  vm.runInContext(source, context);
  const toast = () => body.children.find(child => child.id === 'pot-error-toast');
  const logs = () => JSON.parse(JSON.stringify(context.potErrors.logs()));
  const tick = delay => {
    now += delay;
    for (const [id, timer] of [...timers]) if (timer.at <= now) { timers.delete(id); timer.fn(); }
  };
  return {context, body, document, listeners, storage, consoleErrors, toast, logs, tick,
    launcher: () => body.children.find(child => child.id === 'pot-error-log'),
    reloaded: () => reloaded, copied: () => copied};
}

async function test() {
  const f = fixture();
  assert.equal(f.toast().hidden, true);
  assert.equal(f.launcher().hidden, true);
  const resizeMessages = [
    'ResizeObserver loop completed with undelivered notifications.',
    'ResizeObserver loop limit exceeded',
  ];
  for (const message of resizeMessages) {
    f.listeners.error({message, error: null, filename: 'https://example.test/', lineno: 0, colno: 0});
  }
  assert.equal(f.logs().length, 0, 'Deferred resize notifications are not application crashes');
  assert.equal(f.toast().hidden, true);
  assert.equal(f.launcher().hidden, true);
  assert.equal(f.consoleErrors.length, 0);
  assert.equal(f.storage.size, 0, 'Resize notifications do not enter the persisted crash log');

  const resizeErrors = fixture();
  for (const message of resizeMessages) {
    resizeErrors.listeners.error({message, error: Error(message)});
    resizeErrors.listeners.unhandledrejection({reason: Error(message)});
    resizeErrors.context.potReportPanic(message);
  }
  resizeErrors.listeners.error({message: 'ResizeObserver callback failed'});
  assert.equal(resizeErrors.logs().length, 7, 'Thrown errors, rejections, panics and other observer failures remain visible');
  assert.equal(resizeErrors.toast().hidden, false);
  const error = Error('<img src=x onerror=alert(1)>');
  f.listeners.error({message: error.message, error});
  assert.equal(f.toast().hidden, false);
  assert.ok(f.toast().find('pre').textContent.includes('<img src=x'));
  assert.equal(f.logs()[0].page, 'settings');
  assert.ok(!JSON.stringify(f.logs()).includes('do-not-log'));
  f.tick(7999); assert.equal(f.toast().hidden, false);
  f.listeners.error({message: error.message, error});
  assert.equal(f.logs().length, 1);
  assert.equal(f.logs()[0].count, 2);
  f.tick(1); assert.equal(f.toast().hidden, true, 'duplicates must not reset the eight-second timeout');
  f.listeners.error({message: error.message, error});
  assert.equal(f.toast().hidden, true, 'duplicate traps must not reopen a hidden toast');
  assert.equal(f.consoleErrors.length, 1);

  f.launcher().onclick();
  assert.equal(f.toast().find('details').open, true);
  f.tick(20000); assert.equal(f.toast().hidden, false);
  await f.toast().find('details').find('button').onclick();
  assert.ok(f.copied().includes(error.message));
  f.toast().find('div').children[0].onclick();
  assert.equal(f.reloaded(), true);
  f.toast().find('div').children[1].onclick();
  assert.equal(f.toast().hidden, true);

  const restored = fixture({storage: f.storage});
  assert.equal(restored.logs().length, 1);
  assert.equal(restored.toast().hidden, true, 'old errors should not replay as new crashes');
  assert.equal(restored.launcher().hidden, false);
  restored.listeners.error({message: error.message, error});
  assert.equal(restored.toast().hidden, false, 'a new occurrence after reload must still alert');

  const early = fixture({loading: true, blockedStorage: true, copyFails: true});
  early.context.potReportPanic('time not implemented on this platform');
  assert.equal(early.logs()[0].kind, 'panic');
  early.listeners.DOMContentLoaded();
  assert.equal(early.toast().hidden, false);
  early.context.potErrors.setTranslator(key => `translated:${key}`);
  assert.equal(early.toast().find('strong').textContent, 'translated:web.error.title');
  early.launcher().onclick();
  await early.toast().find('details').find('button').onclick();
  assert.ok(early.toast().find('span').textContent.includes('copy-failed'));
  early.listeners.unhandledrejection({reason: Error('unhandled fixture')});
  assert.equal(early.logs().at(-1).kind, 'unhandledrejection');

  const paused = fixture();
  paused.listeners.error({message: 'hover fixture'});
  paused.toast().listeners.mouseenter();
  paused.tick(10000); assert.equal(paused.toast().hidden, false);
  paused.toast().listeners.mouseleave();
  paused.tick(8000); assert.equal(paused.toast().hidden, true);
  paused.tick(2000);
  paused.listeners.error({message: 'focus fixture'});
  paused.document.activeElement = paused.toast().find('button');
  paused.toast().listeners.focusin();
  paused.tick(10000); assert.equal(paused.toast().hidden, false);
  paused.document.activeElement = null;
  paused.toast().listeners.focusout();
  paused.tick(0); paused.tick(8000); assert.equal(paused.toast().hidden, true);

  const bounded = fixture();
  for (let i = 0; i < 30; i++) bounded.listeners.error({message: `error ${i}`});
  assert.equal(bounded.logs().length, 20);
  bounded.context.potReportPanic('Bearer secret-value api_key=fixture-private-value sk-fixture https://user:password@example.test/app.js?access_token=fixture-private-value#secret');
  const serialized = JSON.stringify(bounded.logs().at(-1));
  for (const secret of ['secret-value', 'fixture-private-value', 'sk-fixture', 'password', '#secret']) assert.ok(!serialized.includes(secret), secret);
  console.log('Web errors: capture, panic bridge, timers, deduplication, copy, storage, limits and redaction passed');
}
test().catch(error => { console.error(error); process.exitCode = 1; });
