// Playwright CLI run-code expression. Only writes to the isolated UI fixture.
async (page) => {
  const base = 'http://127.0.0.1:17908';
  const runtime = await (await page.request.get(base + '/api/v1/runtime')).json();
  if (runtime.runtime_id !== 'fixture') throw Error('Expected isolated UI fixture');
  const assert = (ok, message) => { if (!ok) throw Error(message); };
  const wait = () => page.waitForTimeout(250);
  const errors = [], sends = [];
  const error = value => errors.push(value.message);
  const request = value => {
    if (value.method() === 'POST' && value.url().endsWith('/messages')) sends.push(value.postDataJSON());
  };
  page.on('pageerror', error);
  page.on('request', request);
  const cdp = await page.context().newCDPSession(page);
  const original = await (await page.request.get(base + '/api/v1/assistants/main/snapshot')).json();
  const input = page.locator('#pot-composer');
  const expectValue = value => page.waitForFunction(expected => document.querySelector('#pot-composer').value === expected, value);
  const reload = async () => {
    await page.goto(base + '/?page=chat&assistant=main');
    await input.waitFor({state: 'visible'});
    await wait();
  };
  try {
    await page.emulateMedia({reducedMotion: 'no-preference'});
    await cdp.send('Emulation.setTouchEmulationEnabled', {enabled: false});
    await page.setViewportSize({width: 1440, height: 900});
    await page.evaluate(() => {
      sessionStorage.removeItem('pot-ui-errors');
      localStorage.setItem('pot-locale', 'zh-CN');
      localStorage.setItem('pot-appearance', '1');
    });
    await reload();
    await input.focus();
    await page.screenshot({path: 'output/playwright/composer-after.png', caret: 'initial'});
    const desktop = await input.boundingBox();
    assert(desktop.height === 48, 'Desktop single-line height');

    // A real browser composition session, including the initial IME keydown.
    await cdp.send('Input.dispatchKeyEvent', {type: 'keyDown', key: 'n', code: 'KeyN', windowsVirtualKeyCode: 229});
    await cdp.send('Input.imeSetComposition', {text: 'ni', selectionStart: 2, selectionEnd: 2});
    const during = await page.evaluate(() => JSON.parse(window.potEditorSnapshot()));
    assert(during.composing && during.text === 'ni', 'IME preedit owns the first letter');
    await cdp.send('Input.dispatchKeyEvent', {type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 229});
    await cdp.send('Input.insertText', {text: '你'});
    await wait();
    assert(await input.inputValue() === '你' && sends.length === 0, 'Candidate confirmation must not send or duplicate the first letter');
    await input.press('Shift+Enter');
    await page.keyboard.type('second line');
    await wait();
    assert((await input.boundingBox()).height === 66 && sends.length === 0, 'Shift+Enter grows without sending');
    await input.fill('长文本用于检查自动换行和滚动。'.repeat(80));
    await wait();
    assert((await input.boundingBox()).height === 150, 'Long drafts cap at 150px');
    await input.fill('你好 /tag:desk');
    await wait();
    await input.press('Enter');
    await expectValue('你好 /tag:desktop ');
    assert(sends.length === 0, 'Completion must not send');
    await input.press('Meta+z');
    await expectValue('你好 /tag:desk');
    await input.fill('你');
    await input.press('Enter');
    await expectValue('');
    assert(sends.length === 1 && sends[0].text === '你', 'Ordinary Enter sends exactly once; receipt clears only that draft');
    await input.fill('草稿 A');
    await page.mouse.click(150, 263); // Research assistant in the fixture directory.
    await expectValue('');
    await input.fill('草稿 B');
    await page.mouse.click(150, 165); // Pot.
    await expectValue('草稿 A');
    await input.fill('');
    await wait();
    assert((await input.boundingBox()).height === 48, 'Deleting text shrinks the composer');
    await input.press('Meta+,');
    await input.waitFor({state:'hidden'});
    await page.waitForTimeout(500);
    const hiddenEditorWakes = await page.evaluate(() => new Promise(resolve => {
      const enqueue = window.queueMicrotask;
      let wakes = 0;
      window.queueMicrotask = callback => {
        if (new Error().stack.includes('/composer.js')) wakes++;
        enqueue(callback);
      };
      setTimeout(() => { window.queueMicrotask = enqueue; resolve(wakes); }, 300);
    }));
    assert(hiddenEditorWakes === 0, 'A hidden native editor must not schedule redraws');

    const layouts = [];
    for (const [width, height, touch, theme] of [[393,852,true,1], [393,460,true,2], [820,1180,true,2], [1440,900,false,2]]) {
      await cdp.send('Emulation.setTouchEmulationEnabled', {enabled: touch, maxTouchPoints: touch ? 5 : 1});
      await page.setViewportSize({width,height});
      await page.evaluate(value => localStorage.setItem('pot-appearance', String(value)), theme);
      await reload();
      const bounds = await input.boundingBox();
      assert(bounds.x >= 0 && bounds.y >= 44 && bounds.x + bounds.width <= width && bounds.y + bounds.height <= height - 23, 'Editor must stay inside the visible viewport');
      assert(bounds.height === (touch ? 60 : 48), 'Touch and desktop density');
      await input.fill('第一行\n第二行');
      await wait();
      await page.screenshot({path: `output/playwright/composer-${width}x${height}-${theme}.png`});
      if (width === 393 && height === 852) {
        await input.fill('');
        // Model a keyboard shrinking only VisualViewport, keeping the canvas
        // and layout viewport full-height as on mobile browsers.
        await page.evaluate(() => {
          Object.defineProperty(visualViewport, 'height', {configurable:true, get:()=>460});
          visualViewport.dispatchEvent(new Event('resize'));
        });
        await wait();
        const keyboardBounds = await input.boundingBox();
        assert(keyboardBounds.y === 376, 'Keyboard viewport must move the native editor with its GPUI frame');
        assert((await page.locator('canvas').boundingBox()).height === 852, 'Keyboard test must retain the layout viewport');
        await page.screenshot({path:'output/playwright/composer-keyboard-viewport.png', clip:{x:0,y:0,width:393,height:460}});
        await page.evaluate(() => { delete visualViewport.height; visualViewport.dispatchEvent(new Event('resize')); });
        await wait();
      }
      layouts.push({width,height,touch,theme,bounds});
    }

    await cdp.send('Emulation.setTouchEmulationEnabled', {enabled: false});
    await page.setViewportSize({width:1440,height:900});
    await page.evaluate(() => localStorage.setItem('pot-appearance', '1'));
    const user = {id:'activity-user', conversation_id:'main', role:'user', text:'帮我看一下这个问题', status:'streaming', created_at_ms:1, request_id:null};
    const snapshot = {...original, messages:[user], turn:{user_message_id:user.id,cancelling:false}};
    await page.request.post(base + '/__fixture/chat', {data:snapshot});
    await reload();
    await page.screenshot({path:'output/playwright/chat-thinking.png'});
    const clip = {x:478,y:80,width:760,height:360};
    const first = await page.screenshot({clip});
    await page.waitForTimeout(180);
    const second = await page.screenshot({clip});
    assert(!first.equals(second), 'Thinking dots should animate');
    const cadence = await page.evaluate(() => new Promise(resolve => {
      const times = [];
      const frame = time => { times.push(time); if (times.length < 61) requestAnimationFrame(frame); else resolve(times.slice(1).map((t,i) => t-times[i])); };
      requestAnimationFrame(frame);
    }));
    await page.emulateMedia({reducedMotion:'reduce'});
    await wait();
    const still = await page.screenshot({clip});
    await page.waitForTimeout(180);
    assert(still.equals(await page.screenshot({clip})), 'Reduced Motion must stop the dots');
    await page.emulateMedia({reducedMotion:'no-preference'});
    for (const stage of ['working','streaming','stopping','failed','complete']) {
      snapshot.turn = {user_message_id:user.id,cancelling:stage === 'stopping'};
      snapshot.messages = [user];
      if (stage === 'working') snapshot.messages.push({...user,id:'activity-tool',role:'tool',text:'read\nReading notes'});
      if (stage === 'streaming') snapshot.messages.push({...user,id:'activity-reply',role:'assistant',text:'正在检查输入框和中文输入体验…'});
      if (stage === 'complete') {
        snapshot.turn = null;
        snapshot.messages.push({...user,id:'activity-reply',role:'assistant',status:'complete',text:'输入框已检查完成。'});
      }
      if (stage === 'failed') {
        snapshot.turn = null;
        const detail = 'Control service rejected request (502 Bad Gateway): {"code":"provider_rejected","message":"Model provider returned HTTP 502"}';
        snapshot.messages = [{...user,status:'failed'}, {...user,id:'activity-error-reply',role:'assistant',status:'failed',text:detail}, {...user,id:'activity-error',role:'system',status:'failed',text:'任务未完成：'+detail}];
      }
      await page.request.post(base + '/__fixture/chat', {data:snapshot});
      await reload();
      await page.screenshot({path:`output/playwright/chat-${stage}.png`});
      if (stage === 'failed') {
        const collapsed = await page.screenshot({clip});
        await page.mouse.click(535,236);
        await wait();
        assert(!collapsed.equals(await page.screenshot({clip})), 'Failure details should expand');
        await page.screenshot({path:'output/playwright/chat-failed-details.png'});
      }
    }
    assert(errors.length === 0, 'Browser errors: ' + errors.join('\n'));
    assert(!await page.locator('#pot-error-log:not([hidden])').count(), 'No captured runtime errors');
    const sorted = [...cadence].sort((a,b)=>a-b);
    return {sends:sends.length, ime:during, hiddenEditorWakes, layouts, animationFrameMedianMs:sorted[30], animationFrameP95Ms:sorted[57], errors};
  } finally {
    await page.request.post(base + '/__fixture/chat', {data:original});
    page.off('pageerror',error);
    page.off('request',request);
    await cdp.detach();
  }
}
