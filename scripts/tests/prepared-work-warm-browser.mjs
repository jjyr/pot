import { promises as fs } from 'node:fs';
import path from 'node:path';
import { sourceServer, launchBrowser } from './browser-harness.mjs';
import { attachmentPickerFixture } from './attachment-picker-fixture.mjs';

const BASELINE = process.argv.includes('--baseline');
const captureDir = process.env.POT_CAPTURE_DIR || '/tmp/pot-work-warm-after';
await fs.mkdir(captureDir, { recursive: true });
const report = { ok: false, baseline: BASELINE, durations: {}, titleImages: 0, preparedSurfaces: 0, errors: [] };
let source, fixture, browser, context, pages = [], evidence;

const ready = async (page,owner) => {
  await page.waitForFunction((owner) => {
    const h = document.querySelector('.history[role="log"]:not([data-preparation-surface])');
    const busy = h?.querySelector('[aria-busy="true"]');
    const cs = getComputedStyle(h || document.body);
    const selected=document.querySelector('.assistant-row.is-selected');
    const image=h?.querySelector('.tool-title img');
    return !!h && cs.visibility === 'visible' && !busy && document.querySelectorAll('[data-preparation-surface]').length===0 && selected?.dataset.viewKey===JSON.stringify(['local',owner]) && h.textContent.includes('Synthetic finished') && image?.naturalWidth===128;
  },owner,{timeout:10000});
};
const shot = async (page, name) => { await page.screenshot({ path: path.join(captureDir, name), fullPage: true }); };
const goto = (origin, id) => `${origin}/?locale=en&theme=light&assistant_id=${id}&conversation=main`;

try {
  source = await sourceServer({ root: process.env.POT_SOURCE_ROOT });
  fixture = await attachmentPickerFixture(source.origin, { durableReceipts: true });
  browser = await launchBrowser();
  context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' });
  await context.addCookies([{ name: 'picker-case', value: 'work-avatar', url: fixture.origin }]);

  evidence = await context.newPage(); pages.push(evidence);
  const png = await evidence.evaluate(() => {
    const c = document.createElement('canvas'); c.width = c.height = 128;
    const g = c.getContext('2d'); g.fillStyle = '#2b6cb0'; g.fillRect(0, 0, 128, 128);
    g.fillStyle = '#fff'; g.font = 'bold 64px sans-serif'; g.fillText('A', 48, 90);
    return c.toDataURL('image/png');
  });
  const args = { to:{node_id: 'picker-control', kind: 'assistant', agent_id: 'beta'}, text:'Synthetic work note' }, result = { accepted: true, message_id: 'synthetic' };
  const tool = { id: 'tool1', role: 'tool', text: '', status: 'complete', tool: { id: 'turn', turn_id: 'turn', name: 'send_message', args, result, phase: 'completed', started_at_ms: 1000, finished_at_ms: 2000 } };
  const res = await context.request.post(`${fixture.origin}/fixture/control`, { data: { avatar: png, messages: [
    { id: 'input', request_id: 'turn', role: 'user', text: 'Synthetic work note', status: 'complete' },
    tool,
    { id: 'done', role: 'assistant', text: 'Synthetic finished', status: 'complete' },
  ] } });
  if (!res.ok()) throw new Error(`control failed: ${res.status()}`);

  const page = await context.newPage(); pages.push(page);
  page.on('pageerror', (e) => report.errors.push(String(e)));
  const t0 = performance.now();
  await page.goto(goto(fixture.origin, 'alpha'));
  await ready(page,'alpha');
  await page.waitForFunction(() => {
    const h = document.querySelector('.history[role="log"]:not([data-preparation-surface])');
    const im = h && h.querySelector('.tool-title img');
    return !!im && im.naturalWidth === 128;
  }, null, { timeout: 10000 });
  report.durations.coldAlphaMs = Math.round(performance.now() - t0);
  report.titleImages = await page.locator('.history[role="log"]:not([data-preparation-surface]) .tool-title img').count();
  if (report.titleImages < 1) throw new Error('expected .tool-title img');
  await shot(page, '01-initial-cold-alpha.png');

  const betaRow = page.locator('.assistant-row').filter({ hasText: 'Synthetic beta' });
  await betaRow.click();
  await ready(page,'beta');
  await page.waitForFunction(() => {
    const im = document.querySelector('.history[role="log"]:not([data-preparation-surface]) .tool-title img');
    return !!im && im.naturalWidth === 128;
  }, null, { timeout: 10000 });

  const warm = await context.newPage(); pages.push(warm);
  warm.on('pageerror', (e) => report.errors.push('warm: ' + String(e)));
  await warm.routeWebSocket('**/api/v1/ws', () => {});
  const tw0 = performance.now();
  await warm.goto(goto(fixture.origin, 'beta'));
  await ready(warm, 'beta');
  await warm.waitForFunction(() => {
    const h = document.querySelector('.history[role="log"]:not([data-preparation-surface])');
    const im = h && h.querySelector('.tool-title img');
    return !!im && im.naturalWidth === 128;
  }, null, { timeout: 10000 });
  report.durations.warmBetaMs = Math.round(performance.now() - tw0);

  const t1 = performance.now();
  await warm.locator('.assistant-row').filter({ hasText: 'Synthetic alpha' }).click();
  await ready(warm, 'alpha');
  await warm.waitForFunction(() => {
    const h = document.querySelector('.history[role="log"]:not([data-preparation-surface])');
    const im = h && h.querySelector('.tool-title img');
    return !!im && im.naturalWidth === 128;
  }, null, { timeout: 10000 });
  report.durations.switchAlphaMs = Math.round(performance.now() - t1);

  const status = await warm.locator('.connection-status.is-connected').count();
  if (status !== 0) throw new Error('unexpected connected transport');
  const body = await warm.locator('.history[role="log"]:not([data-preparation-surface])').innerText();
  if (!body.includes('Synthetic finished')) throw new Error('cached body missing Synthetic finished');
  report.titleImages = await warm.locator('.history[role="log"]:not([data-preparation-surface]) .tool-title img').count();
  report.preparedSurfaces = await warm.locator('[data-preparation-surface]').count();
  if (report.titleImages < 1) throw new Error('warm .tool-title img missing');

  const nodeRef = await warm.evaluateHandle(() => document.querySelector('.history[role="log"]:not([data-preparation-surface]) .tool-title img'));
  await warm.locator('.assistant-row').filter({ hasText: 'Synthetic alpha' }).click();
  await ready(warm, 'alpha');
  const preserved = await warm.evaluate((ref) => !!ref?.isConnected, nodeRef);
  if (!preserved) throw new Error('title img node replaced on repeat click');
  await shot(warm, '02-warm-alpha.png');

  const warmOk = report.durations.warmBetaMs < 1000;
  const switchOk = report.durations.switchAlphaMs < 1000;
  if (!BASELINE) {
    if (!warmOk) throw new Error(`warm beta >1000ms: ${report.durations.warmBetaMs}`);
    if (!switchOk) throw new Error(`switch alpha >1000ms: ${report.durations.switchAlphaMs}`);
  }
  if (report.errors.length) throw new Error(report.errors.join('; '));
  report.ok = true;
} catch (e) {
  report.errors.push(String(e?.stack || e));
  if (pages.at(-1)) { try { await pages.at(-1).screenshot({ path: path.join(captureDir, 'failure.png'), fullPage: true }); } catch {} }
  process.exitCode = 1;
} finally {
  try { await fs.writeFile(path.join(captureDir, 'report.json'), JSON.stringify(report, null, 2)); } catch {}
  console.log(JSON.stringify(report, null, 2));
  for (const p of pages) { try { await p.close(); } catch {} }
  try { if (context) await context.close(); } catch {}
  try { if (browser) await browser.close(); } catch {}
  try { if (fixture?.close) await fixture.close(); } catch {}
  try { if (source?.close) await source.close(); } catch {}
}
