// Playwright CLI run-code expression. Operates only on the disposable Go preview.
// Screenshots stay outside the repository; they are evidence, not a parity claim.
async page => {
  const base = 'http://127.0.0.1:17702';
  const initial = await (await page.request.get(base + '/api/preview/snapshot')).json();
  if (initial.runtime_id !== 'go-ui-preview' || !initial.boot_id || initial.turn_id === undefined) throw Error('Expected the isolated, current Go UI preview');
  const assert = (condition, message) => { if (!condition) throw Error(message); };
  const errors = [];
  const sends = [];
  const onError = error => errors.push(error.message);
  const onRequest = request => {
    if (request.method() === 'POST' && request.url().endsWith('/api/preview/messages')) sends.push(request.postDataJSON());
  };
  page.on('pageerror', onError);
  page.on('request', onRequest);
  const cdp = await page.context().newCDPSession(page);
  const input = page.locator('#pot-composer');
  const layouts = [];
  try {
    await cdp.send('Emulation.clearDeviceMetricsOverride');
    await cdp.send('Emulation.setTouchEmulationEnabled', {enabled: false});
    for (const [width, theme, locale, surface] of [
      [1440, 'light', 'en', 'main'], [900, 'dark', 'zh-CN', 'main'],
      [899, 'light', 'en', 'main'], [720, 'dark', 'en', 'main'],
      [719, 'light', 'zh-CN', 'main'], [390, 'dark', 'zh-CN', 'main'],
      [390, 'light', 'en', 'tray'],
    ]) {
      await page.setViewportSize({width, height: 852});
      await cdp.send('Emulation.setDeviceMetricsOverride',{width,height:852,deviceScaleFactor:2,mobile:false});
      await page.goto(`${base}/?theme=${theme}&locale=${locale}&surface=${surface}&conversation=preview-conversation`);
      await input.waitFor({state: 'visible'});
      await page.waitForFunction(() => document.querySelector('#pot-composer') && !document.querySelector('#pot-composer').disabled);
      const geometry = await page.evaluate(() => {
        const bounds = selector => {
          const element = document.querySelector(selector);
          const rect = element.getBoundingClientRect();
          return {x: rect.x, y: rect.y, width: rect.width, height: rect.height, display: getComputedStyle(element).display};
        };
        return {rail: bounds('.feature-rail'), pane: bounds('.assistant-directory'), titlebar: bounds('.titlebar'), composer: bounds('.composer'),
          overflow: document.documentElement.scrollWidth > innerWidth, historyOverflow: document.querySelector('.history').scrollWidth > document.querySelector('.history').clientWidth,
          inset: getComputedStyle(document.documentElement).getPropertyValue('--page-inset').trim()};
      });
      assert(!geometry.overflow && !geometry.historyOverflow, `No horizontal overflow at ${width}/${surface}`);
      assert(geometry.titlebar.height === 44, 'Shared titlebar remains 44px');
      assert(geometry.composer.x >= 0 && geometry.composer.x + geometry.composer.width <= width, 'Composer fits viewport');
      if (surface === 'main' && width >= 720) {
        assert(geometry.rail.width === 56 && geometry.pane.width === 220, 'Preserve rail and assistant pane geometry');
      } else assert(geometry.rail.display === 'none' && geometry.pane.display === 'none', 'Compact conversation hides navigation columns');
      assert(geometry.inset === (width < 900 ? '16px' : '32px') || surface === 'tray', 'Page spacing switches at 900px');
      await page.screenshot({path: `/private/tmp/pot-go-ui-${width}-${theme}-${locale}-${surface}.png`});
      layouts.push({width, theme, locale, surface, geometry});
    }
    // Fresh phone visits select the directory. Navigation preserves its search.
    await page.goto(`${base}/?theme=light&locale=en`);
    await page.getByRole('searchbox', {name: 'Search assistants'}).fill('pot');
    await page.locator('.assistant-row').click();
    await input.waitFor({state: 'visible'});
    await input.fill('draft survives narrow navigation');
    await page.getByRole('button', {name: 'Back to Assistants', exact: true}).click();
    assert(await page.getByRole('searchbox', {name: 'Search assistants'}).inputValue() === 'pot', 'Directory search survives Back');
    await page.locator('.assistant-row').click();
    assert(await input.inputValue() === 'draft survives narrow navigation', 'Conversation draft survives Back');

    await page.setViewportSize({width: 1440, height: 900});
    await cdp.send('Emulation.setDeviceMetricsOverride',{width:1440,height:900,deviceScaleFactor:2,mobile:false});
    await page.goto(`${base}/?theme=light&locale=en&conversation=preview-conversation`);
    await page.waitForFunction(() => document.querySelector('#pot-composer') && !document.querySelector('#pot-composer').disabled);
    await input.focus();
    await cdp.send('Input.dispatchKeyEvent', {type: 'keyDown', key: 'n', code: 'KeyN', windowsVirtualKeyCode: 229});
    await cdp.send('Input.imeSetComposition', {text: 'ni', selectionStart: 2, selectionEnd: 2});
    await cdp.send('Input.dispatchKeyEvent', {type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 229});
    await cdp.send('Input.insertText', {text: '你'});
    assert(await input.inputValue() === '你' && sends.length === 0, 'Real browser IME candidate confirmation must not send');
    await input.press('Shift+Enter');
    await page.keyboard.type('second line');
    assert((await input.inputValue()).includes('\n') && sends.length === 0, 'Shift+Enter preserves native multiline editing');
    await input.fill('保留草稿 / keep draft');
    await page.getByRole('button', {name: 'Preview preferences', exact: true}).click();
    await page.getByLabel('Appearance', {exact: true}).selectOption('dark');
    await page.getByRole('button', {name: 'Close', exact: true}).click();
    assert(await input.inputValue() === '保留草稿 / keep draft', 'Theme changes preserve the live DOM editor');
    const longDraft = '长文本用于检查自动换行和滚动。'.repeat(100);
    await input.fill(longDraft);
    assert((await input.boundingBox()).height === 150, 'Long drafts cap at 150px');
    await input.fill('');
    assert((await input.boundingBox()).height === 44, 'Deleting text shrinks desktop editor');

    // Delay only the acknowledgement to observe typing after submission. A late
    // accepted receipt must not erase text entered during the running preview.
    await page.route('**/api/preview/messages', async route => {
      const response = await route.fetch();
      await page.waitForTimeout(150);
      await route.fulfill({response});
    });
    await input.fill('Go UI browser verification');
    await page.waitForTimeout(100); // outside the IME composition-end guard.
    await input.press('Enter');
    await input.fill('typed while acknowledgement is pending');
    await page.waitForTimeout(300);
    assert(sends.length === 1 && sends[0].boot_id === initial.boot_id, 'Ordinary Enter sends one boot-scoped request');
    assert(await input.inputValue() === 'typed while acknowledgement is pending', 'Delayed acknowledgement preserves newer typing');
    const stop = page.getByRole('button', {name: 'Stop', exact: true});
    if (await stop.isVisible()) await stop.click();
    await page.waitForFunction(() => document.querySelector('.send-button').getAttribute('aria-label') === 'Send');
    await page.unroute('**/api/preview/messages');
    await page.screenshot({path: '/private/tmp/pot-go-ui-keyboard-after.png'});
    assert(errors.length === 0, 'Browser errors: ' + errors.join('\n'));
    return {layouts, sends: sends.length, errors};
  } finally {
    page.off('pageerror', onError); page.off('request', onRequest);
    await cdp.detach();
  }
}
