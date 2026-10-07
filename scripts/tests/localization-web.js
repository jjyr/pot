// Exercise the shipped external script and generated JSON, including asynchronous loading.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const catalogs = JSON.parse(fs.readFileSync(path.join(process.argv[2], 'locales.json'), 'utf8'));
async function run(options = {}) {
    const listeners = {};
    const label = {dataset: {potI18n: 'web.take-over'}, textContent: 'Take over'};
    const tooltip = {attributes: {'data-pot-title': 'web.more-actions'}, getAttribute(name) { return this.attributes[name]; }, setAttribute(name, value) { this.attributes[name] = value; }};
    const clipboard = {value: 'User clipboard 中文 {count}', scrollTop: 27};
    const context = {
        Intl, Event, URL,
        navigator: {languages: options.languages ?? ['fr-FR', 'zh-TW']},
        localStorage: {value: options.saved ?? null, getItem() { if(options.noStorage)throw Error('storage unavailable');return this.value; }, setItem(_, value) { this.value = value; }},
        fetch: async url => {assert.equal(url.href, 'https://pot.test/desktop/ticket/locales.json');return {ok: true, json: async () => catalogs};},
        document: {
            currentScript: {src: 'https://pot.test/desktop/ticket/i18n.js'},
            readyState: 'loading', documentElement: {},
            querySelectorAll(selector) { return selector === '[data-pot-i18n]' ? [label] : selector === '[data-pot-title]' ? [tooltip] : []; },
            addEventListener(name, callback) { listeners[name] = callback; },
        },
        dispatchEvent() {},
    };
    context.window = context;
    vm.createContext(context);
    vm.runInContext(fs.readFileSync(path.join(process.argv[2], 'i18n.js'), 'utf8'), context);
    if(options.earlyLocale)context.potSetLocale(options.earlyLocale);
    await new Promise(resolve => setImmediate(resolve));
    listeners.DOMContentLoaded();
    await context.potLocalizationReady;
    assert.equal(context.potLocale, options.expected ?? 'zh-CN');
    assert.equal(label.textContent, catalogs[context.potLocale]['web.take-over']);
    context.potSetLocale('en');
    assert.equal(label.textContent, 'Take over');
    assert.equal(tooltip.attributes.title, 'More actions');
    assert.equal(context.document.documentElement.lang, 'en');
    assert.equal(context.potNumber(1234.5, 1), '1,234.5');
    if(catalogs.en['web.configured-memory'])assert.equal(context.potF('web.configured-memory', {memory: '{memory} 中文'}), ' (configured: {memory} 中文 GiB)');
    assert.equal(clipboard.value, 'User clipboard 中文 {count}');
    assert.equal(clipboard.scrollTop, 27);
}
(async () => {
    await run({languages:['es-ES','zh-TW']});
    await run({languages: ['de-DE'], noStorage: true, expected: 'en'});
    await run({languages: ['zh-CN'], saved: 'en', expected: 'en'});
    await run({earlyLocale: 'en', expected: 'en'});
    if(catalogs.fr)await run({languages:['fr-CA'], expected:'fr'});
    console.log('External web localization: async startup, matching, saved preference, live DOM and literal parameters passed');
})().catch(error => {console.error(error);process.exitCode=1;});
