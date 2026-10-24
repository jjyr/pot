// Playwright CLI run-code expression. Operates only on the disposable Go preview.
// Screenshots stay outside the repository; they are evidence, not a parity claim.
async page => {
  const base = 'http://127.0.0.1:17702';
  const initial = await (await page.request.get(base + '/api/preview/snapshot')).json();
  if (initial.runtime_id !== 'pot-ui-preview' || !initial.boot_id || initial.turn_id === undefined) throw Error('Expected the isolated, current Pot UI preview');
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
      assert(geometry.titlebar.height === (width >= 720 && surface === 'main' ? 66 : 44), 'Wide columns own a66px header; compact headers retain44px');
      assert(geometry.composer.x >= 0 && geometry.composer.x + geometry.composer.width <= width, 'Composer fits viewport');
      if (surface === 'main' && width >= 720) {
        assert(geometry.rail.width === 56 && geometry.pane.width === 236, 'Preserve rail and assistant pane geometry');
      } else assert(geometry.rail.display === 'none' && geometry.pane.display === 'none', 'Compact conversation hides navigation columns');
      assert(geometry.inset === (width < 900 ? '16px' : '32px') || surface === 'tray', 'Page spacing switches at 900px');
      await page.screenshot({path: `/private/tmp/pot-ui-${width}-${theme}-${locale}-${surface}.png`});
      layouts.push({width, theme, locale, surface, geometry});
    }
    // Emulate hardware insets, not iOS chrome: the shell must own every edge.
    for (const [width,height,insets] of [
      [393,852,{top:59,bottom:34,left:0,right:0}],
      [852,393,{top:0,bottom:21,left:59,right:59}],
      [667,375,{top:0,bottom:21,left:44,right:44}],
    ]) {
      await cdp.send('Emulation.clearDeviceMetricsOverride');
      await page.setViewportSize({width,height});
      await cdp.send('Emulation.setSafeAreaInsetsOverride',{insets});
      await page.goto(`${base}/?theme=light&locale=en&conversation=preview-conversation`);
      await input.waitFor({state:'visible'});
      const geometry=await page.evaluate(()=>{
        const rect=selector=>document.querySelector(selector).getBoundingClientRect().toJSON();
        return {titlebar:rect('.titlebar'),composer:rect('.composer-area'),rail:rect('.feature-rail'),padding:getComputedStyle(document.querySelector('.composer-area')).paddingBottom};
      });
      assert(geometry.titlebar.y===insets.top&&geometry.titlebar.height===(width>=720?66:44),'Titlebar sits below the top safe area');
      assert(geometry.titlebar.x>=insets.left&&geometry.titlebar.right<=width-insets.right,'Titlebar avoids landscape cutouts');
      assert(geometry.composer.bottom===height-insets.bottom,'Shell reserves the bottom safe area once');
      assert(geometry.composer.x>=insets.left&&geometry.composer.right<=width-insets.right,'Composer avoids side cutouts');
      assert(geometry.padding==='24px','Composer does not add another hardware inset');
      if(width<720){
        await page.getByRole('button',{name:'Back to Assistants',exact:true}).click();
        await page.getByRole('button',{name:'Open navigation',exact:true}).click();
        const menu=await page.locator('.mobile-menu-heading').boundingBox();
        assert(menu.y===insets.top&&menu.x>=insets.left&&menu.x+menu.width<=width-insets.right,'Full-screen menu controls remain in the safe area');
        if(width===393){
          await page.evaluate(()=>{Object.defineProperty(window.visualViewport,'height',{configurable:true,value:260});window.visualViewport.dispatchEvent(new Event('resize'));});
          const last=page.locator('.mobile-menu .menu-destination').last();await last.scrollIntoViewIfNeeded();
          const destination=await last.boundingBox();
          assert(destination.y>=insets.top&&destination.y+destination.height<=260-insets.bottom,'Short menu scrolls its controls within the safe area');
          await page.evaluate(()=>{delete window.visualViewport.height;window.visualViewport.dispatchEvent(new Event('resize'));});
        }
        await page.getByRole('button',{name:'Close',exact:true}).click();
        await page.locator('.assistant-row:visible').first().click();
      }
      await page.getByRole('button',{name:'Preview preferences',exact:true}).click();
      const dialog=await page.locator('.preferences').boundingBox();
      assert(dialog.x>=insets.left&&dialog.x+dialog.width<=width-insets.right&&dialog.y>=insets.top&&dialog.y+dialog.height<=height-insets.bottom,'Dialogs fit the safe visible frame');
      await page.getByRole('button',{name:'Close',exact:true}).click();
    }
    await cdp.send('Emulation.setSafeAreaInsetsOverride',{insets:{top:0,bottom:0,left:0,right:0}});
    await page.setViewportSize({width:393,height:852});
    await page.goto(`${base}/?theme=light&locale=en&conversation=preview-conversation`);
    await input.waitFor({state:'visible'});
    await input.fill('Draft survives keyboard panning');
    await page.evaluate(()=>{
      const viewport=window.visualViewport;
      Object.defineProperties(viewport,{height:{configurable:true,value:420},offsetTop:{configurable:true,value:80}});
      viewport.dispatchEvent(new Event('resize'));
      Object.defineProperty(viewport,'offsetTop',{configurable:true,value:100});viewport.dispatchEvent(new Event('scroll'));
    });
    const keyboard=await page.locator('#app').boundingBox();
    assert(keyboard.y===100&&keyboard.height===420,'Shell follows keyboard resize and scroll offsets');
    assert(await input.inputValue()==='Draft survives keyboard panning','Viewport changes preserve the composer draft');
    await page.evaluate(()=>{
      const viewport=window.visualViewport;
      Object.defineProperties(viewport,{scale:{configurable:true,value:2},height:{configurable:true,value:210}});viewport.dispatchEvent(new Event('resize'));
    });
    assert((await page.locator('#app').boundingBox()).height===420,'Pinch zoom does not shrink and reflow the shell');
    await page.evaluate(()=>{for(const key of ['scale','height','offsetTop'])delete window.visualViewport[key];window.visualViewport.dispatchEvent(new Event('resize'));});
    // Native focus can attempt to scroll the outer document despite hidden
    // overflow. Keep that frame locked while inner history/textarea still scroll.
    await page.evaluate(()=>{
      const column=document.querySelector('.message-column');
      for(let index=0;index<30;index++){const row=document.createElement('p');row.textContent=`Focus history fixture ${index}`;column.append(row);}
    });
    const focusDraft='Long mobile draft keeps native selection and scrolling. '.repeat(30);
    await input.fill(focusDraft);await input.evaluate(node=>node.blur());
    await page.locator('.history').evaluate(node=>{node.scrollTop=120;node.dispatchEvent(new Event('scroll'));});
    await cdp.send('Emulation.setTouchEmulationEnabled',{enabled:true});
    const touchRect=await input.boundingBox();
    await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:touchRect.x+touchRect.width/2,y:touchRect.y+touchRect.height/2}]});
    await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
    const focusState=await page.evaluate(()=>{
      const input=document.querySelector('#pot-composer');input.setSelectionRange(50,60);input.scrollTop=40;
      const probe=document.createElement('div');probe.id='focus-scroll-probe';probe.style.cssText='position:absolute;top:1400px;height:1px;width:1px;pointer-events:none';document.body.append(probe);
      window.scrollTo(0,160);
      window.focusHeightWrites=0;window.focusObserver=new MutationObserver(records=>window.focusHeightWrites+=records.filter(row=>row.attributeName==='style').length);window.focusObserver.observe(input,{attributes:true});
      return {rootScroll:scrollY,history:document.querySelector('.history').scrollTop,inputScroll:input.scrollTop};
    });
    assert(focusState.rootScroll===0,'Focusing an input cannot scroll the outer browser document');
    for(const [height,top]of [[420,80],[420,110],[450,50],[852,0]]){
      await page.evaluate(({height,top})=>{
        Object.defineProperties(window.visualViewport,{height:{configurable:true,value:height},offsetTop:{configurable:true,value:top}});
        window.visualViewport.dispatchEvent(new Event('resize'));window.visualViewport.dispatchEvent(new Event('scroll'));
      },{height,top});
      const state=await input.evaluate(node=>({top:node.getBoundingClientRect().top,bottom:node.getBoundingClientRect().bottom,focused:document.activeElement===node,start:node.selectionStart,end:node.selectionEnd,scroll:node.scrollTop}));
      assert(state.top>top+44&&state.bottom<=top+height,'Focused composer stays below the titlebar and above the keyboard');
      assert(state.focused&&state.start===50&&state.end===60,'Keyboard transitions retain native focus and selection');
      assert(state.scroll===focusState.inputScroll,'Keyboard panning retains long-draft reading position');
      assert(await page.locator('.history').evaluate(node=>node.scrollTop)===focusState.history,'Keyboard transitions preserve history reading position');
    }
    assert(await page.evaluate(()=>window.focusHeightWrites)===0,'Keyboard transitions never rewrite the focused textarea height');
    assert(await input.inputValue()===focusDraft,'Keyboard dismissal retains the draft');
    await page.evaluate(()=>{window.focusObserver.disconnect();document.querySelector('#focus-scroll-probe').remove();for(const key of ['height','offsetTop'])delete window.visualViewport[key];window.visualViewport.dispatchEvent(new Event('resize'));});
    await cdp.send('Emulation.setTouchEmulationEnabled',{enabled:false});
    // Native surfaces do not inherit browser cutout padding.
    await cdp.send('Emulation.setSafeAreaInsetsOverride',{insets:{top:59,bottom:34,left:0,right:0}});
    await page.evaluate(()=>document.documentElement.dataset.platform='desktop');
    assert((await page.locator('.titlebar').boundingBox()).y===0,'Native host keeps its own window geometry');
    await page.evaluate(()=>document.documentElement.dataset.platform='browser');
    await cdp.send('Emulation.setSafeAreaInsetsOverride',{insets:{top:0,bottom:0,left:0,right:0}});
    const accessContext=await page.context().browser().newContext({viewport:{width:393,height:852},locale:'en'});
    try {
      // Exercise the connection form without credentials or product data.
      await accessContext.route('**/access/status',route=>route.fulfill({json:{authenticated:false}}));
      const access=await accessContext.newPage(),accessCDP=await accessContext.newCDPSession(access);
      await accessCDP.send('Emulation.setSafeAreaInsetsOverride',{insets:{top:59,bottom:34,left:0,right:0}});
      await access.goto(base);await access.getByLabel('Access token',{exact:true}).waitFor();
      const form=await access.locator('.access-setup form').boundingBox();
      assert(form.y>=59&&form.y+form.height<=818,'Connection form fits the portrait safe area');
      await access.evaluate(()=>{
        Object.defineProperties(window.visualViewport,{height:{configurable:true,value:280},offsetTop:{configurable:true,value:100}});
        window.visualViewport.dispatchEvent(new Event('resize'));
      });
      assert(await access.locator('.access-setup form').evaluate(node=>node.scrollHeight>node.clientHeight),'Connection form scrolls inside the safe area in a short keyboard viewport');
      const connect=access.getByRole('button',{name:'Connect',exact:true});await connect.scrollIntoViewIfNeeded();
      const control=await connect.boundingBox();
      assert(control.y>=159&&control.y+control.height<=346,'Connect stays reachable above the keyboard and bottom inset');
    } finally {await accessContext.close();}
    // Wide Markdown must scroll inside its code/table, never widen history.
    const wideMarkdown = [
      '长消息应当在聊天窗口内自动换行。'.repeat(12),
      'https://example.com/' + 'long-path-segment'.repeat(30),
      '```js\nconst value = "' + 'wide code '.repeat(50) + '";\n```',
      '| ' + Array.from({length: 32}, (_, i) => 'Column ' + i).join(' | ') + ' |\n| ' + Array(32).fill('---').join(' | ') + ' |\n| ' + Array(32).fill('Table content').join(' | ') + ' |',
    ].join('\n\n');
    const wideSnapshot = {...initial, messages: [
      {id: 'wide-user', role: 'user', text: wideMarkdown, status: 'complete'},
      {id: 'wide-assistant', role: 'assistant', text: wideMarkdown, status: 'complete'},
    ]};
    const wideRoute = route => route.fulfill({json: wideSnapshot});
    const eventsRoute = route => route.fulfill({contentType: 'text/event-stream', body: ': layout fixture\n\n'});
    await page.route('**/api/preview/snapshot', wideRoute);
    await page.route('**/api/preview/events', eventsRoute);
    try {
      for (const [width, surface] of [[393, 'main'], [390, 'main'], [719, 'main'], [720, 'main'], [900, 'main'], [1440, 'main'], [390, 'tray']]) {
        await page.setViewportSize({width, height: 852});
        await cdp.send('Emulation.setDeviceMetricsOverride', {width, height: 852, deviceScaleFactor: 2, mobile: width < 720});
        await page.goto(`${base}/?theme=light&locale=zh-CN&surface=${surface}&conversation=preview-conversation`);
        await page.locator('.role-assistant .markdown-table').waitFor();
        const geometry = await page.evaluate(() => {
          const history = document.querySelector('.history');
          history.scrollLeft = 100;
          return {
            viewport: innerWidth, page: document.documentElement.scrollWidth,
            history: history.clientWidth, historyScroll: history.scrollWidth, historyLeft: history.scrollLeft,
            bubblesFit: [...document.querySelectorAll('.message-body')].every(node => {
              const bubble = node.getBoundingClientRect(), row = node.parentElement.getBoundingClientRect();
              return bubble.left >= row.left && bubble.right <= row.right + 1;
            }),
            localScroll: [...document.querySelectorAll('.message-body pre, .markdown-table')].every(node => {
              node.scrollLeft = 100;
              return node.scrollWidth > node.clientWidth && node.scrollLeft > 0;
            }),
          };
        });
        if (width === 393 || width === 1440) {
          await page.locator('.history').evaluate(node => {
            node.scrollLeft = 0;
            for (const content of node.querySelectorAll('pre, .markdown-table')) content.scrollLeft = 0;
            node.scrollTop = node.scrollHeight;
          });
          await page.screenshot({path: `/private/tmp/pot-chat-overflow-${width}.png`});
        }
        assert(geometry.page <= geometry.viewport && geometry.historyScroll <= geometry.history && geometry.historyLeft === 0,
          `Wide Markdown keeps history within ${width}/${surface}: ${JSON.stringify(geometry)}`);
        assert(geometry.bubblesFit, `User and assistant bubbles fit at ${width}/${surface}`);
        assert(geometry.localScroll, `Full code and tables remain horizontally scrollable at ${width}/${surface}`);
      }
    } finally {
      await page.unroute('**/api/preview/snapshot', wideRoute);
      await page.unroute('**/api/preview/events', eventsRoute);
    }


    // Fresh phone visits select the directory. Navigation preserves its search.
    await page.goto(`${base}/?theme=light&locale=en`);
    await page.getByRole('searchbox', {name: 'Search assistants'}).fill('pot');
    await page.locator('.assistant-row:visible').click();
    await input.waitFor({state: 'visible'});
    await input.fill('draft survives narrow navigation');
    await page.getByRole('button', {name: 'Back to Assistants', exact: true}).click();
    assert(await page.getByRole('searchbox', {name: 'Search assistants'}).inputValue() === 'pot', 'Directory search survives Back');
    await page.locator('.assistant-row:visible').click();
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
    await input.fill('Pot UI browser verification');
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
    await page.screenshot({path: '/private/tmp/pot-ui-keyboard-after.png'});
    assert(errors.length === 0, 'Browser errors: ' + errors.join('\n'));
    return {layouts, sends: sends.length, errors};
  } finally {
    page.off('pageerror', onError); page.off('request', onRequest);
    await cdp.send('Emulation.setSafeAreaInsetsOverride',{insets:{top:0,bottom:0,left:0,right:0}});
    await cdp.detach();
  }
}
