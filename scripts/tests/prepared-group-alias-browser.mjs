import assert from 'node:assert/strict';
import { sourceServer, launchBrowser } from './browser-harness.mjs';
import { preparedChatFixture } from './prepared-chat-fixture.mjs';

const targetId = 'alpha-721';
const conversationId = 'alpha-conversation';
const historySel = '.history[role="log"]:not([data-preparation-surface])';

async function dismissNotNow(page) {
  const btn = page.getByRole('button', { name: /not now/i });
  if (await btn.first().isVisible().catch(() => false)) {
    await btn.first().click().catch(() => {});
  }
}

async function ensureGroupExpanded(page, workGroup) {
  const leader = page.locator(`${historySel} .work-record-group[data-work-group="${workGroup}"]`);
  const summary = leader.locator('button.work-record-summary');
  const expanded = await summary.getAttribute('aria-expanded');
  if (expanded !== 'true') {
    await summary.click();
    await page.waitForFunction(
      ({ historySel, workGroup }) =>
        document.querySelector(`${historySel} .work-record-group[data-work-group="${workGroup}"] button.work-record-summary`)?.getAttribute('aria-expanded') === 'true',
      { historySel, workGroup },
    );
  }
  assert.equal(await summary.getAttribute('aria-expanded'), 'true');
}

async function waitIdbAnchor(page) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    const found = await page.evaluate(async ({ conversationId, targetId }) => {
      const db = await new Promise((res, rej) => {
        const req = indexedDB.open('pot-chat-cache');
        req.onsuccess = () => res(req.result);
        req.onerror = () => rej(req.error);
      });
      const tx = db.transaction('views', 'readonly');
      const store = tx.objectStore('views');
      const all = await new Promise((res, rej) => {
        const r = store.getAll();
        r.onsuccess = () => res(r.result || []);
        r.onerror = () => rej(r.error);
      });
      db.close();
      for (const row of all) {
        if (row.conversation_id !== conversationId) continue;
        const pos = row.position;
        if (!pos) continue;
        if (pos.mode !== 'reading') continue;
        if (pos.position?.anchor?.id !== targetId) continue;
        return { found: true, mode: pos.mode, anchorId: pos.position.anchor.id };
      }
      return { found: false };
    }, { conversationId, targetId });
    if (found.found) {
      assert.equal(found.mode, 'reading');
      assert.equal(found.anchorId, targetId);
      return;
    }
    await page.waitForTimeout(150);
  }
  throw new Error('IDB anchor not persisted');
}

async function wheelToTop(page, historySel) {
  const measure = async () => {
    return await page.evaluate((sel) => {
      const h = document.querySelector(sel);
      if (!h) return null;
      const w = h.querySelector('.message-window[aria-busy]');
      if (w && w.getAttribute('aria-busy') === 'true') return { busy: true };
      const t = h.querySelector('article[data-message-id="alpha-721"]');
      if (!t) return null;
      const hr = h.getBoundingClientRect();
      const tr = t.getBoundingClientRect();
      return { busy: false, delta: Math.round(tr.top - hr.top) };
    }, historySel);
  };

  const settle = async () => {
    await page.evaluate(async () => {
      await new Promise((resolve) => {
        let remaining = 12;
        const tick = () => {
          if (--remaining <= 0) return resolve();
          requestAnimationFrame(tick);
        };
        requestAnimationFrame(tick);
      });
    });
  };

  const history = page.locator(historySel);
  if (!(await history.count())) {
    throw new Error(`wheelToTop: history element not found for selector "${historySel}"`);
  }

  const box = await history.boundingBox();
  if (!box) {
    throw new Error(`wheelToTop: history element has no bounding box for selector "${historySel}"`);
  }
  const x = Math.round(box.x + box.width - 2);
  const y = Math.round(box.y + box.height / 2);
  if (x < 0 || y < 0) {
    throw new Error(`wheelToTop: invalid hover point (${x},${y}) for selector "${historySel}"`);
  }

  await page.mouse.move(x, y);

  const maxIterations = 12;
  let lastDelta = null;
  let iterations = 0;

  for (; iterations < maxIterations; iterations++) {
    await settle();

    let state;
    let waited = 0;
    while (true) {
      state = await measure();
      if (state && state.busy === false) break;
      if (waited >= maxIterations) {
        throw new Error(
          `wheelToTop: history message window never became idle (aria-busy) after ${maxIterations} polls; ` +
          `selector="${historySel}", lastDelta=${lastDelta}`
        );
      }
      await settle();
      waited++;
    }

    if (!state || state.delta === undefined) {
      throw new Error(`wheelToTop: could not measure delta; selector="${historySel}", lastDelta=${lastDelta}`);
    }

    lastDelta = state.delta;
    if (Math.abs(state.delta + 2) <= 1) {
      return;
    }

    const step = Math.max(-2000, Math.min(2000, state.delta + 2));
    await page.mouse.wheel(0, step);
  }

  throw new Error(
    `wheelToTop: failed to reach target after ${maxIterations} bounded wheel iterations; ` +
    `selector="${historySel}", lastDelta=${lastDelta}`
  );
}

async function runScenario(viewportName, viewport) {
  const origin1 = await sourceServer();
  const fx = await preparedChatFixture(origin1.origin, { workRecords: true });
  const browser = await launchBrowser();
  const context = await browser.newContext({ viewport, isMobile: viewportName === 'phone', hasTouch: viewportName === 'phone', serviceWorkers: 'block' });
  await context.route('**/*', (route) => {
    const url = new URL(route.request().url());
    if (url.origin !== fx.origin) {
      return route.abort();
    }
    return route.continue();
  });
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  const pageErrors = [];
  page.on('pageerror', (e) => pageErrors.push(e));

  try {
    await page.request.post(fx.origin + '/fixture/control', {
      data: { scenario: 'prose', holdPreview: false },
    });
    await page.goto(fx.origin + '/?locale=en&theme=light&assistant_id=alpha');
    await dismissNotNow(page);
    await page.locator('.connection-status.is-connected').waitFor({ state: "attached", timeout: 15000 });
    await page.locator(historySel).first().waitFor();
    await page.locator(`${historySel} .message-reference-open`).first().click();
    await page.locator(`${historySel} article[data-message-id="${targetId}"]`).waitFor({ timeout: 15000 });

    const target = page.locator(`${historySel} article[data-message-id="${targetId}"]`);
    const workGroup = await target.getAttribute('data-work-group');
    assert.ok(workGroup, 'target should have data-work-group');

    const sharedParent = await page.evaluate(({ historySel, targetId, workGroup }) => {
      const h = document.querySelector(historySel);
      if (!h) return false;
      const t = h.querySelector(`article[data-message-id="${targetId}"]`);
      const leader = h.querySelector(`.work-record-group[data-work-group="${workGroup}"]`);
      if (!t || !leader) return false;
      return t === leader || t.parentElement === leader.parentElement;
    }, { historySel, targetId, workGroup });
    assert.equal(sharedParent, true, 'target and leader must share parent or be identical');

    const leader = page.locator(`${historySel} .work-record-group[data-work-group="${workGroup}"]`);
    await leader.waitFor();
    const summary = leader.locator('button.work-record-summary');
    assert.ok(await summary.count() > 0, 'summary button exists');
    assert.equal(await page.locator(`${historySel} .work-records`).count(), 0);
    assert.equal(await page.locator(`${historySel} .work-record-items`).count(), 0);

    await ensureGroupExpanded(page, workGroup);
    await wheelToTop(page, historySel);
    await waitIdbAnchor(page);

    const histCount = fx.trace.filter((e) => e.kind === 'history-response').length;

    await page.request.post(fx.origin + '/fixture/control', {
      data: { scenario: 'prose', holdEvents: 'alpha' },
    });
    await page.reload();
    await dismissNotNow(page);
    await page.locator(`${historySel} article[data-message-id="${targetId}"]`).waitFor({ timeout: 15000 });

    await page.locator('.history[role="log"]:not([data-preparation-surface])').first().waitFor();
    await page.waitForFunction(
      () => !document.querySelector('.message-window[aria-busy="true"]'),
      null,
      { timeout: 10000 },
    );
    await page.evaluate(() => new Promise((res) => {
      let n = 12;
      const step = () => (n-- <= 0 ? res() : requestAnimationFrame(step));
      requestAnimationFrame(step);
    }));

    const afterCount = fx.trace.filter((e) => e.kind === 'history-response').length;
    assert.equal(afterCount, histCount, 'no additional history read for warm cached anchor');

    const restored = await page.evaluate((sel) => {
      const h = document.querySelector(sel);
      const t = h?.querySelector(`article[data-message-id="alpha-721"]`);
      return { delta: Math.round(t.getBoundingClientRect().top - h.getBoundingClientRect().top) };
    }, historySel);
    assert.ok(Math.abs(restored.delta + 2) <= 3, `restored delta <=3px near -2, got ${restored.delta}`);

    const initial = await page.evaluate((historySel) => {
      const h = document.querySelector(historySel);
      const t = h?.querySelector(`article[data-message-id="alpha-721"]`);
      window.__node = t;
      window.__parent = t?.parentElement;
      return { tag: t?.tagName, parent: t?.parentElement?.className || '' };
    }, historySel);
    assert.ok(initial);

    const history = page.locator(historySel);
    await history.hover();
    await page.mouse.wheel(0, 5000);
    await page.waitForTimeout(400);
    await page.waitForFunction(
      () => !document.querySelector('.message-window[aria-busy="true"]'),
      null,
      { timeout: 10000 },
    );
    const sameAfterDown = await page.evaluate((historySel) => {
      const h = document.querySelector(historySel);
      const t = h?.querySelector(`article[data-message-id="alpha-721"]`);
      return !!t && t === window.__node && t.parentElement === window.__parent;
    }, historySel);
    assert.equal(sameAfterDown, true, 'same node+parent after scroll down');

    await wheelToTop(page, historySel);
    await page.waitForFunction(
      () => !document.querySelector('.message-window[aria-busy="true"]'),
      null,
      { timeout: 10000 },
    );
    await page.waitForTimeout(200);
    const sameAfterUp = await page.evaluate((historySel) => {
      const h = document.querySelector(historySel);
      const t = h?.querySelector(`article[data-message-id="alpha-721"]`);
      return {
        same: !!t && t === window.__node && t.parentElement === window.__parent,
        delta: Math.round(t.getBoundingClientRect().top - h.getBoundingClientRect().top),
      };
    }, historySel);
    assert.equal(sameAfterUp.same, true, 'same node+parent after scroll back up');
    assert.ok(Math.abs(sameAfterUp.delta + 2) <= 3, `after-up delta <=3px near -2, got ${sameAfterUp.delta}`);

    assert.deepEqual(pageErrors, [], `page errors: ${pageErrors.map((e) => e.message).join('; ')}`);
    console.log(JSON.stringify({ passed: viewportName, restored: restored.delta, sameAfterUp: sameAfterUp.delta }));
  } finally {
    await context.close();
    await fx.close();
    await origin1.close();
    await browser.close();
  }
}

await runScenario('desktop', { width: 1280, height: 900 });
await runScenario('phone', { width: 390, height: 852 });
