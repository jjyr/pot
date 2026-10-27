import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {chatInteractionsFixture} from './chat-interactions-fixture.mjs';

// Exercises the production document, HTTP adapter, SSE, SW and IndexedDB. The
// fixture only supplies isolated business data and controlled transport faults.
export async function chatDirectoryActivity(browser,{sourceOrigin,baseline=false,output}={}){
 const results=[];
 for(const phone of [false,true]){
  const fixture=await chatInteractionsFixture(sourceOrigin),context=await browser.newContext({viewport:phone?{width:390,height:852}:{width:1280,height:800},isMobile:phone,hasTouch:phone}),page=await context.newPage(),errors=[],pages=[];
  page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message));
  await context.addInitScript(()=>{
   window.directoryTimeline=[];window.directoryPhase='startup';
   const complete=Object.getOwnPropertyDescriptor(IDBTransaction.prototype,'oncomplete');
   Object.defineProperty(IDBTransaction.prototype,'oncomplete',{...complete,set(handler){complete.set.call(this,function(event){directoryTimeline.push({at:Date.now(),kind:'idb-complete',phase:directoryPhase,mode:this.mode,stores:[...this.objectStoreNames]});handler.call(this,event);});}});
   const sample=()=>{
    const history=document.querySelector('.history');
    directoryTimeline.push({at:Date.now(),kind:'frame',phase:directoryPhase,cooking:document.querySelectorAll('.assistant-activity[data-state="cooking"]').length,disabled:document.querySelector('.send-button')?.disabled,local:document.querySelector('#app')?.classList.contains('is-local-startup'),status:document.querySelector('.connection-status')?.textContent,historyVisible:history&&getComputedStyle(history).visibility==='visible',scrollTop:history?.scrollTop});requestAnimationFrame(sample);
   };requestAnimationFrame(sample);
  });
  const control=async data=>assert((await context.request.post(fixture.origin+'/fixture/control',{data})).ok());
  const phase=name=>page.evaluate(name=>{window.directoryPhase=name;return new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));},name);
  const waitReady=()=>page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&!document.querySelector('.send-button')?.disabled);
  const sample=()=>page.evaluate(()=>directoryTimeline.findLast(row=>row.kind==='frame'));
  const blocked=()=>page.waitForFunction(()=>!!document.querySelector('.send-button')?.disabled);
  const noCommand=async()=>{const before=fixture.trace.filter(row=>row.method==='POST').length;await page.locator('.send-button').evaluate(button=>{button.disabled=false;try{button.click();}finally{button.disabled=true;}});await page.waitForTimeout(80);assert.equal(fixture.trace.filter(row=>row.method==='POST').length,before,'unconfirmed capabilities must reject a command inside the handler');};
  const mark=async name=>{const frames=await page.evaluate(name=>directoryTimeline.filter(row=>row.kind==='frame'&&row.phase===name),name);const value={phone,name,frames:frames.length,first:frames[0],last:frames.at(-1),minCooking:Math.min(...frames.map(row=>row.cooking)),blockedFrames:frames.filter(row=>row.disabled).length};results.push(value);return value;};
  try{
   await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha');
   if(phone&&await page.locator('.assistant-row').filter({hasText:'Synthetic alpha'}).isVisible())await page.locator('.assistant-row').filter({hasText:'Synthetic alpha'}).click();
   await page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&!document.querySelector('#pot-composer')?.disabled);
   const notNow=page.getByRole('button',{name:'Not now',exact:true});if(await notNow.count())await notNow.click();
   await page.evaluate(()=>navigator.serviceWorker.ready);pages.push(...await page.evaluate(()=>directoryTimeline));await page.reload();
   await page.waitForFunction(()=>!!navigator.serviceWorker.controller&&document.querySelector('.connection-status.is-connected'));
   await page.locator('#pot-composer').fill('Synthetic draft retained during directory refresh');await waitReady();
   const streamsBefore=fixture.trace.filter(row=>row.kind==='http-start'&&/\/(events|changes)$/.test(row.path)).length;
   await phase('healthy-heartbeat');await page.waitForTimeout(15500);const healthy=await mark('healthy-heartbeat');
   assert.equal(healthy.minCooking,3);assert.equal(healthy.blockedFrames,0);assert.equal(fixture.trace.filter(row=>row.kind==='http-start'&&/\/(events|changes)$/.test(row.path)).length,streamsBefore,'healthy heartbeats must not reconnect');
   await phase('ordinary-invalidation');await control({holdDirectory:true,directory:true});await blocked();await page.waitForTimeout(250);const invalidated=await mark('ordinary-invalidation');
   assert(invalidated.blockedFrames>0);assert.equal(invalidated.minCooking,baseline?0:3,'retained display must not invent execution completion');await noCommand();
   if(phone)await page.locator('.titlebar .narrow-navigation').click();
   if(output)await page.screenshot({path:resolve(output,`directory-${phone?'phone':'desktop'}-${baseline?'before':'after'}.png`)});
   await phase('directory-confirmed');await control({holdDirectory:false});
   if(phone)await page.locator('.assistant-row').filter({hasText:'Synthetic alpha'}).click();
   await waitReady();assert.equal((await sample()).cooking,3);
   await phase('fresh-execution-finished');await control({executing:false,directory:true});await page.waitForFunction(()=>!document.querySelector('.assistant-activity[data-state="cooking"]'));await mark('fresh-execution-finished');
   await control({executing:true,directory:true});await page.waitForFunction(()=>document.querySelectorAll('.assistant-activity[data-state="cooking"]').length===3);await waitReady();
   await phase('fresh-capability-removed');await control({sendAllowed:false,directory:true});await blocked();await page.waitForTimeout(200);assert.equal((await sample()).cooking,3);await noCommand();await mark('fresh-capability-removed');
   await control({sendAllowed:true,directory:true});await waitReady();
   for(const stream of ['changes','events']){
    await phase('reconnect-'+stream);await control({holdDirectory:true,holdChat:true,close:stream});await blocked();await page.waitForTimeout(250);await noCommand();await mark('reconnect-'+stream);
    await phase('directory-before-session-'+stream);await control({holdDirectory:false});await page.waitForTimeout(250);assert(await page.locator('.send-button').isDisabled(),'fresh directory alone cannot confirm Chat');await noCommand();
    await phase('session-confirmed-'+stream);await control({holdChat:false});await waitReady();assert.equal(await page.locator('#pot-composer').inputValue(),'Synthetic draft retained during directory refresh');await mark('session-confirmed-'+stream);
   }
   await phase('offline');await context.setOffline(true);await blocked();await page.waitForTimeout(200);await mark('offline');await noCommand();
   await context.setOffline(false);await waitReady();await mark('online-recovered');
   // A fresh document may display retained history while API verification is
   // unavailable, but must not animate old cached execution or enable commands.
   pages.push(...await page.evaluate(()=>directoryTimeline));await context.route('**/api/**',route=>route.abort());await context.route('**/access/status',route=>route.abort());await page.reload();
   await page.waitForFunction(()=>document.querySelector('.assistant-row'));
   await page.waitForTimeout(250);await phase('cold-retained');await page.waitForTimeout(100);assert.equal((await sample()).cooking,0);assert(await page.locator('.send-button').isDisabled());await mark('cold-retained');
   assert.deepEqual(errors,[]);
  }finally{
   if(output)await writeFile(resolve(output,`directory-timeline-${phone?'phone':'desktop'}.json`),JSON.stringify({baseline,phone,results:results.filter(row=>row.phone===phone),http:fixture.trace,ui:[...pages,...await page.evaluate(()=>directoryTimeline)],errors},null,2));
   await context.close();await fixture.close();
  }
 }
 return results;
}

if(process.argv[1]===import.meta.filename){
 const output=resolve(process.env.POT_CHAT_OUTPUT||'.pot-artifacts/directory-activity');await mkdir(output,{recursive:true});
 const source=await sourceServer({root:process.env.POT_CHAT_SOURCE||resolve('.')});
 if(process.argv.includes('--serve')){
  const fixture=await chatInteractionsFixture(source.origin);console.log(fixture.origin);
  process.once('SIGINT',async()=>{await fixture.close();await source.close();});
 }else{
  const browser=await launchBrowser();
  try{console.log(JSON.stringify(await chatDirectoryActivity(browser,{sourceOrigin:source.origin,baseline:process.argv.includes('--baseline'),output}),null,2));}
  finally{await browser.close();await source.close();}
 }
}
