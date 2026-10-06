// Exercise the actual generated script without loading a desktop or connecting to a model.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const html = fs.readFileSync(process.argv[2], 'utf8');
const script = html.split('<!-- POT_LOCALIZATION_START -->')[1].split('<script>')[1].split('</script>')[0];
const listeners = {};
const label = {dataset: {potI18n: 'web.take-over'}, textContent: ''};
const tooltip = {attributes: {'data-pot-title': 'web.more-actions'}, getAttribute(name) { return this.attributes[name]; }, setAttribute(name, value) { this.attributes[name] = value; }};
const clipboard = {value: 'User clipboard 中文 {count}', scrollTop: 27};
const context = {
    Intl, Event,
    navigator: {languages: ['fr-FR', 'zh-TW']},
    localStorage: {value: null, getItem() { return this.value; }, setItem(_, value) { this.value = value; }},
    document: {
        documentElement: {},
        querySelectorAll(selector) { return selector === '[data-pot-i18n]' ? [label] : selector === '[data-pot-title]' ? [tooltip] : []; },
        addEventListener(name, callback) { listeners[name] = callback; },
    },
    dispatchEvent() {},
};
context.window = context;
vm.createContext(context);
vm.runInContext(script, context);
listeners.DOMContentLoaded();
assert.equal(context.potLocale, 'zh-CN');
assert.equal(label.textContent, '接管');
assert.equal(tooltip.attributes.title, '更多操作');
context.potSetLocale('en');
assert.equal(label.textContent, 'Take over');
assert.equal(tooltip.attributes.title, 'More actions');
assert.equal(context.document.documentElement.lang, 'en');
assert.equal(context.localStorage.value, 'en');
assert.equal(context.potF('web.uptime.one', {count: 1}), '1 minute');
assert.equal(context.potF('web.uptime.other', {count: 2}), '2 minutes');
assert.equal(context.potF('web.configured-memory', {memory: '{memory} 中文'}), ' (configured: {memory} 中文 GiB)');
assert.equal(context.potNumber(1234.5, 1), '1,234.5');
assert.equal(clipboard.value, 'User clipboard 中文 {count}');
assert.equal(clipboard.scrollTop, 27);
context.localStorage.getItem = () => { throw new Error('storage unavailable'); };
context.navigator.languages = ['de-DE'];
listeners.DOMContentLoaded();
assert.equal(context.potLocale, 'en');
console.log('Web localization: matching, live DOM labels, parameters, numbers and unavailable storage passed');
