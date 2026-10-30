import assert from 'node:assert';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import { sourceServer, launchBrowser } from './browser-harness.mjs';
import { attachmentPickerFixture } from './attachment-picker-fixture.mjs';
import { waitForOutbox } from './outbox-persistence-browser.mjs';

const sourceRoot = process.env.POT_SOURCE_ROOT;
const captureDir = process.env.POT_CAPTURE_DIR || '/tmp/pot-unified-after';
await fs.mkdir(captureDir, { recursive: true });

const report = { ok: false, steps: [], errors: [] };
let source, fixture, browser, context, page, warm;

async function shot(name) {
  const file = path.join(captureDir, `${name}.png`);
  await page.screenshot({ path: file, fullPage: true });
  report.steps.push({ screenshot: file });
  return file;
}

async function control(payload) {
  const res = await context.request.post(`${fixture.origin}/fixture/control`, { data: payload });
  assert.ok(res.ok(), `control failed: ${res.status()}`);
  return res.json();
}

try {
  source = await sourceServer({ root: sourceRoot });
  fixture = await attachmentPickerFixture(source.origin, { durableReceipts: true });
  browser = await launchBrowser();
  context = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    serviceWorkers: 'block',
  });
  await context.addCookies([{ name: 'picker-case', value: 'unified', url: fixture.origin }]);
  const pageErrors = [];
  page = await context.newPage();
  page.on('pageerror', (err) => pageErrors.push(String(err)));

  const initial = [{ id: 'assistant1', role: 'assistant', text: 'Committed conversation', status: 'complete' }];
  await control({ messages: initial, holdSend: true });

  await page.goto(`${fixture.origin}/?locale=en&theme=light&assistant_id=alpha&conversation=main`);
  const composer = page.locator('#pot-composer');
  await composer.waitFor({ state: 'visible' });
  await page.waitForFunction(() => {
    const connected = document.querySelector('.connection-status.is-connected');
    const c = document.querySelector('#pot-composer');
    return !!connected && c && !c.disabled;
  });
  await composer.fill('Unified record pending message');
  await page.getByRole('button', { name: 'Send', exact: true }).click();

  const pendingState = await waitForOutbox(page, ([rows]) => rows.length === 1);
  const requestId = pendingState.rows[0].request.request_id;
  assert.ok(requestId, 'expected request_id');

  const articleSel = '.history:not([data-preparation-surface]) article.message';
  const sentArticle = page.locator(articleSel).filter({ hasText: 'Unified record pending message' });
  await sentArticle.waitFor({ state: 'visible' });
  const capturedNode = await page.evaluate((txt) => {
    const n = [...document.querySelectorAll('.history:not([data-preparation-surface]) article.message')].find((el) => el.textContent.includes(txt));
    window.__unifiedNodeRef = n;
    return { connected: !!n?.isConnected, text: n?.textContent || '' };
  }, 'Unified record pending message');
  assert.ok(capturedNode.connected, 'pending node not connected');
  await shot('01-pending');

  await control({ messages: [...initial, { id: 'incoming', role: 'assistant', text: 'Concurrent incoming message', status: 'complete' }], publish: true });
  const incomingArticle = page.locator(articleSel).filter({ hasText: 'Concurrent incoming message' });
  await incomingArticle.waitFor({ state: 'visible' });

  const domOrder1 = await page.evaluate((sel) => {
    return Array.from(document.querySelectorAll(sel)).map((n) => n.textContent || '');
  }, articleSel);
  const idxSent = domOrder1.findIndex((t) => t.includes('Unified record pending message'));
  const idxInc = domOrder1.findIndex((t) => t.includes('Concurrent incoming message'));
  assert.ok(idxSent >= 0 && idxInc >= 0 && idxSent < idxInc, 'submitted must precede incoming');
  const sameNode1 = await page.evaluate(() => !!window.__unifiedNodeRef?.isConnected);
  assert.ok(sameNode1, 'pending node replaced by incoming');

  await control({ holdSend: false, releaseSends: true });
  await waitForOutbox(page, ([rows]) => rows.length > 0 && rows[0].state === 'accepted');
  await page.waitForFunction(() => {
    const el = window.__unifiedNodeRef;
    if (!el) return false;
    const indicator = el.querySelector('.message-pending');
    if (!indicator) return true;
    if (indicator.hidden) return true;
    return getComputedStyle(indicator).display === 'none';
  }, null, { timeout: 5000 });

  const pendingVisible = await sentArticle.locator('.message-pending').isVisible().catch(() => false);
  assert.strictEqual(pendingVisible, false, 'pending indicator still visible');
  const sameNode2 = await page.evaluate(() => !!window.__unifiedNodeRef?.isConnected);
  assert.ok(sameNode2, 'node replaced after accept');
  const rowsAfterAccept = await page.evaluate(() => {
    const txt = 'Unified record pending message';
    return Array.from(document.querySelectorAll('.history article.message')).filter((n) => n.textContent?.includes(txt)).length;
  });
  assert.strictEqual(rowsAfterAccept, 1, 'duplicate submitted rows');
  await shot('02-accepted');

  await warmInit();
  await warm.routeWebSocket('**/api/v1/ws', () => {});
  await warm.goto(`${fixture.origin}/?locale=en&theme=light&assistant_id=alpha&conversation=main`);
  const warmArticle = warm.locator(articleSel).filter({ hasText: 'Unified record pending message' });
  await warmArticle.waitFor({ state: 'visible', timeout: 5000 });
  const warmConnectedCount = await warm.evaluate(() => document.querySelectorAll('.connection-status.is-connected').length);
  assert.strictEqual(warmConnectedCount, 0, 'warm transport should not appear connected');
  const warmShot = path.join(captureDir, '03-warm-reload.png');
  await warm.screenshot({ path: warmShot, fullPage: true });
  report.steps.push({ screenshot: warmShot });
  await warm.close();
  warm = null;

  await control({
    messages: [
      ...initial,
      { id: 'incoming', role: 'assistant', text: 'Concurrent incoming message', status: 'complete', publish: true },
      { id: requestId, request_id: requestId, role: 'user', text: 'Unified record pending message', status: 'complete' },
    ],
    publish: true,
  });
  await control({
    messages: [
      ...initial,
      { id: 'incoming', role: 'assistant', text: 'Concurrent incoming message', status: 'complete', publish: true },
      { id: requestId, request_id: requestId, role: 'user', text: 'Unified record pending message', status: 'complete' },
    ],
    publish: true,
  });

  await page.waitForFunction((rid) => {
    const el = window.__unifiedNodeRef;
    if (!el) return false;
    const attr = el.dataset?.messageId ?? el.dataset?.messageid;
    return attr === rid;
  }, requestId, { timeout: 5000 });

  const sameNode3 = await page.evaluate(() => !!window.__unifiedNodeRef?.isConnected);
  assert.ok(sameNode3, 'node replaced after materialization');
  const domOrder2 = await page.evaluate((sel) => Array.from(document.querySelectorAll(sel)).map((n) => n.textContent || ''), articleSel);
  const idxSent2 = domOrder2.findIndex((t) => t.includes('Unified record pending message'));
  const idxInc2 = domOrder2.findIndex((t) => t.includes('Concurrent incoming message'));
  assert.ok(idxSent2 >= 0 && idxInc2 >= 0 && idxSent2 < idxInc2, 'order changed after materialize');
  await shot('04-materialized');

  await page.getByRole('button', { name: 'Saved messages', exact: true }).click();
  const dialog = page.locator('.outbox-dialog');
  await dialog.waitFor({ state: 'visible' });
  const dialogArticle = dialog.locator('article').filter({ hasText: 'Unified record pending message' });
  await dialogArticle.waitFor({ state: 'visible' });
  const bodyText1 = await dialog.innerText();
  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  await dialog.waitFor({ state: 'hidden' });
  await page.getByRole('button', { name: 'Saved messages', exact: true }).click();
  await dialog.waitFor({ state: 'visible' });
  await dialog.locator('article').filter({ hasText: 'Unified record pending message' }).waitFor({ state: 'visible' });
  const bodyText2 = await dialog.innerText();
  assert.strictEqual(bodyText2, bodyText1, 'saved dialog body changed between opens');
  await dialog.getByRole('button', { name: 'Close', exact: true }).click();

  report.pageErrors = pageErrors;
  assert.deepEqual(pageErrors, []);
  assert.deepEqual(report.errors, []);
  report.ok = true;
  const reportPath = path.join(captureDir, 'report.json');
  await fs.writeFile(reportPath, JSON.stringify(report, null, 2));
  console.log(reportPath);
} catch (err) {
  report.errors.push(String(err?.stack || err));
  if (page) {
    try { await page.screenshot({ path: path.join(captureDir, 'failure.png'), fullPage: true }); } catch {}
  }
  const reportPath = path.join(captureDir, 'report.json');
  try { await fs.writeFile(reportPath, JSON.stringify(report, null, 2)); } catch {}
  console.error(err);
  process.exitCode = 1;
} finally {
  try { if (warm) await warm.close(); } catch {}
  try { if (context) await context.close(); } catch {}
  try { if (browser) await browser.close(); } catch {}
  try { if (fixture?.close) await fixture.close(); } catch {}
  try { if (source?.close) await source.close(); } catch {}
}

async function warmInit() {
  warm = await context.newPage();
  warm.on('pageerror', (e) => report.errors.push('warm: ' + String(e)));
}
