// Startup preference and editor history contracts without a backend or models.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const script = name => fs.readFileSync(path.join(__dirname, '../../crates/web-ui/web', name), 'utf8');

function preferences({saved = {}, dark = false, blocked = false} = {}) {
  const system = {matches: dark, addEventListener: (_, callback) => { system.changed = callback; }};
  const root = {dataset: {}, style: {}};
  const meta = {setAttribute: (_, value) => { meta.color = value; }};
  const context = vm.createContext({window: {}, navigator: {languages: ['zh-CN', 'en']},
    document: {documentElement: root, querySelector: () => meta}, matchMedia: () => system,
    localStorage: {getItem: key => { if (blocked) throw Error('blocked'); return saved[key]; },
      setItem: (key, value) => { if (blocked) throw Error('blocked'); saved[key] = value; }},
  });
  vm.runInContext(script('preferences.js'), context);
  return {prefs: context.window.potPreferences, root, system, meta, saved};
}

{
  const fixture = preferences({saved: {'pot-appearance': '1', 'pot-locale': 'en'}, dark: true});
  assert.equal(fixture.root.dataset.theme, 'light', 'saved light applies before first paint despite dark OS');
  assert.equal(fixture.root.lang, 'en');
  assert.equal(fixture.meta.color, '#fdfdfd');
  fixture.system.changed();
  assert.equal(fixture.root.dataset.theme, 'light');
  fixture.prefs.setAppearance(0);
  assert.equal(fixture.root.dataset.theme, 'dark');
  fixture.system.matches = false;
  fixture.system.changed();
  assert.equal(fixture.root.dataset.theme, 'light');
  fixture.prefs.setAppearance(2);
  fixture.prefs.setLocale('zh-CN');
  assert.equal(fixture.root.style.colorScheme, 'dark');
  assert.equal(fixture.saved['pot-locale'], 'zh-CN');
  assert.equal(preferences({saved: fixture.saved}).root.dataset.theme, 'dark', 'reload retains explicit mode');
}
{
  const fixture = preferences({blocked: true, dark: true});
  fixture.prefs.setLocale('en');
  fixture.prefs.setAppearance(1);
  assert.equal(fixture.root.dataset.theme, 'light', 'storage failures do not interrupt preference changes');
  assert.equal(preferences({saved: {'pot-appearance': 'invalid'}, dark: true}).root.dataset.theme, 'dark');
}

function editorHistory() {
  const listeners = new Map(), pending = [], events = [];
  const entries = [{page: 'settings', section: 1, offset: 42}];
  let index = 0;
  const history = {get state() { return entries[index]; },
    pushState(state) { entries.splice(++index); entries[index] = state; },
    go(delta) { pending.push(() => {
      index += delta;
      assert.ok(index >= 0 && index < entries.length);
      const event = {state: entries[index], stopImmediatePropagation() { this.stopped = true; }};
      listeners.get('popstate')(event);
      if (!event.stopped) events.push({type: 'route', state: event.state});
    }); },
  };
  const context = vm.createContext({window: {}, history, location: {href: 'http://localhost/?page=settings'},
    Date, Math, addEventListener: (type, callback) => listeners.set(type, callback),
    CustomEvent: class { constructor(type, init) { this.type = type; this.detail = init.detail; } },
    dispatchEvent: event => events.push(event),
  });
  vm.runInContext(script('editor-history.js'), context);
  return {api: context.window.potEditorHistory, history, entries, events,
    flush() { while (pending.length) pending.shift()(); }};
}
{
  const fixture = editorHistory();
  fixture.api.setDepth(1);
  fixture.api.setDepth(2);
  fixture.history.go(-1);
  fixture.flush();
  assert.equal(fixture.events.length, 1);
  assert.equal(fixture.events[0].type, 'pot-editor-back');
  assert.equal(fixture.events[0].detail, 1);
  assert.equal(fixture.history.state.potEditor.depth, 2, 'leave guard can decline without changing history');
  fixture.api.setDepth(1);
  fixture.flush();
  assert.equal(fixture.history.state.potEditor.depth, 1, 'confirmed nested return pops exactly one step');
  fixture.api.setDepth(0);
  fixture.flush();
  assert.deepEqual(fixture.history.state, {page: 'settings', section: 1, offset: 42});
  assert.equal(fixture.api.active, false);
  assert.equal(fixture.events.filter(event => event.type === 'route').length, 0, 'editor history restoration does not run ordinary route navigation');
  assert.ok(fixture.entries.every(entry => !('draft' in entry) && !('credentials' in entry)));
}
{
  const fixture = editorHistory();
  fixture.api.setDepth(3);
  fixture.history.go(-3);
  fixture.flush();
  assert.equal(fixture.events[0].detail, 3, 'multi-entry browser Back preserves the intended return depth');
  fixture.api.setDepth(0);
  fixture.flush();
  assert.equal(fixture.history.state.offset, 42);
}
{
  const fixture = editorHistory();
  fixture.api.setDepth(1);
  fixture.api.setDepth(0);
  fixture.flush();
  fixture.history.go(1);
  fixture.flush();
  fixture.api.setDepth(1);
  fixture.events.length = 0;
  fixture.history.go(-1);
  fixture.flush();
  assert.equal(fixture.events[0].type, 'pot-editor-back', 'a reopened editor still guards Back past a stale entry');
  assert.equal(fixture.events[0].detail, 1);
}
console.log('Web preference and editor history contracts passed');
