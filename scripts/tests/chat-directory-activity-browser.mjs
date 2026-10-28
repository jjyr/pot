import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {chatInteractionsFixture} from './chat-interactions-fixture.mjs';
import {waitForOutbox} from './outbox-persistence-browser.mjs';

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
    const history=document.querySelector('.history[role="log"]:not([data-preparation-surface])');
    directoryTimeline.push({at:Date.now(),kind:'frame',phase:directoryPhase,cooking:document.querySelectorAll('.assistant-activity[data-state="cooking"]').length,disabled:document.querySelector('.send-button')?.disabled,local:document.querySelector('#app')?.classList.contains('is-local-startup'),status:document.querySelector('.connection-status')?.textContent,historyVisible:history&&getComputedStyle(history).visibility==='visible',scrollTop:history?.scrollTop});requestAnimationFrame(sample);
   };requestAnimationFrame(sample);
  });
  const control=async data=>assert((await context.request.post(fixture.origin+'/fixture/control',{data})).ok());
  const phase=name=>page.evaluate(name=>{window.directoryPhase=name;return new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));},name);
  const waitReady=()=>page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&!document.querySelector('.send-button')?.disabled);
  const sample=()=>page.evaluate(()=>directoryTimeline.findLast(row=>row.kind==='frame'));
  const composer=()=>page.locator('#pot-composer'),draft='Synthetic draft retained during directory refresh';
  const mutations=()=>fixture.trace.filter(row=>row.kind==='http-start'&&row.method==='POST'&&['/api/v1/messages','/api/v1/turn/steer'].includes(row.path));
  const waitTrace=async(predicate,label)=>{const until=Date.now()+12000;while(Date.now()<until){const row=fixture.trace.find(predicate);if(row)return row;await page.waitForTimeout(25);}throw Error('Missing fixture evidence: '+label);};
  const row=async(text,state)=>{const saved=await waitForOutbox(page,'([rows])=>rows.some(row=>row.request.text==='+JSON.stringify(text)+'&&row.state==='+JSON.stringify(state)+')');return saved.rows.find(row=>row.request.text===text);};
  const submit=async text=>{await composer().fill(text);assert(await page.locator('.send-button').isEnabled(),'known conversation accepts explicit local submission');await page.locator('.send-button').click();};
  const queued=async text=>{await submit(text);const saved=await row(text,'queued');assert.equal(saved.started,false);assert.deepEqual({host:saved.target.host,control:saved.target.control,space:saved.target.space,owner:saved.target.owner,conversation:saved.target.conversation},{host:'interaction-control',control:'interaction-control',space:'local',owner:'alpha',conversation:'alpha-conversation'});return saved;};
  // Hold the boundary across a full 2s dispatcher interval, not just the click.
  const noPost=async saved=>{await page.waitForTimeout(2200);assert.equal(mutations().filter(entry=>entry.body.request_id===saved.request.request_id).length,0,'unconfirmed authority cannot dispatch the durable submission');const current=await row(saved.request.text,'queued');assert.equal(current.started,false);assert.deepEqual(current.request,saved.request);assert.deepEqual(current.target,saved.target);};
  const accepted=async saved=>{const current=await row(saved.request.text,'accepted');assert.deepEqual(current.target,saved.target);assert.equal(current.request.request_id,saved.request.request_id);if(saved.request.user_message_id)assert.deepEqual(current.request,saved.request);assert.equal(current.request.user_message_id,saved.request.user_message_id);const posts=mutations().filter(entry=>entry.body.request_id===saved.request.request_id);assert.equal(posts.length,1,'one request reaches the fixture after authority confirmation');assert.equal(posts[0].path,saved.request.user_message_id?'/api/v1/turn/steer':'/api/v1/messages');assert.equal(posts[0].owner,saved.target.owner);assert.equal(posts[0].body.text,saved.request.text);assert.equal(posts[0].query.expected_control_id,saved.target.control);assert.equal(posts[0].query.space_id,saved.target.space);assert.equal(current.request.conversation_id,saved.target.conversation);if(!saved.request.user_message_id)assert.equal(posts[0].body.conversation_id,saved.target.conversation);assert.equal(posts[0].body.user_message_id,saved.request.user_message_id);return current;};
  const restoreDraft=async()=>{await composer().fill(draft);await composer().evaluate(node=>{window.retainedDirectoryComposer=node;node.focus();node.setSelectionRange(4,12);});};
  const assertDraft=async()=>assert.deepEqual(await composer().evaluate(node=>({same:node===window.retainedDirectoryComposer,focused:document.activeElement===node,text:node.value,start:node.selectionStart,end:node.selectionEnd})),{same:true,focused:true,text:draft,start:4,end:12});
  const cancel=async text=>{await page.getByRole('button',{name:'Saved messages',exact:true}).click();await page.locator('.outbox-dialog article.message').filter({hasText:text}).getByRole('button',{name:'Cancel queued message',exact:true}).click();await row(text,'cancelled');await page.locator('.outbox-dialog').getByRole('button',{name:'Close',exact:true}).click();};
  const mark=async name=>{const frames=await page.evaluate(name=>directoryTimeline.filter(row=>row.kind==='frame'&&row.phase===name),name);const value={phone,name,frames:frames.length,first:frames[0],last:frames.at(-1),minCooking:Math.min(...frames.map(row=>row.cooking)),blockedFrames:frames.filter(row=>row.disabled).length};results.push(value);return value;};
  try{
   await control({turnID:'alpha-799'});
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
   await restoreDraft();
   await phase('ordinary-invalidation');let start=fixture.trace.length;await control({holdDirectory:true,directory:true});await waitTrace((entry,index)=>index>=start&&entry.kind==='http-start'&&entry.path==='/api/v1/assistants','held directory request');await page.waitForTimeout(250);const invalidated=await mark('ordinary-invalidation');
   assert.equal(invalidated.minCooking,baseline?0:3,'retained display must not invent execution completion');await assertDraft();
   const guidance=await queued('Queued guidance during directory refresh');assert.equal(guidance.request.user_message_id,'alpha-799');await noPost(guidance);await restoreDraft();
   if(output)await page.screenshot({path:resolve(output,`directory-${phone?'phone':'desktop'}-${baseline?'before':'after'}.png`)});
   await phase('directory-confirmed');await control({turnID:'alpha-797',publish:true,holdDirectory:false});await accepted(guidance);await waitReady();assert.equal((await sample()).cooking,3);await assertDraft();
   await phase('fresh-execution-finished');await control({executing:false,directory:true});await page.waitForFunction(()=>!document.querySelector('.assistant-activity[data-state="cooking"]'));await mark('fresh-execution-finished');
   await control({executing:true,directory:true});await page.waitForFunction(()=>document.querySelectorAll('.assistant-activity[data-state="cooking"]').length===3);await waitReady();
   await phase('fresh-capability-removed');start=fixture.trace.length;await control({sendAllowed:false,directory:true});await waitTrace((entry,index)=>index>=start&&entry.kind==='http-response'&&entry.path==='/api/v1/assistants'&&!entry.sendAllowed,'confirmed revoked directory');
   await submit('Known revoked permission retains local submission');const revoked=await row('Known revoked permission retains local submission','failed');assert.equal(revoked.problem,'permission');assert.equal(revoked.started,false);await page.waitForTimeout(2200);assert.equal(mutations().filter(entry=>entry.body.request_id===revoked.request.request_id).length,0,'known revoked permission performs zero POST');assert.equal((await sample()).cooking,3);await mark('fresh-capability-removed');await cancel(revoked.request.text);
   await control({sendAllowed:true,directory:true});await restoreDraft();await waitReady();
   for(const stream of ['changes','events']){
    await phase('reconnect-'+stream);start=fixture.trace.length;await control({holdDirectory:true,holdChat:true,close:stream});await waitTrace((entry,index)=>index>=start&&entry.kind==='http-start'&&entry.path==='/api/v1/assistants','directory requalification after '+stream+' reconnect');
    const pending=await queued('Queued after '+stream+' reconnect');await noPost(pending);await restoreDraft();await mark('reconnect-'+stream);
    await phase('directory-before-session-'+stream);await control({holdDirectory:false});await waitTrace((entry,index)=>index>=start&&entry.kind==='snapshot-held','fresh outbox snapshot held after '+stream+' reconnect');await noPost(pending);await assertDraft();
    await phase('session-confirmed-'+stream);await control({holdChat:false});await accepted(pending);await waitReady();await assertDraft();await mark('session-confirmed-'+stream);
   }
   await phase('offline');await context.setOffline(true);const offline=await queued('Queued while browser offline');await noPost(offline);await restoreDraft();await mark('offline');
   await phase('online-recovered');await context.setOffline(false);await accepted(offline);await waitReady();await assertDraft();await mark('online-recovered');
   // Retained history supports local submission after reload, but cached
   // execution never animates and unconfirmed transport remains blocked.
   pages.push(...await page.evaluate(()=>directoryTimeline));await context.route('**/api/**',route=>route.abort());await context.route('**/access/status',route=>route.abort());await page.reload();
   await page.waitForFunction(()=>document.querySelector('.assistant-row'));await composer().waitFor();
   await page.waitForTimeout(250);await phase('cold-retained');await page.waitForTimeout(100);assert.equal((await sample()).cooking,0);const cold=await queued('Queued from cold retained conversation');await noPost(cold);await mark('cold-retained');
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
