// Playwright CLI run-code expression. Edits only unsaved isolated-fixture drafts.
async (page) => {
  const base = 'http://127.0.0.1:17908';
  const runtime = await (await page.request.get(base + '/api/v1/runtime')).json();
  if (runtime.runtime_id !== 'fixture') throw Error('Expected isolated UI fixture');
  const assert = (ok, message) => { if (!ok) throw Error(message); };
  const wait = () => page.waitForTimeout(250);
  const mirror = page.locator('textarea:not(#pot-composer)');
  const cdp = await page.context().newCDPSession(page);
  const errors = [], checks = [];
  const error = value => errors.push(value.message);
  page.on('pageerror', error);
  const writes = (await (await page.request.get(base + '/__fixture/writes')).json()).length;
  const compose = async name => {
    await page.keyboard.press('Meta+A');
    await page.keyboard.press('Backspace');
    await wait();
    await cdp.send('Input.dispatchKeyEvent', {type:'keyDown',key:'n',code:'KeyN',windowsVirtualKeyCode:229});
    await wait();
    assert(await mirror.inputValue() === '', name + ': initial IME key must not insert n');
    await cdp.send('Input.imeSetComposition', {text:'ni',selectionStart:2,selectionEnd:2});
    await cdp.send('Input.dispatchKeyEvent', {type:'keyDown',key:'Enter',code:'Enter',windowsVirtualKeyCode:229});
    await cdp.send('Input.insertText', {text:'你'});
    await wait();
    assert(await mirror.inputValue() === '你', name + ': composition must commit exactly once');
    assert(!await page.evaluate(() => window.potIsComposing()), name + ': composition must end');
    checks.push(name);
  };
  try {
    await cdp.send('Emulation.setTouchEmulationEnabled', {enabled:false});
    await page.setViewportSize({width:1440,height:900});
    await page.goto(base + '/?page=chat&assistant=main');
    await page.locator('#pot-composer').waitFor({state:'visible'});
    await page.evaluate(() => {
      sessionStorage.removeItem('pot-ui-errors');
      localStorage.setItem('pot-locale','zh-CN');
      localStorage.setItem('pot-appearance','1');
    });
    await page.reload();
    await page.locator('#pot-composer').waitFor({state:'visible'});
    await page.mouse.click(130,110);
    await compose('assistant-search');
    await page.screenshot({path:'output/playwright/audit-search-ime-fixed.png'});
    await page.keyboard.press('Meta+A');
    await page.keyboard.type('Research 123');
    await wait();
    assert(await mirror.inputValue() === 'Research 123', 'Ordinary letters, numbers and Select All must work');

    await page.setViewportSize({width:393,height:852});
    await page.goto(base + '/?page=chat&assistant=main');
    await page.locator('#pot-composer').waitFor({state:'visible'});
    await page.mouse.click(355,22); await wait();
    await page.mouse.click(250,74); await wait();
    assert(await page.locator('#pot-composer').isHidden(), 'Form must hide native chat editor');
    await page.mouse.click(100,118);
    await compose('assistant-name');
    await page.screenshot({path:'output/playwright/audit-name-ime-fixed.png'});
    await page.mouse.click(170,470); await wait();
    await page.mouse.click(90,560);
    await compose('assistant-instructions');
    await page.keyboard.press('Enter');
    await page.keyboard.type('second line');
    await wait();
    assert(await mirror.inputValue() === '你\nsecond line', 'Multiline Enter must remain a newline');
    await page.screenshot({path:'output/playwright/audit-rules-ime-fixed.png'});

    await page.setViewportSize({width:1280,height:900});
    await page.goto(base + '/?page=machines&assistant=main'); await wait();
    await page.mouse.click(1021,162); await wait();
    await page.mouse.click(670,230); await wait();
    await page.mouse.click(710,127);
    await compose('machine-tags');
    await page.keyboard.press('Meta+A');
    await page.keyboard.type('lin'); await wait();
    // Before a marked-text range exists, an IME Enter must not accept the
    // highlighted "linux" suggestion. Its browser default stays available.
    const defaultAllowed = await mirror.evaluate(element => element.dispatchEvent(new KeyboardEvent('keydown', {
      key:'Enter',code:'Enter',keyCode:229,bubbles:true,cancelable:true,
    })));
    await wait();
    assert(defaultAllowed && await mirror.inputValue() === 'lin', 'IME Enter must not select a tag or cancel browser default');
    await page.keyboard.press('Enter'); await wait();
    assert(await mirror.inputValue() === '', 'Ordinary Enter must still choose the tag');
    await page.screenshot({path:'output/playwright/audit-tags-ime-fixed.png'});

    // Password/token fields are real DOM inputs. Candidate confirmation must
    // not reach an Enter-to-submit handler, and must not cancel native editing.
    const token = await page.evaluate(() => {
      const input = document.querySelector('#access-token');
      const original = input.onkeydown;
      let delivered = 0;
      input.onkeydown = () => delivered++;
      const defaults = ['Enter','Escape','ArrowDown'].map(key => input.dispatchEvent(new KeyboardEvent('keydown', {
        key,keyCode:229,bubbles:true,cancelable:true,
      })));
      input.dispatchEvent(new KeyboardEvent('keydown',{key:'a',bubbles:true}));
      input.onkeydown = original;
      return {delivered,defaults};
    });
    assert(token.delivered === 1 && token.defaults.every(Boolean), 'Token IME keys must not submit or prevent native default');
    checks.push('token-composition-keys','ordinary-editing-and-tag-selection');
    const after = (await (await page.request.get(base + '/__fixture/writes')).json()).slice(writes);
    assert(!after.some(write => /\/(messages|definition|machine-connections|providers\/check)$/.test(write.path)), 'Audit must not save forms or call models');
    assert(errors.length === 0, errors.join('\n'));
    assert(!await page.locator('#pot-error-log:not([hidden])').count(), 'No runtime errors');
    return {checks,errors,savedForms:0,modelCalls:0};
  } finally {
    page.off('pageerror', error);
    await cdp.detach();
  }
}
