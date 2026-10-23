// Focused checks for the production Star link. API fixtures never contact GitHub.
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE||'playwright');
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
const origin=process.env.POT_WEBSITE_ORIGIN||'http://127.0.0.1:4173';
const browser=await chromium.launch({headless:true,executablePath:process.env.POT_CHROMIUM_EXECUTABLE});
const api='https://api.github.com/repos/jjyr/pot',key='pot-github-stars-v1';
const errors=[],results=[];
async function open({locale='en',width=1280,count=1234,status=200,mode='',cached,css}={}){
 const context=await browser.newContext({viewport:{width,height:900},reducedMotion:'reduce'}),page=await context.newPage();
 page.on('pageerror',e=>errors.push(e.message));let requests=0;
 await page.route(api,async route=>{
  requests++;const request=route.request();
  assert.equal(request.headers().authorization,undefined);assert.equal(request.headers().referer,undefined);
  if(mode==='network')return route.abort();if(mode==='timeout')return;
  return route.fulfill({status,contentType:'application/json',body:mode==='json'?'invalid':JSON.stringify({stargazers_count:count})});
 });
 if(cached)await page.addInitScript(({key,cached})=>localStorage.setItem(key,JSON.stringify(cached)),{key,cached});
 if(mode==='storage')await page.addInitScript(()=>Object.defineProperty(window,'localStorage',{get(){throw Error('Storage blocked')}}));
 if(css!==undefined)await page.route('**/style.css',route=>route.fulfill({contentType:'text/css',body:css}));
 await page.goto(`${origin}/website/${locale}/`);await page.locator('.github-star img').evaluate(e=>e.decode());
 if(mode==='timeout')await page.waitForEvent('requestfailed',{predicate:r=>r.url()===api});
 else await page.waitForFunction(()=>document.readyState==='complete');
 return {page,context,requests:()=>requests};
}
async function layout(page){
 const m=await page.evaluate(()=>{
  const a=document.querySelector('.github-star'),i=a.querySelector('img'),b=a.querySelector('.github-star-count'),text=a.querySelector('.github-star-action>span');
  const rect=e=>e.getBoundingClientRect().toJSON(),style=getComputedStyle(a);
  return {icon:rect(i),link:rect(a),text:rect(text),header:rect(document.querySelector('.site-header')),cta:rect(document.querySelector('.nav-cta')),overflow:document.documentElement.scrollWidth>innerWidth,bubbleHidden:b.hidden,tip:getComputedStyle(b,'::before').width,cursor:style.cursor,href:a.getAttribute('href'),nested:a.querySelectorAll('a,button,input,select,[tabindex]').length};
 });
 assert.equal(m.icon.height,16);assert(Math.abs(m.icon.width-16*98/96)<.03);assert.equal(m.link.height,44);assert.equal(m.cursor,'pointer');
 assert.equal(m.overflow,false);assert.equal(m.nested,0);assert.equal(m.href,'https://github.com/jjyr/pot');
 assert(Math.abs(m.icon.y+m.icon.height/2-m.text.y-m.text.height/2)<.5);
 assert(m.link.right<=m.cta.x);assert(m.header.height<=130);if(!m.bubbleHidden)assert.equal(m.tip,'7px');
 return m;
}
try{
 for(const locale of ['en','zh-CN','zh-TW','ja','ko','es'])for(const width of [1280,390,320])for(const count of [0,1,1234,175008,1234567890,Number.MAX_SAFE_INTEGER]){
  const {page,context}=await open({locale,width,count});await page.waitForFunction(()=>!document.querySelector('.github-star-count').hidden);
  const m=await layout(page),full=new Intl.NumberFormat(locale).format(count),label=await page.locator('.github-star').getAttribute('aria-label');
  assert(label.includes(full));assert.equal(await page.locator('.github-star').getAttribute('title'),label);
  if(width<580){const shown=await page.locator('.github-star-count-compact').innerText();assert(shown.length<=5,shown);}else assert.equal(await page.locator('.github-star-count-full').innerText(),full);
  results.push({locale,width,count,icon:[m.icon.width,m.icon.height],header:m.header.height});await context.close();
 }
 for(const opts of [{status:404},{status:403},{status:429},{mode:'network'},{mode:'timeout'},{mode:'json'},{count:null},{count:'0'},{count:-1},{count:1.5},{count:1e20}]){
  const {page,context}=await open({...opts,width:320});await page.waitForTimeout(80);assert(await page.locator('.github-star-count').isHidden());assert.equal(await page.locator('.github-star').innerText(),'Star');await layout(page);await context.close();
 }
 for(const count of [0,987]){
  const {page,context,requests}=await open({cached:{time:Date.now(),count}});assert.equal(requests(),0);assert.equal(await page.locator('.github-star-count-full').innerText(),String(count));await context.close();
 }
 for(const cached of [{time:Date.now()-7*3600000,count:987},{time:Date.now()+3600000,count:987},{time:Date.now(),count:-1}]){
  const {page,context,requests}=await open({cached});await page.waitForFunction(()=>!document.querySelector('.github-star-count').hidden);assert.equal(requests(),1);assert.equal(await page.locator('.github-star-count-full').innerText(),'1,234');await context.close();
 }
 const blocked=await open({mode:'storage',width:320});await blocked.page.waitForFunction(()=>!document.querySelector('.github-star-count').hidden);await layout(blocked.page);await blocked.context.close();
 // Current main's CSS has no icon selector; HTML height must work even with that CSS or none.
 const oldCSS=process.env.POT_WEBSITE_OLD_CSS?await fs.readFile(process.env.POT_WEBSITE_OLD_CSS,'utf8'):null;
 for(const css of ['',...(oldCSS===null?[]:[oldCSS])]){
  const {page,context}=await open({css});const icon=await page.locator('.github-star img').boundingBox();assert.equal(icon.height,16);assert(Math.abs(icon.width-16*98/96)<.03);await context.close();
 }
 const {page,context}=await open();await page.waitForFunction(()=>!document.querySelector('.github-star-count').hidden);const link=page.locator('.github-star'),action=page.locator('.github-star-action');
 assert.equal(await action.evaluate(e=>getComputedStyle(e).backgroundColor),'rgb(255, 254, 250)');
 await link.hover();assert.equal(await action.evaluate(e=>getComputedStyle(e).backgroundColor),'rgb(243, 235, 224)');
 await page.mouse.down();assert.equal(await action.evaluate(e=>getComputedStyle(e).backgroundColor),'rgb(234, 219, 201)');await page.mouse.move(0,0);await page.mouse.up();
 await link.focus();await page.keyboard.press('Tab');await page.keyboard.press('Shift+Tab');assert(await link.evaluate(e=>e.matches(':focus-visible')&&getComputedStyle(e).outlineWidth==='3px'));await page.keyboard.press('Tab');assert.equal(await page.evaluate(()=>document.activeElement.className),'nav-cta');
 // Observe trusted DOM activation while preventing navigation; no GitHub Star action is made.
 await link.evaluate(a=>{window.starClicks=0;a.addEventListener('click',e=>{e.preventDefault();window.starClicks++;});});
 await link.focus();await page.keyboard.press('Enter');assert.equal(await page.evaluate(()=>window.starClicks),1);
 await page.locator('.github-star-count').click();assert.equal(await page.evaluate(()=>window.starClicks),2);
 const r=await link.boundingBox(),left=await action.boundingBox();await page.mouse.click(left.x+left.width+4,r.y+22);assert.equal(await page.evaluate(()=>window.starClicks),3);
 await context.close();assert.deepEqual(errors,[]);
 if(process.env.POT_STAR_RESULTS)await fs.writeFile(process.env.POT_STAR_RESULTS,JSON.stringify(results,null,2));
 console.log(`Star: ${results.length} locale/viewport/count layouts; failures, cache, storage, CSS fallback, pointer and keyboard checks passed`);
}finally{await browser.close();}
