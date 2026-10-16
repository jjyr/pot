// Playwright CLI run-code expression; use only the isolated shared-ui fixture.
// These checks type drafts and navigate; they never save configuration or send messages.
async (page) => {
  const runtime = await (await page.request.get('http://127.0.0.1:17908/api/v1/runtime')).json();
  if (runtime.runtime_id !== 'fixture') throw Error('Expected isolated UI fixture on port 17908');
  const expectInput = async (expected, reason) => {
    try {
      await page.waitForFunction(value => {
        const composer = document.querySelector('#pot-composer');
        const editor = composer && !composer.hidden ? composer : document.querySelector('textarea:not(#pot-composer)');
        return editor?.value === value;
      }, expected, {timeout:3000});
    } catch {
      const value = await page.evaluate(() => {
        const composer = document.querySelector('#pot-composer');
        return (composer && !composer.hidden ? composer : document.querySelector('textarea:not(#pot-composer)'))?.value;
      });
      throw Error(reason + ': ' + value);
    }
  };
  const navigation = async (page) => {
  await page.evaluate(()=>{localStorage.setItem('pot-locale','en');localStorage.setItem('pot-appearance','1');});
  const assert=(ok,msg)=>{if(!ok)throw Error(msg);};
  const wait=()=>page.waitForTimeout(500);
  const cdp=await page.context().newCDPSession(page);
  await cdp.send('Emulation.setTouchEmulationEnabled',{enabled:true,maxTouchPoints:5});
  await page.setViewportSize({width:393,height:852});
  await page.goto('http://127.0.0.1:17908/'); await page.waitForTimeout(1000);
  assert(page.url().includes('page=assistants'),'phone should start in assistant directory');
  await page.screenshot({path:'output/playwright/devices/final-phone-assistants.png'});
  const listShot=()=>page.screenshot({clip:{x:0,y:170,width:393,height:682}});
  const baseline=await listShot();
  const swipe=async (points,cancel=false)=>{
    await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:points[0],y:440}]});
    for(const x of points.slice(1)){await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x,y:440}]});await page.waitForTimeout(35);}
    await cdp.send('Input.dispatchTouchEvent',{type:cancel?'touchCancel':'touchEnd',touchPoints:[]}); await wait();
  };
  await swipe([8,35,70,110,150,190,240]);
  await page.screenshot({path:'output/playwright/devices/final-phone-menu.png'});
  assert(!baseline.equals(await listShot()),'edge swipe must open menu');
  await swipe([300,270,240,210,180,150,110,70]);
  assert(baseline.equals(await listShot()),'menu gesture must not scroll underlying assistant list');
  await swipe([8,20,35,50]);
  assert(baseline.equals(await listShot()),'short swipe must settle closed');
  await swipe([8,40,80,130,170],true);
  assert(baseline.equals(await listShot()),'cancelled swipe must settle closed');
  await swipe([8,50,100,160,110,60,20]);
  assert(baseline.equals(await listShot()),'reversed swipe must settle closed');
  await page.mouse.click(34,22); await wait(); await page.mouse.click(120,242);
  await page.waitForURL(/page=usage/); await wait();
  await page.screenshot({path:'output/playwright/devices/final-phone-usage.png'});
  await page.mouse.click(34,22); await wait(); await page.mouse.click(120,294);
  await page.waitForURL(/page=settings/); await wait();
  await page.screenshot({path:'output/playwright/devices/final-phone-settings.png'});
  await page.mouse.click(34,22); await wait(); await page.mouse.click(120,86);
  await page.waitForURL(/page=assistants/); await wait();
  await page.mouse.click(120,140); await page.keyboard.type('Research'); await wait();
  await page.mouse.click(130,22); await wait();
  await page.screenshot({path:'output/playwright/devices/phone-search-before.png'});
  const filtered=await listShot();
  await page.mouse.click(120,205); await page.waitForURL(/page=chat.*assistant=assistant-2/); await wait();
  await page.screenshot({path:'output/playwright/devices/final-phone-chat.png'});
  await page.mouse.click(110,798); await expectInput('', 'new conversation input'); await page.keyboard.type('draft for Research'); await wait();
  await expectInput('draft for Research','chat draft entry');
  await swipe([8,45,90,135,190]);
  await page.waitForURL(/page=assistants/); await wait();
  assert(filtered.equals(await listShot()),'Back must restore search and directory layout');
  await page.mouse.click(120,205); await page.waitForURL(/page=chat.*assistant=assistant-2/); await wait();
  await expectInput('draft for Research','assistant draft must survive list return');
  await page.mouse.click(355,22); await wait();
  await page.screenshot({path:'output/playwright/devices/final-conversation-actions.png'});
  await page.goto('http://127.0.0.1:17908/'); await page.waitForTimeout(800);
  const header = await page.screenshot({clip:{x:0,y:0,width:393,height:44}});
  const unscrolled = await listShot();
  await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:200,y:650}]});
  for(const y of [610,570,530,490,450,410,370]){
    await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:200,y}]});
    await page.waitForTimeout(35);
  }
  await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]}); await wait();
  assert(header.equals(await page.screenshot({clip:{x:0,y:0,width:393,height:44}})),'vertical touch must not open navigation');
  assert(!unscrolled.equals(await listShot()),'vertical touch must scroll the assistant list');
  await swipe([150,180,210,240,270,300]);
  assert(header.equals(await page.screenshot({clip:{x:0,y:0,width:393,height:44}})),'content horizontal gesture must not open navigation');
  await cdp.detach();
  return {checks:12,phone:'393×852',url:page.url()};
};
  const restoration = async (page) => {
 const assert=(v,m)=>{if(!v)throw Error(m);};const wait=()=>page.waitForTimeout(500);
 await page.evaluate(()=>{localStorage.setItem('pot-locale','en');localStorage.setItem('pot-appearance','1');});
 const cdp=await page.context().newCDPSession(page);await cdp.send('Emulation.setTouchEmulationEnabled',{enabled:true,maxTouchPoints:5});
 await page.setViewportSize({width:393,height:852});await page.goto('http://127.0.0.1:17908/?page=chat&assistant=main');await page.waitForTimeout(900);
 await page.mouse.click(110,798);await expectInput('', 'initial conversation input');await page.keyboard.type('draft A retained');await wait();
 await page.mouse.click(34,22);await page.waitForURL(/page=assistants/);await wait();
 await page.mouse.click(120,275);await page.waitForURL(/page=chat.*assistant=assistant-2/);await wait();
 await page.mouse.click(110,798);await expectInput('','second assistant starts with independent draft');await page.keyboard.type('draft B retained');
 await page.mouse.click(34,22);await page.waitForURL(/page=assistants/);await wait();await page.mouse.click(120,205);await page.waitForURL(/page=chat.*assistant=main/);await wait();
 await page.mouse.click(110,798);await expectInput('draft A retained','draft A survives assistant switch');
 for(const width of [820,1280,393]){await page.setViewportSize({width,height:852});await wait();await page.mouse.click(width<720?110:width/2,798);await expectInput('draft A retained','draft survives resize '+width);}
 await page.setViewportSize({width:393,height:460});await wait();await page.mouse.click(110,406);await expectInput('draft A retained','draft survives keyboard-height viewport');
 await page.mouse.move(200,180);await page.mouse.wheel(0,-220);await wait();await page.mouse.click(140,22);await wait();
 const reading=await page.screenshot({clip:{x:0,y:44,width:393,height:300},path:'output/playwright/devices/reading-before.png'});
 await page.mouse.click(34,22);await page.waitForURL(/page=assistants/);await wait();await page.mouse.click(120,275);await page.waitForURL(/page=chat.*assistant=assistant-2/);await wait();
 await page.mouse.click(34,22);await page.waitForURL(/page=assistants/);await wait();await page.mouse.click(120,205);await page.waitForURL(/page=chat.*assistant=main/);await wait();await page.mouse.click(140,22);await wait();
 assert(reading.equals(await page.screenshot({clip:{x:0,y:44,width:393,height:300},path:'output/playwright/devices/reading-return.png'})),'reading position survives assistant switch');
 await page.setViewportSize({width:393,height:852});await wait();await page.mouse.click(355,22);await wait();await page.mouse.click(250,74);await wait();
 await page.mouse.click(130,118);await page.keyboard.press('Meta+A');await page.keyboard.type('Dirty assistant name');await page.mouse.click(34,22);await wait();
 await page.screenshot({path:'output/playwright/devices/final-assistant-dirty-guard.png'});
 await page.keyboard.press('Escape');await wait();await page.mouse.click(130,118);await expectInput('Dirty assistant name','Stay retains assistant draft');
 await page.mouse.click(34,22);await wait();await page.mouse.click(290,448);await wait();await page.mouse.click(110,798);
 await expectInput('draft A retained','editor return restores conversation draft');
 await page.screenshot({path:'output/playwright/devices/final-editor-chat-return.png'});
 await page.mouse.click(355,22);await wait();await page.mouse.click(250,74);await wait();await page.mouse.click(130,118);await expectInput('Pot','Discard resets assistant draft');await page.mouse.click(34,22);await wait();
 await page.mouse.click(34,22);await page.waitForURL(/page=assistants/);await wait();await page.setViewportSize({width:393,height:460});await wait();await page.mouse.move(200,300);await page.mouse.wheel(0,280);await wait();await page.mouse.move(140,22);await wait();
 const list=await page.screenshot({clip:{x:0,y:170,width:393,height:290},path:'output/playwright/devices/list-scroll-before.png'});
 await page.mouse.click(120,205);await page.waitForURL(/page=chat/);await wait();await page.mouse.click(34,22);await page.waitForURL(/page=assistants/);await wait();
 assert(list.equals(await page.screenshot({clip:{x:0,y:170,width:393,height:290},path:'output/playwright/devices/list-scroll-return.png'})),'directory scroll restored');
 await cdp.detach();return {checks:12,assistantDrafts:'isolated',reading:'restored',list:'restored',editor:'guarded and restored',resize:[393,820,1280,393]};
};
  return { navigation: await navigation(page), restoration: await restoration(page) };
}
