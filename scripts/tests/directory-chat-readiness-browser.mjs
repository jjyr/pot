import assert from 'node:assert/strict';
import { sourceServer, launchBrowser } from './browser-harness.mjs';
import { chatInteractionsFixture } from './chat-interactions-fixture.mjs';

const TIMEOUT = 15000;

async function waitFor(fn, { timeout = TIMEOUT, interval = 50, message } = {}) {
  const start = Date.now();
  let lastError;
  while (Date.now() - start < timeout) {
    try {
      const result = await fn();
      if (result) return result;
    } catch (err) {
      lastError = err;
    }
    await new Promise((r) => setTimeout(r, interval));
  }
  if (lastError) throw lastError;
  throw new Error(message || `waitFor timed out after ${timeout}ms`);
}

async function main() {
  const source = await sourceServer();
  const fixture = await chatInteractionsFixture(source.origin);
  let browser;
  let context;
  let page;
  const errors = [];

  try {
    browser = await launchBrowser();
    context = await browser.newContext({ serviceWorkers: 'block' });
    page = await context.newPage();
    page.on('pageerror', error => errors.push(error.message));

    // Test-only route to expose the live client on window.
    await page.route('**/components/ui.js', async (route) => {
      const response = await route.fetch();
      const body = await response.text();
      const needle =
        "export function mountApp(root, client, {surface = 'main'} = {}) {";
      assert.ok(
        body.includes(needle),
        'expected exact mountApp signature in components/ui.js'
      );
      const patched = body.replace(
        needle,
        `${needle}\n  window.__testClient=client;`
      );
      await route.fulfill({
        response,
        body: patched,
        headers: {
          ...response.headers(),
          'content-type': 'text/javascript; charset=utf-8',
        },
      });
    });

    // First navigation captures the initial live snapshot.
    await page.goto(`${fixture.origin}/?assistant_id=alpha&locale=en`, {
      waitUntil: 'domcontentloaded',
    });

    await waitFor(async () => {
      return page.evaluate(() => {
        const el = document.querySelector('.connection-status');
        return !!(el && el.classList.contains('is-connected')) && window.__testClient && typeof window.__testClient.operationScopeReady === 'function' && window.__testClient.operationScopeReady(window.__testClient.scope()) === true;
      });
    }, { message: 'connection-status never became is-connected (initial)' });

    // Phase A: install the directory query override AFTER the initial
    // committed live snapshot has landed in the UI.
    await page.evaluate(async () => {
      const mod = await import('/state/domains/index.mjs');
      const domains = mod.domainsFor(window.__testClient);
      const dir = domains && domains.directory;
      if (!dir || typeof dir.query !== 'function') {
        throw new Error('directory.query not available on domains');
      }
      const original = dir.query.bind(dir);
      window.__directoryOriginalQuery = original;
      dir.query = async (...args) => {
        const data = await original(...args);
        if (window.__directoryOffline) {
          return {
            ...data,
            assistants: (data.assistants || []).map((row) => ({
              ...row,
              offline: true,
              retained: true,
              capabilities: {
                version: 1,
                operations: ['chat.read'],
              },
            })),
          };
        }
        return data;
      };
      window.__directoryOffline = false;
    });

    // Baseline: operationScopeReady should be true while connected.
    const readyBefore = await page.evaluate(() => {
      const c = window.__testClient;
      return c.operationScopeReady(c.scope());
    });
    assert.equal(
      readyBefore,
      true,
      'operationScopeReady must be true before going offline'
    );

    const frameCountBefore = fixture.trace.filter(
      (e) => e.kind === 'sse-frame' && e.path === '/api/v1/events'
    ).length;

    // Phase B: go offline and trigger a directory refresh. The retained
    // offline assistant projection (chat.read only) must land in the live
    // UI and drive the connection status to offline.
    await page.evaluate(() => {
      window.__directoryOffline = true;
    });

    const offlineResp = await context.request.post(
      `${fixture.origin}/fixture/control`,
      { data: { directory: true } }
    );
    assert.equal(offlineResp.status(), 200, 'fixture control offline must succeed');

    await waitFor(async () => {
      return page.evaluate(() => {
        const el = document.querySelector('.connection-status');
        return !!(el && el.textContent.includes('Service unavailable'));
      });
    }, { message: 'connection-status never reached Service unavailable after offline directory' });

    const readyOffline = await page.evaluate(() => {
      const c = window.__testClient;
      return c.operationScopeReady(c.scope());
    });
    assert.equal(
      readyOffline,
      false,
      'operationScopeReady must be false when retained offline assistant projection is applied'
    );

    // Phase C: recover by restoring live directory on the wire (no chat
    // frame should be needed to recover gating).
    await page.evaluate(() => {
      window.__directoryOffline = false;
    });

    const onlineResp = await context.request.post(
      `${fixture.origin}/fixture/control`,
      { data: { directory: true } }
    );
    assert.equal(onlineResp.status(), 200, 'fixture control online must succeed');

    await waitFor(async () => {
      return page.evaluate(() => {
        const el = document.querySelector('.connection-status');
        return !!(el && el.classList.contains('is-connected')) && window.__testClient && typeof window.__testClient.operationScopeReady === 'function' && window.__testClient.operationScopeReady(window.__testClient.scope()) === true;
      });
    }, { message: 'connection-status never returned to is-connected after recovery' });

    const readyRecovered = await page.evaluate(() => {
      const c = window.__testClient;
      return c.operationScopeReady(c.scope());
    });
    assert.equal(
      readyRecovered,
      true,
      'operationScopeReady must return to true after live directory recovery'
    );

    // Quiet-stream invariant: the offline/online directory cycle must not
    // have produced any additional chat SSE frames.
    const frameCountAfter = fixture.trace.filter(
      (e) => e.kind === 'sse-frame' && e.path === '/api/v1/events'
    ).length;
    assert.equal(
      frameCountAfter,
      frameCountBefore,
      'directory offline/online cycle must not produce chat SSE frames'
    );

    // No errors during the cycle.
    assert.deepEqual(errors, [], 'no runtime errors expected during cycle');

    // No business POST to /api/v1/messages should have occurred.
    const messagePosts = (fixture.trace || []).filter(
      (e) =>
        e.kind === 'http-start' &&
        e.method === 'POST' &&
        e.path === '/api/v1/messages'
    );
    assert.equal(
      messagePosts.length,
      0,
      `unexpected business POST to /api/v1/messages: ${messagePosts.length}`
    );
    console.log('PASS directory offline projection cannot overwrite live chat readiness');
  } finally {
    try {
      if (context) await context.close();
    } catch {}
    try {
      if (browser) await browser.close();
    } catch {}
    try {
      if (fixture && typeof fixture.close === 'function') await fixture.close();
    } catch {}
    try {
      if (source && typeof source.close === 'function') await source.close();
    } catch {}
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
