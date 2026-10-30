import assert from 'node:assert/strict';

// Retained DOM contract: all 100 loaded roots stay mounted, so scrolling past
// the committed viewport must reveal the destination immediately (no blank
// frame, no loading spinner, no re-prepare). Then cover asynchronous
// new-row prep (a single row) that must be delayed by the renderer's gate
// without disturbing the retained roots.
export async function preparedScrollLoading(browser, origin) {
  const results = [];
  for (const direction of [-1, 1]) {
    const page = await browser.newPage({ viewport: { width: direction < 0 ? 1280 : 390, height: 800 } });
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    try {
      await page.goto(origin + '/empty-prepared-fixture');
      await page.evaluate(async () => {
        const { createMessageWindow } = await import('/components/message-window.mjs');
        const sheet = document.createElement('link');
        sheet.rel = 'stylesheet';
        sheet.href = '/styles.css';
        document.head.append(sheet);
        await new Promise(resolve => (sheet.onload = resolve));
        window.scrollLocale = 'en';
        window.rangeGate = null;
        document.body.innerHTML =
          '<style>.history{height:360px;width:600px;max-width:100%;overflow:auto;overflow-anchor:none;padding:0;position:relative}.message{height:100px!important;margin:0!important;flex:none}</style>' +
          '<div class="history"><div class="messages"></div></div>';
        const scroller = document.querySelector('.history');
        const host = scroller.firstElementChild;
        const rows = Array.from({ length: 100 }, (_, i) => ({
          id: 'm' + i,
          viewKey: 'm' + i,
          text: 'Message ' + i,
          sequence: 100 + i,
        }));
        let renderCount = 0;
        const render = (message, old) => {
          renderCount++;
          const record = old || { row: document.createElement('article') };
          record.row.className = 'message';
          record.row.textContent = message.text;
          // Only the synthetic "new edge" rows arm the gate.
          if (rangeGate && window.gateRowIds && window.gateRowIds.has(message.id)) {
            const image = document.createElement('img');
            image.style.cssText = 'position:absolute;width:1px;height:1px';
            Object.defineProperty(image, 'complete', { get: () => false });
            image.decode = () => {
              scrollLoading.decodeCalls = (scrollLoading.decodeCalls || 0) + 1;
              return rangeGate;
            };
            record.row.append(image);
          }
          return record;
        };
        const list = createMessageWindow({
          scroller,
          host,
          locale: () => scrollLocale,
          render,
          dispose: record => record.row.remove(),
        });
        list.setMessages(rows);
        window.gateRowIds = new Set();
        const visible = () => {
          const viewport = scroller.getBoundingClientRect();
          return [...host.querySelectorAll('article')]
            .map(row => {
              const box = row.getBoundingClientRect();
              return {
                id: row.dataset.messageId,
                offset: box.top - viewport.top,
                visible: Math.min(box.bottom, viewport.bottom) - Math.max(box.top, viewport.top),
                held: row.hasAttribute('data-conversation-hold'),
              };
            })
            .filter(row => row.visible >= 24);
        };
        window.scrollLoading = {
          list,
          scroller,
          host,
          visible,
          rows,
          getRenderCount: () => renderCount,
        };
      });

      await page.waitForFunction(() => scrollLoading.list.prepared);

      // Initial restore to m40 at top=4000 (retained DOM contract).
      await page.evaluate(() =>
        scrollLoading.list.restore({
          mode: 'reading',
          position: { top: 4000, anchors: [{ key: 'message:m40', offset: 0 }], anchor: { id: 'm40', offset: 0 } },
        })
      );
      await page.waitForFunction(
        () => scrollLoading.list.prepared && Math.abs(scrollLoading.scroller.scrollTop - 4000) < 1
      );

      // --- Phase 1: retained-DOM scroll across committed destination. ---
      const scrollResult = await page.evaluate(async direction => {
        const { scroller, host, visible } = scrollLoading;

        // Capture original retained roots + render count + committed extent.
        const originalRoots = Array.from(host.querySelectorAll('article'));
        const originalIds = originalRoots.map(r => r.dataset.messageId);
        const originalCount = originalRoots.length;
        const renderCountBefore = scrollLoading.getRenderCount();
        const heightBefore = scroller.scrollHeight;

        // Establish a focus + selection anchor on a visible row that survives
        // the scroll (we focus whatever is visible right now).
        const firstVisible = originalRoots.find(r => {
          const box = r.getBoundingClientRect();
          const view = scroller.getBoundingClientRect();
          return Math.min(box.bottom, view.bottom) - Math.max(box.top, view.top) >= 24;
        });
        let focusedId = null;
        let focusSurvived = null;
        if (firstVisible) {
          firstVisible.tabIndex = -1;
          firstVisible.focus();
          focusedId = firstVisible.dataset.messageId;
        }

        const move = delta => {
          scroller.dispatchEvent(new WheelEvent('wheel', { deltaY: delta, bubbles: true }));
          scroller.scrollTop += delta;
          scroller.dispatchEvent(new Event('scroll'));
        };
        scrollLoading.move = move;

        const committed = visible();
        move(direction * 400);
        const lastVisible = visible();
        move(direction * 3000);
        // Give any (incorrect) virtualized swap two frames to settle.
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        const blank = visible();
        const spinner = document.querySelector('.conversation-range-loading');
        const afterRoots = Array.from(host.querySelectorAll('article'));
        const afterIds = afterRoots.map(r => r.dataset.messageId);
        const renderCountAfter = scrollLoading.getRenderCount();
        const heightAfter = scroller.scrollHeight;

        // identity check in page (never serialize element references to Node)
        const identityPreserved =
          afterRoots.length === originalCount &&
          afterRoots.every((node, i) => node === originalRoots[i]) &&
          afterIds.join('\u0000') === originalIds.join('\u0000');

        if (focusedId) {
          focusSurvived = document.activeElement === firstVisible;
        }

        // Post-frame identity check against stored original refs.
        const frameRoots = Array.from(host.querySelectorAll('article'));
        const frameIds = frameRoots.map(r => r.dataset.messageId);
        const frameIdentityPreserved =
          frameRoots.length === originalCount &&
          frameRoots.every((node, i) => node === originalRoots[i]) &&
          frameIds.join('\u0000') === originalIds.join('\u0000');
        const frameRenderCount = scrollLoading.getRenderCount();

        return {
          committed,
          lastVisible,
          blank,
          spinnerPresent: !!spinner,
          heightBefore,
          heightAfter,
          originalCount,
          renderCountBefore,
          renderCountAfter,
          identityPreserved,
          focusedId,
          focusSurvived,
          frameIdentityPreserved,
          frameRenderCount,
        };
      }, direction);

      // Core retained-DOM assertions.
      assert.notDeepEqual(
        scrollResult.lastVisible.map(r => r.id),
        scrollResult.committed.map(r => r.id),
        'wheel input moves beyond the last committed viewport'
      );
      assert(scrollResult.blank.length > 0, 'destination is visible immediately (non-empty)');
      assert.equal(
        scrollResult.spinnerPresent,
        false,
        'no .conversation-range-loading — destination is already mounted'
      );
      assert.equal(
        scrollResult.heightAfter,
        scrollResult.heightBefore,
        'scroll extent unchanged (no reprepare)'
      );
      assert.equal(scrollResult.originalCount, 100, '100 roots originally rendered');
      assert.equal(scrollResult.identityPreserved, true, '100 roots identical and in the same order');
      assert.equal(
        scrollResult.renderCountAfter,
        scrollResult.renderCountBefore,
        'render count unchanged — no rerender on retained scroll'
      );
      if (scrollResult.focusedId) {
        assert.equal(scrollResult.focusSurvived, true, 'focused row survives scroll');
      }
      assert.equal(scrollResult.frameIdentityPreserved, true, '100 roots identical and same order after frames');
      assert.equal(
        scrollResult.frameRenderCount,
        scrollResult.renderCountBefore,
        'render count unchanged after frames — no transient replacement'
      );

      // Return to the reading anchor so the async prep below starts from a
      // known position (m40 at top).
      await page.evaluate(() =>
        scrollLoading.list.restore({
          mode: 'reading',
          position: { top: 4000, anchors: [{ key: 'message:m40', offset: 0 }], anchor: { id: 'm40', offset: 0 } },
        })
      );
      await page.waitForFunction(
        () => scrollLoading.list.prepared && Math.abs(scrollLoading.scroller.scrollTop - 4000) < 1
      );

      // --- Phase 2: asynchronous new-row prep (single edge row) delayed by gate. ---
      const prepPlan = await page.evaluate(direction => {
        const { list, scroller, host, rows, visible } = scrollLoading;
        window.rangeGate = new Promise(resolve => (window.releaseRange = resolve));

        if (direction > 0) {
          const newRow = { id: 'new', viewKey: 'new', text: 'New message', sequence: 200 };
          window.expectedNewId = 'new';
          window.gateRowIds.add('new');
          window.pendingSetRows = rows.concat(newRow);
        } else {
          const newRow = { id: 'new', viewKey: 'new', text: 'New message', sequence: 99 };
          window.expectedNewId = 'new';
          window.gateRowIds.add('new');
          window.pendingSetRows = [newRow, ...rows];
        }

        // Snapshot current visible anchor before the async commit.
        const vis = visible();
        if (!vis.length) throw new Error('prepPlan: no visible anchor row');
        const snapshotAnchor = { id: vis[0].id, offset: vis[0].offset };

        // Capture original roots and extent.
        const originalRoots = Array.from(host.querySelectorAll('article'));
        const originalIds = originalRoots.map(r => r.dataset.messageId);
        const originalExtent = scroller.scrollHeight;

        scrollLoading.originalRoots = originalRoots;
        scrollLoading.originalIds = originalIds;

        // Establish focus/selection for preservation check.
        let focusId = null;
        let focusElement = null;
        const focusTarget = originalRoots.find(r => r.dataset.messageId === snapshotAnchor.id);
        if (focusTarget) {
          focusTarget.tabIndex = -1;
          focusTarget.focus();
          focusId = focusTarget.dataset.messageId;
          focusElement = focusTarget;
        }

        scrollLoading.decodeCalls = 0;
        scrollLoading.originalFocusElement = focusElement;

        // Kick off async setMessages (renderer will await img decode on 'new').
        list.setMessages(window.pendingSetRows);

        return {
          snapshotAnchor,
          originalExtent,
          originalCount: originalRoots.length,
          originalIds,
          focusId,
        };
      }, direction);

      // Wait for commit delay proven by prepared=false AND at least one decode call.
      await page.waitForFunction(() =>
        scrollLoading.list.prepared === false && (scrollLoading.decodeCalls || 0) > 0
      );

      const preRelease = await page.evaluate(() => {
        const { list, scroller, host } = scrollLoading;
        const rootsNow = Array.from(host.querySelectorAll('article'));
        const oldRoots = rootsNow.filter(r => r.dataset.messageId !== window.expectedNewId);
        const oldIdsPreserved =
          oldRoots.length === window.scrollLoading.originalRoots.length &&
          oldRoots.every((node, i) => node === window.scrollLoading.originalRoots[i]) &&
          oldRoots.map(r => r.dataset.messageId).join('\u0000') ===
            window.scrollLoading.originalIds.join('\u0000');
        return {
          prepared: list.prepared,
          busyPresent: !!document.querySelector('[aria-busy="true"]'),
          decodeCalls: scrollLoading.decodeCalls || 0,
          newMounted: rootsNow.some(r => r.dataset.messageId === window.expectedNewId),
          oldCount: oldRoots.length,
          oldIdsPreserved,
          extentNow: scroller.scrollHeight,
        };
      });

      // Old 100 must remain live and in the same order; extent must be unchanged;
      // the new row must not be mounted before the gate resolves.
      assert.equal(preRelease.prepared, false, 'prepared=false while gate held');
      assert(preRelease.decodeCalls > 0, 'decode gate was actually awaited');
      assert.equal(preRelease.newMounted, false, 'new row not mounted before gate resolves');
      assert.equal(preRelease.oldCount, 100, 'old 100 roots remain mounted during async prep');
      assert.equal(preRelease.oldIdsPreserved, true, 'old 100 roots retain identity and order');
      assert.equal(preRelease.extentNow, prepPlan.originalExtent, 'old extent remains during async prep');

      // Release the gate and finish.
      await page.evaluate(() => {
        window.releaseRange();
        window.rangeGate = null;
        window.gateRowIds.clear();
      });
      await page.waitForFunction(
        () => scrollLoading.list.prepared && !document.querySelector('.conversation-range-loading')
      );

      const postRelease = await page.evaluate(() => {
        const { host, visible, originalRoots: originals, originalFocusElement } = scrollLoading;
        const roots = Array.from(host.querySelectorAll('article'));
        const ids = roots.map(r => r.dataset.messageId);
        const retained = roots.filter(row => row.dataset.messageId !== 'new');
        const allOriginalPresent = originals.every(node => roots.includes(node));
        const originalOrderPreserved = retained.length === originals.length &&
          retained.every((node, index) => node === originals[index]);
        const newIndex = ids.indexOf('new');
        const firstIndex = ids.indexOf(originals[0] && originals[0].dataset.messageId);
        const lastIndex = ids.indexOf(originals[originals.length - 1] && originals[originals.length - 1].dataset.messageId);
        const edgeOk = newIndex === 0 || newIndex === ids.length - 1;
        const vis = visible();
        return {
          total: roots.length,
          allOriginalPresent,
          originalOrderPreserved,
          newIndex,
          firstIndex,
          lastIndex,
          edgeOk,
          visible: vis,
          spinnerPresent: !!document.querySelector('.conversation-range-loading'),
          focusedId: document.activeElement ? document.activeElement.dataset.messageId || null : null,
          sameFocus: document.activeElement === originalFocusElement,
          expectedNewId: window.expectedNewId,
        };
      });

      assert.equal(postRelease.total, 101, 'exactly 101 roots after async prep');
      assert.equal(postRelease.allOriginalPresent, true, 'all original 100 rows remain present');
      assert.equal(postRelease.originalOrderPreserved, true, 'original 100 rows keep relative order');
      assert.equal(postRelease.spinnerPresent, false, 'no spinner after retained prep');
      assert.equal(postRelease.edgeOk, true, 'new row is at the correct edge');
      if (direction > 0) {
        assert.equal(postRelease.newIndex, postRelease.lastIndex + 1, 'new row appended at tail');
      } else {
        assert.equal(postRelease.newIndex, postRelease.firstIndex - 1, 'new row prepended at head');
      }

      // Visible anchor offset preserved (prefix comp for prepend; scroll
      // unchanged for append). Match against the snapshot from kickoff.
      assert(prepPlan.snapshotAnchor);
      const anchorRow = postRelease.visible.find(r => r.id === prepPlan.snapshotAnchor.id);
      assert(anchorRow, 'Saved reading anchor remains visible after edge commit');
      assert(Math.abs(anchorRow.offset - prepPlan.snapshotAnchor.offset) <= 1);

      // Focus survives the async prep if it was established.
      assert(postRelease.sameFocus);
      assert.equal(postRelease.focusedId, prepPlan.focusId, 'focused node survives async prep');

      assert.deepEqual(errors, []);
      results.push({
        direction,
        retained: {
          destinationVisible: scrollResult.blank.length > 0,
          spinnerAbsent: !scrollResult.spinnerPresent,
          extentUnchanged: scrollResult.heightAfter === scrollResult.heightBefore,
          identityPreserved: scrollResult.identityPreserved,
          renderCountStable: scrollResult.renderCountAfter === scrollResult.renderCountBefore,
        },
        delayedCommit: {
          newMountedBeforeRelease: preRelease.newMounted,
          oldRootsStableDuringPrep: preRelease.oldIdsPreserved,
          extentStableDuringPrep: preRelease.extentNow === prepPlan.originalExtent,
          totalAfterRelease: postRelease.total,
          edgeOk: postRelease.edgeOk,
        },
      });

      await page.evaluate(() => scrollLoading.list.close());
    } finally {
      await page.evaluate(() => { window.releaseRange?.(); window.scrollLoading?.list.close(); }).catch(() => {});
      await page.close();
    }
  }
  return results;
}
