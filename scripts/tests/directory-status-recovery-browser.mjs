// Real production UI, HTTP/SSE and IndexedDB; no product service or inference.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {resolve} from 'node:path';
const child=spawn(process.execPath,[resolve('scripts/tests/pot-ui-cold-start-browser.mjs'),'--serve'],{env:process.env,stdio:['ignore','pipe','inherit']});
let browser;
try{
 const origin=await new Promise((resolve,reject)=>{let text='';child.stdout.on('data',part=>{text+=part;const match=text.match(/http:\/\/127\.0\.0\.1:\d+/);if(match)resolve(match[0]);});child.on('exit',code=>reject(Error('Fixture exited '+code)));});
 const config=async values=>(await fetch(origin+'/__fixture?'+new URLSearchParams(values))).json();
 const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE||'playwright');
 browser=await chromium.launch({headless:true,executablePath:process.env.POT_CHROMIUM_EXECUTABLE,args:['--no-sandbox']});
 for(const locale of ['en','zh-CN']){
  const context=await browser.newContext({viewport:locale==='en'?{width:1280,height:800}:{width:390,height:844}}),page=await context.newPage();page.setDefaultTimeout(10000);
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await config({localOffline:'false'});
  await page.goto(origin+'/?assistant_id=alpha&space_id=local&locale='+locale);
  const status=page.locator('.titlebar .connection-status');
  await page.waitForFunction(()=>document.querySelector('.titlebar .connection-status')?.classList.contains('is-connected'));
  await page.reload();
  await page.waitForFunction(()=>document.querySelector('.titlebar .connection-status')?.classList.contains('is-connected'));
  const streams=(await config({})).calls.filter(call=>call.path.startsWith('/api/v1/events')).length;
  // A directory refresh can report unavailable after the chat stream is ready.
  await config({localOffline:'true',directory:'1'});
  await page.waitForFunction(()=>document.querySelector('.titlebar .connection-status')?.classList.contains('has-error'));
  assert.equal(await status.locator('.connection-retry').count(),1,'unavailable directory retains retry');
  const indicator=status.locator('.connection-indicator');
  assert(await indicator.getAttribute('aria-label'),'status has localized accessible details');
  assert.equal(await indicator.locator('.connection-detail').isVisible(),false,'no persistent status prose');
  assert.equal(await status.locator('.connection-retry').innerText(),'','retry is an icon');
  assert(await indicator.evaluate(node=>getComputedStyle(node).color===getComputedStyle(node.parentElement).color),'status uses neutral color');
  await indicator.click();await indicator.locator('.connection-detail').waitFor({state:'visible'});
  const box=await indicator.locator('.connection-detail').boundingBox();
  assert(box.x>=0&&box.x+box.width<=page.viewportSize().width,'detail fits viewport');
  await indicator.press('Tab');await page.mouse.move(0,0);
  await indicator.locator('.connection-detail').waitFor({state:'hidden'});
  await config({localOffline:'false',directory:'1'});
  await page.waitForFunction(()=>document.querySelector('.titlebar .connection-status')?.classList.contains('is-connected'));
  assert.equal(await status.locator('.connection-retry').count(),0,'directory recovery clears retry automatically');
  assert.equal((await config({})).calls.filter(call=>call.path.startsWith('/api/v1/events')).length,streams,'recovery must not need a new chat subscription');
  // A real offline transport must still be shown even with an online directory.
  await context.setOffline(true);
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  await page.waitForFunction(()=>!document.querySelector('.titlebar .connection-status')?.classList.contains('is-connected'));
  await context.setOffline(false);
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));
  await page.waitForFunction(()=>document.querySelector('.titlebar .connection-status')?.classList.contains('is-connected'));
  await config({hold:'session:status-animation'});await page.reload();
  const spinner=status.locator('.connection-indicator.is-loading');await spinner.waitFor();
  assert.notEqual(await spinner.locator('svg').evaluate(node=>getComputedStyle(node).animationName),'none');
  await page.emulateMedia({reducedMotion:'reduce'});
  assert.equal(await spinner.locator('svg').evaluate(node=>getComputedStyle(node).animationName),'none');
  await config({hold:'',releaseHeld:'status-animation'});
  await page.waitForFunction(()=>document.querySelector('.titlebar .connection-status')?.classList.contains('is-connected'));
  assert.deepEqual(errors,[]);await context.close();
 }
 console.log('PASS directory status recovers without retry or chat resubscription; real disconnect/reconnect, en/zh');
}finally{await browser?.close();child.kill('SIGTERM');}
