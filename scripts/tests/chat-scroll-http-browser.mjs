import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {chatScrollHTTPFixture} from './chat-scroll-http-fixture.mjs';
import {waitForDraftSaved} from './draft-persistence-browser.mjs';

const baseline=process.argv.includes('--baseline');
const output=resolve(process.env.POT_CHAT_OUTPUT||'.pot-artifacts/chat-scroll-http');await mkdir(output,{recursive:true});
const source=await sourceServer({root:process.env.POT_CHAT_SOURCE||resolve('.')}),browser=await launchBrowser();
const results=[];
try{
 for(const phone of [false,true]){
  const fixture=await chatScrollHTTPFixture(source.origin),context=await browser.newContext({viewport:phone?{width:390,height:852}:{width:1280,height:800},hasTouch:phone,isMobile:phone,serviceWorkers:'block'}),page=await context.newPage(),errors=[],timelines=[];
  page.setDefaultTimeout(10000);page.on('pageerror',error=>errors.push(error.message));
  await context.addInitScript(()=>{
   window.timeline=[];window.phase='startup';
   const complete=Object.getOwnPropertyDescriptor(IDBTransaction.prototype,'oncomplete');
   Object.defineProperty(IDBTransaction.prototype,'oncomplete',{...complete,set(handler){complete.set.call(this,function(event){
    if(this.objectStoreNames.contains('composerDrafts')){
     timeline.push({at:Date.now(),kind:'idb-complete',mode:this.mode,phase});
     if(window.holdDraft&&this.mode===window.holdDraft){window.releaseDraft=()=>{window.holdDraft=null;handler.call(this,event);};return;}
    }
    return handler.call(this,event);
   });}});
   const sample=()=>{
    const h=document.querySelector('.history:not([data-preparation-surface])'),box=h?.getBoundingClientRect(),rows=[...document.querySelectorAll('article.message')];
    const first=rows.find(row=>row.getBoundingClientRect().bottom>box?.top&&row.getBoundingClientRect().top<box?.bottom);
    timeline.push({at:Date.now(),kind:'frame',phase,owner:document.querySelector('.assistant-row.is-selected .assistant-title')?.textContent,visible:!!h&&getComputedStyle(h).visibility==='visible'&&!document.querySelector('#app.show-directory'),top:h?.scrollTop,height:h?.clientHeight,extent:h?.scrollHeight,first:first?.dataset.messageId,firstTop:first?.getBoundingClientRect().top,anchor:first?.dataset.scrollAnchor,geometry:rows.filter(row=>row.getBoundingClientRect().bottom>box?.top&&row.getBoundingClientRect().top<box?.bottom).map(row=>({id:row.dataset.messageId,top:row.getBoundingClientRect().top,height:row.getBoundingClientRect().height})),disabled:document.querySelector('.send-button')?.disabled,connection:document.querySelector('.connection-status')?.textContent,draft:document.querySelector('#pot-composer')?.value});
    requestAnimationFrame(sample);
   };requestAnimationFrame(sample);
  });
  const control=async data=>assert((await context.request.post(fixture.origin+'/fixture/control',{data})).ok());
  const phase=name=>page.evaluate(name=>window.phase=name,name);
  const ready=()=>page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&document.querySelector('.history:not([data-preparation-surface])')?.style.visibility!=='hidden'&&!document.querySelector('#pot-composer')?.disabled);
  const switchOwner=async owner=>{if(phone)await page.locator('.titlebar .narrow-navigation').click();await page.locator('.assistant-row').filter({has:page.locator('.assistant-title',{hasText:'Synthetic '+owner})}).click();await page.waitForFunction(owner=>document.querySelector('.assistant-row.is-selected .assistant-title')?.textContent==='Synthetic '+owner&&[...document.querySelectorAll('article.message')].some(row=>row.dataset.messageId.startsWith(owner+'-')),owner);};
  const mark=async name=>{const samples=await page.evaluate(name=>timeline.filter(row=>row.kind==='frame'&&row.phase===name),name);const last=samples.at(-1);results.push({phone,name,first:samples[0],last,visibleFrames:samples.filter(row=>row.visible).length});return samples;};
  const sendVisible=async text=>{const row=page.locator('article.message').filter({hasText:text});return await row.count()?row.evaluate(row=>{const r=row.getBoundingClientRect(),h=document.querySelector('.history:not([data-preparation-surface])').getBoundingClientRect();return r.top>=h.top&&r.bottom<=h.bottom;}):false;};
  try{
   let releaseFont;
   await page.route('**/assets/IBMPlexSans-Regular.ttf',route=>new Promise(resolve=>{releaseFont=async()=>{await route.continue();resolve();};}));
   await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha',{waitUntil:'domcontentloaded'});
   await page.waitForFunction(()=>document.fonts.status==='loading'&&document.querySelector('.assistant-row.is-selected'));
   await phase('font-pending');await page.waitForTimeout(250);assert(releaseFont);assert.equal(await page.locator('.history:not([data-preparation-surface])').evaluate(h=>getComputedStyle(h).visibility),'hidden');await releaseFont();await page.unroute('**/assets/IBMPlexSans-Regular.ttf');await ready();
   const notNow=page.getByRole('button',{name:'Not now',exact:true});if(await notNow.count())await notNow.click();
   await phase('send-bottom');await page.locator('#pot-composer').fill('User send from Latest');await page.getByRole('button',{name:'Send',exact:true}).click();await page.waitForTimeout(500);assert(await sendVisible('User send from Latest'));
   await page.locator('#pot-composer').fill('');
   const box=await page.locator('.history:not([data-preparation-surface])').boundingBox();await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.wheel(0,-2400);await page.waitForTimeout(350);
   await phase('background-reading');await control({background:true});await page.waitForTimeout(500);const background=await mark('background-reading');
   assert(Math.abs(background.at(-1).firstTop-background[0].firstTop)<2&&background.at(-1).first===background[0].first,'background reset preserves reading');
   await phase('send-history');await page.locator('#pot-composer').fill('User send while reading old history');await page.getByRole('button',{name:'Send',exact:true}).click();await page.waitForTimeout(700);await ready();
   const visible=await sendVisible('User send while reading old history');
   results.push({phone,name:'send-history',visible});if(!baseline)assert(visible,'explicit send must expose the submitted row');
   await page.screenshot({path:output+`/send-${phone?'phone':'desktop'}-${baseline?'before':'after'}.png`});
   await mark('send-history');
   await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.wheel(0,-1600);await page.waitForTimeout(200);
   await phase('send-preparing');await page.locator('#pot-composer').fill('Send with delayed durable fence');await waitForDraftSaved(page);await page.evaluate(()=>window.holdDraft='readwrite');await page.getByRole('button',{name:'Send',exact:true}).click();await page.waitForFunction(()=>!!window.releaseDraft);await page.waitForTimeout(150);
   const preparingVisible=await sendVisible('Send with delayed durable fence');results.push({phone,name:'send-preparing',visible:preparingVisible});if(!baseline)assert(preparingVisible);
   await switchOwner('short');await ready();await phase('old-send-new-owner');const position=await page.locator('.history:not([data-preparation-surface])').evaluate(h=>h.scrollTop);await page.evaluate(()=>window.releaseDraft());await page.waitForTimeout(400);assert.equal(await page.locator('.history:not([data-preparation-surface])').evaluate(h=>h.scrollTop),position);assert.equal(await page.locator('article.message').filter({hasText:'Send with delayed durable fence'}).count(),0);
   await switchOwner('beta');await ready();await page.locator('#pot-composer').fill(Array.from({length:10},(_,i)=>'Persisted beta draft line '+i).join('\n'));await waitForDraftSaved(page);
   await switchOwner('alpha');await ready();timelines.push(...await page.evaluate(()=>timeline));await page.reload();await ready();
   await page.evaluate(()=>window.holdDraft='readonly');await phase('switch-draft-pending');await switchOwner('beta');await page.waitForFunction(()=>!!window.releaseDraft);await page.waitForTimeout(250);
   const before=await mark('switch-draft-pending');
   if(!baseline)assert(before.filter(row=>row.owner==='Synthetic beta').every(row=>!row.visible),'draft hydrate belongs to hidden preparation');
   await phase('switch-draft-release');await page.evaluate(()=>window.releaseDraft());await ready();await page.waitForTimeout(350);const after=await mark('switch-draft-release');
   const shown=after.filter(row=>row.visible&&row.owner==='Synthetic beta'&&row.draft.includes('Persisted beta'));
   results.push({phone,name:'switch-draft-motion',visibleTopRange:Math.max(...shown.map(row=>row.top))-Math.min(...shown.map(row=>row.top))});
   if(!baseline)assert(Math.max(...shown.map(row=>row.top))-Math.min(...shown.map(row=>row.top))<2,'first visible draft geometry is final');
   await switchOwner('short');await ready();await switchOwner('alpha');await ready();
   const reading=()=>page.evaluate(()=>{const h=document.querySelector('.history:not([data-preparation-surface])'),top=h.getBoundingClientRect().top,row=[...h.querySelectorAll('article.message')].find(row=>row.getBoundingClientRect().bottom>top);return {id:row?.dataset.messageId,offset:row?.getBoundingClientRect().top-top};});
   const wheelUp=async amount=>{const box=await page.locator('.history:not([data-preparation-surface])').boundingBox();await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.wheel(0,-amount);await page.waitForTimeout(180);};
   // Traverse enough pages to leave the 640-message data window, then send.
   await phase('send-evicted-history');
   for(let i=0;i<32;i++){await wheelUp(100000);const current=await reading();if(Number(current.id?.split('-').at(-1))<150)break;}
   const old=await reading();assert(Number(old.id?.split('-').at(-1))<160,'fixture must reach evicted history');
   await page.locator('#pot-composer').fill('Send from evicted history');await page.getByRole('button',{name:'Send',exact:true}).click();await ready();await page.waitForTimeout(350);
   const evictedVisible=await sendVisible('Send from evicted history');results.push({phone,name:'send-evicted-history',old,visible:evictedVisible});if(!baseline)assert(evictedVisible);
   // A new deliberate reading gesture wins over the earlier send, including
   // a real stream reconnect and a delayed HTTP acceptance.
   await phase('send-reconnect-reading');await control({holdSend:true});
   await page.locator('#pot-composer').fill('Delayed send and reconnect');const delayedPost=page.waitForRequest(request=>request.method()==='POST'&&new URL(request.url()).pathname==='/api/v1/messages');await page.getByRole('button',{name:'Send',exact:true}).click();await delayedPost;await page.waitForTimeout(150);
   if(!baseline)assert(await sendVisible('Delayed send and reconnect'));
   await wheelUp(1500);const beforeReceipt=await reading();await control({close:'events'});await control({holdSend:false});await ready();await page.waitForTimeout(350);const afterReceipt=await reading();
   assert.equal(afterReceipt.id,beforeReceipt.id);assert(Math.abs(afterReceipt.offset-beforeReceipt.offset)<2,'late receipt/reconnect must preserve subsequent reading');
   results.push({phone,name:'send-reconnect-reading',beforeReceipt,afterReceipt});
   // Save/restore historical reading across cached and short conversations.
   await phase('cached-reading');const saved=await reading();await switchOwner('short');await ready();await switchOwner('alpha');await ready();await page.waitForTimeout(200);const restored=await reading();
   assert.equal(restored.id,saved.id);assert(Math.abs(restored.offset-saved.offset)<2);
   // Retire a delayed owner subscription before its late frames arrive.
   await phase('rapid-beta-alpha');await control({holdEvents:'beta'});await switchOwner('beta');await switchOwner('alpha');await ready();const rapid=await reading();await control({holdEvents:null});await page.waitForTimeout(350);const late=await reading();
   assert.equal(late.id,rapid.id);assert(Math.abs(late.offset-rapid.offset)<2);assert.equal(await page.locator('article.message[data-message-id^="beta-"]').count(),0);
   results.push({phone,name:'cached-reading-and-rapid-switch',saved,restored,rapid,late});
   await phase('old-post-new-owner');await control({holdSend:true});await page.locator('#pot-composer').fill('Old owner delayed POST');const oldPost=page.waitForRequest(request=>request.method()==='POST'&&new URL(request.url()).pathname==='/api/v1/messages');await page.getByRole('button',{name:'Send',exact:true}).click();await oldPost;
   await switchOwner('short');await ready();const shortPosition=await reading();await control({holdSend:false});await page.waitForTimeout(350);assert.deepEqual(await reading(),shortPosition);assert.equal(await page.locator('article.message').filter({hasText:'Old owner delayed POST'}).count(),0);
   await phase('media-pending');await control({holdImages:true});await switchOwner('media');await page.waitForFunction(()=>!!document.querySelector('pot-resource[data-state="loading"]'));await page.waitForTimeout(250);
   assert.equal(await page.locator('.history:not([data-preparation-surface])').evaluate(h=>getComputedStyle(h).visibility),'hidden');await control({holdImages:false});await ready();await page.locator('pot-resource[data-state="ready"]').waitFor();await phase('media-visible');await page.waitForTimeout(300);
   const media=await mark('media-visible');assert(media.length>2&&media.every(row=>row.visible));assert(Math.max(...media.map(row=>row.top))-Math.min(...media.map(row=>row.top))<2);
   await switchOwner('short');await ready();await control({holdImages:true});await phase('media-timeout');const timeoutStarted=Date.now();await switchOwner('media');await ready();const fallbackMs=Date.now()-timeoutStarted;assert(fallbackMs>=4900,'use the product five-second deadline');
   assert(await page.locator('.initial-layout-slot').count(),'five-second fallback reserves the stalled preview');const slot=await reading();await control({holdImages:false});await page.waitForTimeout(350);assert.deepEqual(await reading(),slot);
   results.push({phone,name:'font-media-and-fixed-slot',visibleRange:Math.max(...media.map(row=>row.top))-Math.min(...media.map(row=>row.top)),fallbackMs,slot});
   assert.deepEqual(errors,[]);
  }finally{
   await writeFile(output+`/timeline-${phone?'phone':'desktop'}.json`,JSON.stringify({results:results.filter(row=>row.phone===phone),http:fixture.trace,ui:[...timelines,...await page.evaluate(()=>timeline)],errors},null,2));
   await context.close();await fixture.close();
  }
 }
 console.log(JSON.stringify(results,null,2));
}finally{await browser.close();await source.close();}
