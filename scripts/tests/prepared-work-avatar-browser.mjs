import {sourceServer,launchBrowser} from './browser-harness.mjs';

const baseline = process.argv.includes('--baseline');
const log = (obj) => process.stdout.write(JSON.stringify(obj) + '\n');

const server = await sourceServer({});
let browser;

try {
  browser = await launchBrowser();
  for (const open of [false, true]) {
    const page = await browser.newPage();
    const errors=[];page.on('pageerror',error=>errors.push(error.message));
    try {
      await page.goto(server.origin + '/scripts/tests/fixtures/prepared-empty.html');
      const results = await page.evaluate(async ({openFlag,baselineFlag}) => {
        const cleanupFns = [];
        let container, holder;
        const out = {};
        try {
          const [{ createPreparedRender, withPreparedRender, settlePreparedTree }, { createToolCall }] = await Promise.all([
            import('/components/prepared-render.mjs'),
            import('/components/tool-call.mjs')
          ]);

          const canvas = document.createElement('canvas');
          canvas.width = canvas.height = 128;
          const paint = canvas.getContext('2d');
          paint.fillStyle = '#4188cc';
          paint.fillRect(0, 0, 128, 128);
          const png = canvas.toDataURL('image/png');

          const actor = { node_id: 'synthetic-node', kind: 'assistant', agent_id: 'actor' };
          const peer = { node_id: 'synthetic-node', kind: 'assistant', agent_id: 'peer' };

          const memory = new Map([['synthetic', { open: openFlag }]]);

          container = document.createElement('div');
          container.style.cssText = 'position:fixed;left:-10000px;top:0;width:600px;visibility:hidden';
          const row = document.createElement('article');
          row.dataset.messageId = 'synthetic';
          container.append(row);
          document.body.append(container);
          cleanupFns.push(() => container.remove());

          const context = createPreparedRender({});
          let layouts = 0;
          const decodeCalls = [];
          const titleNodes = new Set();

          const origDecode = HTMLImageElement.prototype.decode;
          HTMLImageElement.prototype.decode = function (...args) {
            decodeCalls.push(this);
            return origDecode.apply(this, args);
          };
          cleanupFns.push(() => { HTMLImageElement.prototype.decode = origDecode; });

          const message = {
            id: 'synthetic',
            role: 'tool',
            status: 'complete',
            text: '',
            workRecord: true,
            tool: {
              id: 'synthetic',
              turn_id: 'turn',
              name: 'send_message',
              args: { to: peer, text: 'Synthetic' },
              result: { accepted: true, message_id: 'synthetic' },
              phase: 'completed',
              started_at_ms: 1000,
              finished_at_ms: 2000
            }
          };

          const profile = { name: 'Synthetic peer', avatar: png };

          const call = withPreparedRender(context, 'synthetic', () => createToolCall({
            key: 'synthetic',
            memory,
            messageActor: () => actor,
            resolveMessageIdentity: () => ({ name: profile.name, avatar: profile.avatar })
          }));
          row.append(call.element);
          withPreparedRender(context, 'synthetic', () => call.update(message));

          const tStart = performance.now();
          const settled = settlePreparedTree(row, context, {
            layout: () => {
              layouts++;
              withPreparedRender(context, 'synthetic', () => call.update(message));
              row.querySelectorAll('.tool-title img').forEach((img) => titleNodes.add(img));
            }
          });
          const timer = setTimeout(() => settled.cancel(), 8000);
          cleanupFns.push(() => clearTimeout(timer));
          await settled.promise;
          clearTimeout(timer);
          const elapsedMs = performance.now() - tStart;

          const titleImages = [...row.querySelectorAll('.tool-title img')];

          const initialMetrics = {
            open: openFlag,
            elapsedMs,
            layouts,
            uniqueTitleNodes: titleNodes.size,
            decodeCalls: decodeCalls.length,
            titleImages: titleImages.length,
            naturalWidth: titleImages.map((i) => i.naturalWidth),
            complete: titleImages.map((i) => i.complete),
            fontsStatus: document.fonts ? document.fonts.status : 'unavailable'
          };

          if (baselineFlag) {
            out.initial = initialMetrics;
            return out;
          }

          // ---- fixed: baseline assertions ----
          if (!(initialMetrics.elapsedMs < 1000)) throw Error('elapsed should be <1000ms');
          if ((initialMetrics.uniqueTitleNodes) !== (1)) throw Error('uniqueTitleNodes should be 1');
          if ((initialMetrics.titleImages) !== (1)) throw Error('titleImages should be 1');
          if ((initialMetrics.naturalWidth[0]) !== (128)) throw Error('naturalWidth should be 128');
          if ((initialMetrics.complete[0]) !== (true)) throw Error('image should be complete');
          if ((document.fonts ? document.fonts.status : 'loaded') !== ('loaded')) throw Error('fonts should be loaded');

          // ---- narrow repeat regression (same context, same node) ----
          const titleImg = titleImages[0];
          let repeatSeenNode = null;
          withPreparedRender(context, 'synthetic', () => call.update(message));
          const repeatSettled = settlePreparedTree(row, context, {
            layout: () => {
              withPreparedRender(context, 'synthetic', () => call.update(message));
              const imgs = row.querySelectorAll('.tool-title img');
              if (imgs.length === 1) repeatSeenNode = imgs[0];
            }
          });
          const repeatTimer = setTimeout(() => repeatSettled.cancel(), 8000);
          cleanupFns.push(() => clearTimeout(repeatTimer));
          await repeatSettled.promise;
          clearTimeout(repeatTimer);
          if ((repeatSeenNode) !== (titleImg)) throw Error('repeat: same title node retained');

          // ---- profile regression (new context, hidden row, decodedWeakMap resets) ----
          const origSrc = titleImg ? titleImg.src : null;
          profile.name = 'Renamed peer';
          const canvas2 = document.createElement('canvas');
          canvas2.width = canvas2.height = 128;
          const paint2 = canvas2.getContext('2d');
          paint2.fillStyle = '#cc8841';
          paint2.fillRect(0, 0, 128, 128);
          profile.avatar = canvas2.toDataURL('image/png');

          const context2 = createPreparedRender();
          withPreparedRender(context2, 'synthetic', () => call.update(message));

          const t2Start = performance.now();
          let secondSeenNode = null;
          const settled2 = settlePreparedTree(row, context2, {
            layout: () => {
              withPreparedRender(context2, 'synthetic', () => call.update(message));
              const imgs = row.querySelectorAll('.tool-title img');
              if (imgs.length === 1) secondSeenNode = imgs[0];
            }
          });
          const timer2 = setTimeout(() => settled2.cancel(), 8000);
          cleanupFns.push(() => clearTimeout(timer2));
          await settled2.promise;
          clearTimeout(timer2);
          const secondElapsed = performance.now() - t2Start;

          if ((secondSeenNode) !== (titleImg)) throw Error('profile: same title node retained');
          if (!(titleImg && titleImg.src !== origSrc)) throw Error('profile: title img src should change');
          const titleText = row.querySelector('.tool-title')?.textContent || '';
          if (!(titleText.includes('Renamed peer'))) throw Error('profile: title text should update');
          if (!(secondElapsed < 1000)) throw Error('second elapsed <1000ms');

          // ---- visible layout regression ----
          const measuredWidth = row.getBoundingClientRect().width;
          const measuredHeight = row.getBoundingClientRect().height;

          container.style.cssText = '';
          holder = document.createElement('div');
          holder.style.cssText = 'position:relative;width:600px';
          holder.append(row);
          document.body.append(holder);
          cleanupFns.push(() => holder.remove());

          await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));

          const afterWidth = row.getBoundingClientRect().width;
          const afterHeight = row.getBoundingClientRect().height;

          if (!(Math.abs(afterWidth - measuredWidth) <= 1)) throw Error('width diff <=1px');
          if (!(Math.abs(afterHeight - measuredHeight) <= 1)) throw Error('height diff <=1px');

          out.initial = initialMetrics;
          out.repeat = { sameNode: repeatSeenNode === titleImg };
          out.profile = { secondElapsed, sameNode: secondSeenNode === titleImg, srcChanged: titleImg && titleImg.src !== origSrc, textUpdated: titleText.includes('Renamed peer') };
          out.visible = { measuredWidth, measuredHeight, afterWidth, afterHeight };

          return out;
        } finally {
          for (const fn of cleanupFns) try { fn(); } catch (e) {}
        }
      }, {openFlag:open,baselineFlag:baseline});

      if(errors.length)throw Error('Page errors: '+errors.join('; '));
      if (baseline) {
        log({ baseline: true, ...results });
      } else {
        log({ open, ...results });
      }
    } finally {
      try { await page.close(); } catch (_) {}
    }
  }
} finally {
  try { await browser?.close(); } catch (_) {}
  try { await server.close(); } catch (_) {}
}
