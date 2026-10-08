import repl from 'node:repl';
import { PassThrough } from 'node:stream';
import { inspect } from 'node:util';
import { operation, close } from './browser.mjs';

export function createPlugin({ mode, environment = "sandbox", emit }) {
  const vision = mode === 'mixed';
  let active, latest;
  let calls = [];
  let busy = false;
  const docs = `Computer-use API (one browser tab per REPL):
let tab = await cua.open("https://example.com");
let state = await tab.observe(); // {sequence, elements, title, url, ...}
await tab.fill({element: "8", sequence: state.sequence, text: "3"});
state = await tab.observe();
await tab.click({element: "9", sequence: state.sequence});
await tab.navigate("https://example.com");
await tab.press({element: "8", sequence: state.sequence, key: "Enter"});
await tab.scroll({sequence: state.sequence, delta_y: 500});
await tab.close();
Use cua.getTab(tabId) to retrieve the current tab; cua.getState() observes it.
Each browser operation automatically emits the fresh observation. Its return value is the parsed observation, so assign it to state. Element actions require the latest sequence. Variables (let/const/var) and top-level await persist across calls. Redeclaring a let/const binding fails; reuse or rename it.
nodeRepl.write(value) emits text; console.log does the same. Unreturned values are not automatically printed. JS errors preserve the REPL and any completed page effects. reset=true with the existing session_id ends the entire REPL; timeout or cancellation invalidates it.
${vision ? 'Mixed mode returns a viewport screenshot with observations. tab.click also accepts {x,y,sequence} using the 1280x800 viewport.' : 'Text mode returns AX observations only. Coordinates and image emission are unavailable. Canvas/image-only content requires a vision-capable model.'}
The REPL controls a separate headless Chromium in ${environment === "host" ? "Host" : "the Linux Sandbox"}. Native apps, existing browser profiles, full CDP access, arbitrary page evaluation, popups and multiple tabs are not exposed by this API.`;
  let rejectEvaluation;
  const r = repl.start({ input: new PassThrough(), output: new PassThrough(), terminal: false, prompt: '', useGlobal: false });
  // Node's default REPL evaluator reports runtime errors outside its eval callback.
  // Node 26 uses _handleError; earlier Node versions use the REPL error domain.
  r._handleError = error => { if (rejectEvaluation) rejectEvaluation(error); };
  if (r._domain) r._domain.on('error', error => { if (rejectEvaluation) rejectEvaluation(error); });
  const write = value => emit({ type: 'text', text: typeof value === 'string' ? value : inspect(value, { depth: 5, maxArrayLength: 300, maxStringLength: 32768 }) });
  async function perform(action, args = {}) {
    const allowed = {
      open: ['url'], navigate: ['url'], observe: [], close: [],
      click: vision ? ['element', 'sequence', 'x', 'y'] : ['element', 'sequence'],
      fill: ['element', 'sequence', 'text'], press: ['element', 'sequence', 'key'],
      scroll: ['sequence', 'delta_y'],
    }[action];
    if (action === 'open' && active) throw new Error('A tab is already open; reuse its handle or close it first');
    if (!args || typeof args !== 'object' || Object.keys(args).some(k => !allowed.includes(k))) throw new Error('Unsupported browser arguments for this model mode');
    if (args.text !== undefined && (typeof args.text !== 'string' || args.text.length > 16384)) throw new Error('Invalid fill text');
    if (action === 'fill' && typeof args.text !== 'string') throw new Error('Fill requires text');
    if (action === 'scroll' && (!Number.isInteger(args.delta_y) || Math.abs(args.delta_y) > 2000)) throw new Error('Invalid scroll distance');
    if (['click', 'fill', 'press'].includes(action) && args.x === undefined && typeof args.element !== 'string') throw new Error('Observed element required');
    if (['click', 'fill', 'press', 'scroll'].includes(action) && args.sequence !== latest?.sequence) throw new Error('Use the latest observation sequence');
    let result;
    try { result = await operation({ ...args, action, session_id: active, screenshot: vision, environment }); }
    catch (error) { if (action === 'open') { await close(); active = undefined; latest = undefined; } throw error; }
    if (action === 'close') { active = undefined; latest = undefined; write({ closed: true }); return { closed: true }; }
    active = result.session_id;
    for (const content of result.content) emit(content);
    latest = JSON.parse(result.content[0].text);
    return latest;
  }
  function run(action, args) {
    if (busy) throw new Error('Await each browser operation before starting another');
    busy = true;
    const promise = perform(action, args).finally(() => { busy = false; });
    promise.catch(() => {}); // Execute awaits every operation, including accidentally unawaited ones.
    calls.push(promise);
    return promise;
  }
  function tabHandle(id) {
    const call = (action, args) => {
      if (id !== active) throw new Error('Tab expired; explicitly open a new tab');
      return run(action, args);
    };
    return Object.freeze({
      id,
      observe: () => call('observe'), navigate: url => call('navigate', { url }),
      click: args => call('click', args), fill: args => call('fill', args),
      press: args => call('press', args), scroll: args => call('scroll', args), close: () => call('close'),
    });
  }
  const cua = Object.freeze({
    open: async url => { await run('open', url === undefined ? {} : { url }); return tabHandle(active); },
    getTab: id => { if (!active || id !== active) throw new Error('Unknown tab'); return tabHandle(id); },
    getState: () => active ? run('observe') : Promise.resolve({ tabs: [] }),
  });
  // Guest OS/cgroup isolation is authoritative; a JS context is not a security boundary.
  for (const key of ['process', 'require', 'module', 'exports']) delete r.context[key];
  Object.assign(r.context, { cua, nodeRepl: Object.freeze({ write }), console: Object.freeze({ log: (...args) => write(args.length === 1 ? args[0] : args) }) });
  let initialized = false;
  return {
    async execute({ tool, input }) {
      if (tool !== 'cua_repl') throw new Error('Unknown computer-use tool');
      if (!initialized) { write(docs); initialized = true; }
      calls = [];
      let failure;
      try {
        if (input.code) await new Promise((resolve, reject) => { rejectEvaluation = reject; r.eval(input.code + '\n', r.context, 'cua', error => error ? reject(error) : resolve()); });
      } catch (error) { failure = error; }
      finally { rejectEvaluation = undefined; }
      const outcomes = await Promise.allSettled(calls);
      if (failure) throw failure;
      const rejected = outcomes.find(result => result.status === 'rejected');
      if (rejected) throw rejected.reason;
      return {};
    },
    async dispose() { r.close(); await close(); },
  };
}
