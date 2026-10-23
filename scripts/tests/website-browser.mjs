// Serve the checkout root locally; provide Playwright externally (no website runtime dependency).
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE||'playwright');
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
const installOnly=process.argv.includes('--install-only');
const browser=await chromium.launch({headless:true,executablePath:process.env.POT_CHROMIUM_EXECUTABLE});
try {
const origin=process.env.POT_WEBSITE_ORIGIN||'http://127.0.0.1:4173',errors=[];
const install=JSON.parse(await fs.readFile('website/install.json','utf8'));
const page=await browser.newPage({viewport:{width:1280,height:900},reducedMotion:'reduce'});
page.on('pageerror',e=>errors.push(e.message));
let starMode='ok',starRequests=0;
await page.route('https://api.github.com/repos/jjyr/pot',async route=>{
 starRequests++;
 if(starMode==='network')return route.abort();
 if(starMode==='timeout')return;
 await route.fulfill({status:starMode==='missing'?404:starMode==='limited'?403:200,contentType:'application/json',body:JSON.stringify({stargazers_count:starMode==='invalid'?'wrong':starMode==='zero'?0:1234})});
});
await page.addInitScript(()=>{Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async text=>{window.copied=text;}}});});
for(const locale of ['en','zh-CN','zh-TW','ja','ko','es']){
 await page.goto(`${origin}/website/${locale}/`);
 assert.equal(await page.locator('#agent-prompt').textContent(),install.prompt);
 assert.equal(await page.locator('.install-panel').count(),4);
 assert.deepEqual(await page.locator('[role=tab]').evaluateAll(tabs=>tabs.map(t=>t.dataset.install)),['agent','macos','linux','web']);
 assert.equal(await page.locator('[role=tab][aria-selected=true]').getAttribute('data-install'),'macos');
 assert.equal(await page.locator('.install-panel:visible').count(),1);
 for(const width of [1280,390]){
  await page.setViewportSize({width,height:width===390?844:900});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`${locale} overflow at ${width}`);
  const mark=page.locator('.github-star svg');
  assert.equal(await mark.count(),1);
  assert.equal(await mark.getAttribute('aria-hidden'),'true');
  assert(await mark.evaluate(e=>{const rect=e.getBoundingClientRect(),style=getComputedStyle(e);return rect.width===16&&rect.height===16&&style.fill===style.color&&style.stroke==='none';}),`${locale} GitHub Mark is filled and keeps its proportions at ${width}`);
  assert(await page.locator('.github-star').getAttribute('aria-label'));
  if(width===390)assert(await page.evaluate(()=>{const star=document.querySelector('.github-star').getBoundingClientRect(),start=document.querySelector('.nav-cta').getBoundingClientRect();return star.right<=start.left&&Math.abs((star.top+star.bottom-start.top-start.bottom)/2)<2;}),`${locale} header actions share a row`);
  for(const [channel,id] of [['agent','agent-prompt'],['macos','brew-command'],['linux',null],['web','docker-image'],['web','server-command']]){
   await page.locator(`[data-install="${channel}"]`).click();
   assert.equal(await page.locator('.install-panel:visible').count(),1);
   assert.equal(await page.locator(`[data-install="${channel}"]`).getAttribute('aria-selected'),'true');
   if(channel==='linux'){
    assert.equal(await page.locator('#install-linux a.primary').count(),1);
    assert.equal(await page.locator('#install-linux #docker-image').count(),0);
    assert.equal(await page.locator('#install-linux a[href*="packages/container"]').count(),0);
    assert(await page.locator('#install-linux .linux-native').isVisible());
    assert.equal(await page.locator('#install-linux .experimental').innerText(),'Experimental');
   }
   if(channel==='macos'){
    assert.equal(await page.locator('#install-macos a').count(),1);
    assert.equal(await page.locator('#install-macos .install-guide').getAttribute('href'),'https://github.com/jjyr/pot/blob/main/docs/desktop-installation.md#macos');
    assert.equal(await page.locator('#install-macos a[href*="/releases"]').count(),0);
    assert.equal(await page.locator('#install-macos a.primary').count(),0);
    assert(await page.locator('#install-macos .brew-install').isVisible());
   }
   if(channel==='web'){
    assert(await page.locator('#install-web .web-server').isVisible());
    assert(await page.locator('#install-web .web-docker').isVisible());
   }
   if(channel==='web'&&!await page.locator('.web-start').evaluate(e=>e.open))await page.locator('.web-start summary').click();
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`${locale} ${channel} overflow at ${width}`);
   if(id){
    await page.locator(`[data-copy="${id}"]`).click();
    assert.equal(await page.evaluate(()=>window.copied),await page.locator('#'+id).textContent());
   }
  }
 }
 // Keyboard navigation exposes one focusable tab and its associated panel.
 await page.locator('[data-install="macos"]').focus();
 await page.keyboard.press('ArrowRight');
 assert.equal(await page.locator('[role=tab][aria-selected=true]').getAttribute('data-install'),'linux');
 await page.keyboard.press('End');assert.equal(await page.locator('#install-web').isVisible(),true);
 await page.keyboard.press('Home');assert.equal(await page.locator('#install-agent').isVisible(),true);
 assert.equal(await page.locator('[role=tab][tabindex="0"]').count(),1);
 assert.equal(await page.locator('#brew-command').textContent(),install.brew);
 assert.equal(await page.locator('#docker-image').textContent(),install.image);
 assert.equal(await page.locator('#install-web .web-docker .primary').getAttribute('href'),'https://github.com/users/jjyr/packages/container/package/pot');
 for(const selector of ['#install-linux .linux-native .primary','#install-web .web-server .primary'])
  for(const link of await page.locator(selector).all())assert.equal(await link.getAttribute('href'),'https://github.com/jjyr/pot/releases');
 if(!installOnly){
 await page.locator('#run-project').click();
 assert.equal(await page.locator('.project-task.running').count(),3);
 await page.waitForFunction(()=>document.querySelectorAll('.project-task.done').length===1);
 assert.equal(await page.locator('.project-task.running').count(),2);
 await page.locator('#finish-project').click();assert.equal(await page.locator('.project-task.done').count(),3);
 await page.waitForTimeout(2300);assert.equal(await page.locator('.project-task.running').count(),0); // stopped timers cannot overwrite terminal state
 for(const type of ['install','docs','screens']){
  await page.locator(`.delivery-links [data-open-report="${type}"]`).click();
  assert(await page.locator('#report-content').innerText());
  if(type==='screens'){
   await page.waitForFunction(()=>[...document.querySelectorAll('.report-gallery img')].every(i=>i.complete&&i.naturalWidth===1280));
  }
  await page.keyboard.press('Escape');assert.equal(await page.locator('#report-dialog').evaluate(e=>e.open),false);
 }
 }
 console.log(installOnly?'installation, responsive, clipboard and keyboard:':'locale, responsive, clipboard, parallel playback and deliverables:',locale,'passed');
}
await page.locator('[data-install="web"]').click();
await page.selectOption('#language-select','ja');await page.waitForURL('**/ja/**');
assert.equal(await page.locator('[role=tab][aria-selected=true]').getAttribute('data-install'),'web');
assert(await page.locator('#install-web .web-docker').isVisible());
console.log('Static language switch preserves selected installation channel');
await page.setViewportSize({width:1280,height:900});
if(!installOnly){
for(const mode of ['ok','missing','limited','network','invalid','zero','timeout']){
 starMode=mode;
 await page.evaluate(()=>localStorage.removeItem('pot-github-stars-v1'));
 const failure=mode==='timeout'?page.waitForEvent('requestfailed',{predicate:r=>r.url()==='https://api.github.com/repos/jjyr/pot',timeout:5000}):null;
 await page.goto(`${origin}/website/en/`);if(failure)await failure;await page.waitForTimeout(150);
 const label=await page.locator('.github-star').innerText();
 assert.equal(await page.locator('.github-star').getAttribute('href'),'https://github.com/jjyr/pot');
 assert.equal(label.includes('1.2K'),mode==='ok',mode);
 if(mode!=='ok')assert.equal(label.trim(),'GitHub Star');
 console.log('Star',mode,'passed');
}
// Cached and unavailable storage must leave the UI usable.
await page.evaluate(()=>localStorage.setItem('pot-github-stars-v1',JSON.stringify({time:Date.now(),count:987})));
const before=starRequests;await page.reload();await page.waitForTimeout(100);assert.equal(starRequests,before);assert.match(await page.locator('.github-star').innerText(),/987/);
await page.addInitScript(()=>{Object.defineProperty(window,'localStorage',{configurable:true,get(){throw Error('Unavailable storage')}})});
starMode='limited';await page.reload();await page.waitForTimeout(100);assert.equal((await page.locator('.github-star').innerText()).trim(),'GitHub Star');
await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async()=>{throw Error('Denied')}}}));
await page.locator('[data-install="agent"]').click();await page.locator('[data-copy="agent-prompt"]').click();assert.equal(await page.evaluate(()=>getSelection().toString()),install.prompt);assert.match(await page.locator('#copy-status').innerText(),/Select and copy/);
// Keyboard activation and modal focus restoration use native controls.
await page.locator('#run-project').focus();await page.keyboard.press('Enter');assert.equal(await page.locator('.project-task.running').count(),3);
await page.locator('#finish-project').focus();await page.keyboard.press('Space');
await page.locator('.delivery-links [data-open-report="docs"]').focus();await page.keyboard.press('Enter');assert.equal(await page.locator('#report-dialog').evaluate(e=>e.open),true);
await page.keyboard.press('Escape');assert.equal(await page.evaluate(()=>document.activeElement.dataset.openReport),'docs');
await page.locator('.github-star').focus();assert.equal(await page.evaluate(()=>document.activeElement.getAttribute('href')),'https://github.com/jjyr/pot');
assert(await page.locator('.github-star').evaluate(e=>getComputedStyle(e).outlineStyle!=='none'));
assert.deepEqual(errors,[]);
console.log('cache, unavailable storage, clipboard fallback and keyboard checks passed');
}
assert.deepEqual(errors,[]);
const offline=await browser.newPage();const outgoing=[];
offline.on('request',request=>{if(request.url().startsWith('https://'))outgoing.push(request.url())});
await offline.goto(origin+'/website/pot-preview.html');
if(!installOnly){
await offline.locator('.delivery-links [data-open-report="screens"]').click();
await offline.waitForFunction(()=>[...document.querySelectorAll('.report-gallery img')].every(i=>i.complete&&i.naturalWidth===1280));assert.deepEqual(outgoing,[]);
await offline.locator('#report-dialog').evaluate(e=>e.close());
}
await offline.locator('[data-install="web"]').click();await offline.selectOption('#language-select','es');
assert.equal(await offline.locator('[role=tab][aria-selected=true]').getAttribute('data-install'),'web');
assert(await offline.locator('#install-web .web-server').isVisible());
assert(await offline.locator('#install-web .web-docker').isVisible());
const noJS=await browser.newPage({javaScriptEnabled:false});await noJS.goto(origin+'/website/en/');assert.equal(await noJS.locator('#agent-prompt').textContent(),install.prompt);assert.equal(await noJS.locator('.project-task').count(),3);assert.equal(await noJS.locator('.install-panel:visible').count(),4);
assert.equal(await noJS.locator('#install-linux #docker-image').count(),0);
assert(await noJS.locator('#install-web .web-docker').isVisible());
console.log('Offline preview and no-JavaScript install content passed');
} finally {
 await browser.close();
}
