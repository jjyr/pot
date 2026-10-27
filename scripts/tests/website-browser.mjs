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
async function capture(){
 const dir='docs/pr-images/redesign-website';await fs.mkdir(dir,{recursive:true});
 for(const width of [1280,390,320])for(const version of ['before','after']){
  await page.setViewportSize({width,height:900});
  await page.goto(`${origin}/${version==='before'?'output/website-before/':''}website/zh-CN/`);
  await page.screenshot({path:`${dir}/manual-home-${width}-${version}.png`});
 }
 await page.setViewportSize({width:1280,height:900});
 for(const view of ['security','install','changelog'])for(const version of ['before','after']){
  await page.goto(`${origin}/${version==='before'?'output/website-before/':''}website/zh-CN/`);
  await page.locator(`[data-view-tab="${view}"]`).click();
  await page.screenshot({path:`${dir}/manual-${view}-1280-${version}.png`});
 }
 await page.goto(`${origin}/website/zh-CN/#assistant`);
 await page.locator('img').evaluateAll(images=>images.forEach(image=>image.loading='eager'));
 await page.locator('img').evaluateAll(images=>Promise.all(images.map(image=>image.decode())));
 await page.screenshot({path:`${dir}/manual-design-1280-after.png`,fullPage:true});
}
if(process.argv.includes('--capture-only')){await capture();await browser.close();process.exit(0);}
for(const locale of ['en','zh-CN','zh-TW','ja','ko','es']){
 await page.goto(`${origin}/website/${locale}/`);
 assert.equal(await page.locator('#view-overview').isVisible(),true);
 assert.equal(await page.locator('#view-security').isVisible(),false);
 assert.equal(await page.locator('#view-install').isVisible(),false);
 assert.equal(await page.locator('#view-changelog').isVisible(),false);
 await page.locator('[data-view-tab="security"]').click();
 assert.equal(await page.locator('#view-security').isVisible(),true);
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`${locale} security overflow`);
 await page.locator('[data-view-tab="changelog"]').click();
 assert.equal(await page.locator('#view-changelog').isVisible(),true);
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`${locale} changelog overflow`);
 await page.locator('[data-view-tab="install"]').click();
 assert.equal(await page.locator('#view-install').isVisible(),true);
 assert.equal(await page.locator('#agent-prompt').textContent(),install.prompt);
 assert.equal(await page.locator('.install-panel').count(),4);
 assert.deepEqual(await page.locator('[role=tab]').evaluateAll(tabs=>tabs.map(t=>t.dataset.install)),['agent','macos','linux','web']);
 assert.equal(await page.locator('[role=tab][aria-selected=true]').getAttribute('data-install'),'macos');
 assert.equal(await page.locator('.install-panel:visible').count(),1);
 for(const width of [1280,390,320]){
  await page.setViewportSize({width,height:width===390?844:900});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`${locale} overflow at ${width}`);
  const mark=page.locator('.github-star img');
  assert.equal(await mark.count(),1);
  assert.equal(await mark.getAttribute('aria-hidden'),'true');
  assert.equal(await mark.getAttribute('alt'),'');
  assert(await mark.evaluate(e=>{const rect=e.getBoundingClientRect();return e.complete&&e.naturalWidth===98&&e.naturalHeight===96&&rect.height===16&&Math.abs(rect.width/rect.height-98/96)<0.001;}),`${locale} official Invertocat loads and keeps its proportions at ${width}`);
  assert.equal(await mark.getAttribute('height'),'16');
  assert(await mark.evaluate(e=>{
   const sheets=[...document.styleSheets],disabled=sheets.map(s=>s.disabled);
   try{for(const sheet of sheets)sheet.disabled=true;const rect=e.getBoundingClientRect();return rect.height===16&&Math.abs(rect.width/rect.height-98/96)<0.001;}
   finally{sheets.forEach((s,i)=>{s.disabled=disabled[i];});}
  }),`${locale} Invertocat stays compact without the stylesheet at ${width}`);
  assert(await page.locator('.github-star').evaluate(e=>e.getBoundingClientRect().height>=44&&getComputedStyle(e).cursor==='pointer'));
  assert(await page.locator('.github-star').getAttribute('aria-label'));
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
 await page.locator('[data-view-tab="changelog"]').click();
 assert.equal(await page.locator('#view-changelog').isVisible(),true);
 assert.deepEqual(await page.locator('.release-tag').evaluateAll(els=>els.map(e=>e.getAttribute('href'))),[
  'https://github.com/jjyr/pot/releases/tag/v0.0.4',
  'https://github.com/jjyr/pot/releases/tag/v0.0.3',
  'https://github.com/jjyr/pot/releases/tag/v0.0.2',
  'https://github.com/jjyr/pot/releases/tag/v0.0.1',
 ]);
 assert.deepEqual(await page.locator('.release-title-link').evaluateAll(els=>els.map(e=>e.getAttribute('href'))),[
  'https://github.com/jjyr/pot/releases/tag/v0.0.4',
  'https://github.com/jjyr/pot/releases/tag/v0.0.3',
  'https://github.com/jjyr/pot/releases/tag/v0.0.2',
  'https://github.com/jjyr/pot/releases/tag/v0.0.1',
 ]);
 assert.match(await page.locator('.release-title-link').first().innerText(),/Pot 0\.0\.4/);
 await page.locator('[data-view-tab="overview"]').click();
 assert.equal(await page.locator('#view-overview').isVisible(),true);
 const postHref=await page.locator('.further').getAttribute('href');
 assert.equal(postHref,locale==='zh-CN'||locale==='zh-TW'?'https://jjylab.com/posts/why-agent-coding-is-exhausting/':'https://jjylab.com/en/posts/why-agent-coding-is-exhausting/');
 for(const width of [1280,390,320]){
  await page.setViewportSize({width,height:900});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`${locale} overview overflow at ${width}`);
 }
 // Direct links, refresh and history must reveal their target panel.
 await page.goto(`${origin}/website/${locale}/#view-security`);
 assert(await page.locator('#view-security').isVisible());
 await page.locator('[data-view-tab="install"]').click();
 await page.locator('[data-install="linux"]').click();
 await page.reload();assert(await page.locator('#install-linux').isVisible());
 await page.goBack();assert(await page.locator('#view-security').isVisible());
 await page.goForward();assert(await page.locator('#view-install').isVisible());
 await page.locator('[data-view-tab="overview"]').click();
 assert.equal(new URL(page.url()).hash,'#philosophy');
 assert(await page.locator('#philosophy').isVisible());
 assert.equal(await page.locator('.product-figure').count(),2);
 await page.locator('.product-figure img').first().scrollIntoViewIfNeeded();
 await page.locator('.product-figure img').first().evaluate(img=>img.decode());
 assert(await page.locator('.product-figure img').first().evaluate(img=>img.naturalWidth>300));
 assert.equal(await page.locator('.product-figure a').first().getAttribute('href'),'../assets/product-work.png');

 }
 console.log(installOnly?'installation, responsive, clipboard and keyboard:':'locale, responsive, clipboard, changelog links and navigation:',locale,'passed');
}
await page.locator('[data-view-tab="install"]').click();
await page.locator('[data-install="web"]').click();
await page.selectOption('#language-select','ja');await page.waitForURL('**/ja/**');
assert.equal(await page.locator('[role=tab][aria-selected=true]').getAttribute('data-install'),'web');
assert(await page.locator('#install-web .web-docker').isVisible());
console.log('Static language switch preserves installation state');
await page.setViewportSize({width:1280,height:900});
if(!installOnly){
for(const mode of ['ok','missing','limited','network','invalid','zero','timeout']){
 starMode=mode;
 await page.evaluate(()=>localStorage.removeItem('pot-github-stars-v1'));
 const failure=mode==='timeout'?page.waitForEvent('requestfailed',{predicate:r=>r.url()==='https://api.github.com/repos/jjyr/pot',timeout:5000}):null;
 await page.goto(`${origin}/website/en/`);if(failure)await failure;await page.waitForTimeout(150);
 const label=await page.locator('.github-star').innerText();
 assert.equal(await page.locator('.github-star').getAttribute('href'),'https://github.com/jjyr/pot');
 assert.equal(label.includes('1,234'),mode==='ok',mode);
 if(mode!=='ok'&&mode!=='zero')assert.equal(label.trim(),'GitHub');
 if(mode==='zero')assert.match(label,/0/);
 console.log('Star',mode,'passed');
}
// Cached and unavailable storage must leave the UI usable.
await page.evaluate(()=>localStorage.setItem('pot-github-stars-v1',JSON.stringify({time:Date.now(),count:987})));
const before=starRequests;await page.reload();await page.waitForTimeout(100);assert.equal(starRequests,before);assert.match(await page.locator('.github-star').innerText(),/987/);
await page.addInitScript(()=>{Object.defineProperty(window,'localStorage',{configurable:true,get(){throw Error('Unavailable storage')}})});
starMode='limited';await page.reload();await page.waitForTimeout(100);assert.equal((await page.locator('.github-star').innerText()).trim(),'GitHub');
await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async()=>{throw Error('Denied')}}}));
await page.locator('[data-view-tab="install"]').click();
await page.locator('[data-install="agent"]').click();await page.locator('[data-copy="agent-prompt"]').click();assert.equal(await page.evaluate(()=>getSelection().toString()),install.prompt);assert.match(await page.locator('#copy-status').innerText(),/Select and copy/);
await page.locator('[data-view-tab="overview"]').click();
await page.keyboard.press('Tab');
await page.locator('.github-star').focus();assert.equal(await page.evaluate(()=>document.activeElement.getAttribute('href')),'https://github.com/jjyr/pot');
assert(await page.locator('.github-star').evaluate(e=>getComputedStyle(e).outlineStyle!=='none'));
assert.deepEqual(errors,[]);
console.log('cache, unavailable storage, clipboard fallback and keyboard checks passed');
}
assert.deepEqual(errors,[]);
if(process.argv.includes('--capture'))await capture();

const offline=await browser.newPage();const outgoing=[];
offline.on('request',request=>{if(request.url().startsWith('https://'))outgoing.push(request.url())});
await offline.goto(origin+'/website/pot-preview.html');
await offline.locator('[data-view-tab="install"]').click();
await offline.locator('[data-install="web"]').click();await offline.selectOption('#language-select','es');
assert.equal(await offline.locator('[role=tab][aria-selected=true]').getAttribute('data-install'),'web');
assert(await offline.locator('#install-web .web-server').isVisible());
assert(await offline.locator('#install-web .web-docker').isVisible());
assert(await offline.locator('.github-star img').evaluate(e=>e.src.startsWith('data:image/svg+xml;base64,')&&e.complete&&e.naturalWidth===98&&e.naturalHeight===96));
const noJS=await browser.newPage({javaScriptEnabled:false});await noJS.goto(origin+'/website/en/');assert.equal(await noJS.locator('#agent-prompt').textContent(),install.prompt);assert.equal(await noJS.locator('.release-tag').count(),4);assert.equal(await noJS.locator('.install-panel:visible').count(),4);
assert.equal(await noJS.locator('#install-linux #docker-image').count(),0);
assert(await noJS.locator('#install-web .web-docker').isVisible());
assert.deepEqual(outgoing,[]);
console.log('Offline preview and no-JavaScript install content passed');
for(const [language,saved,expected] of [['ja-JP',null,'ja'],['zh-Hant',null,'zh-TW'],['zh-Hans-TW',null,'zh-CN'],['fr-FR',null,'en'],['ko-KR','es','es']]){
 const context=await browser.newContext({locale:language});
 if(saved)await context.addInitScript(value=>localStorage.setItem('pot.website.language',value),saved);
 const entry=await context.newPage();await entry.route('https://api.github.com/**',route=>route.abort());
 await entry.goto(origin+'/website/');await entry.waitForURL(`**/${expected}/`);
 assert.equal(await entry.locator('html').getAttribute('lang'),expected);
 await entry.goto(origin+'/website/en/');assert.equal(await entry.locator('html').getAttribute('lang'),'en');
 await context.close();
}
console.log('Root language detection, saved preference and explicit locale paths passed');
} finally {
 await browser.close();
}
