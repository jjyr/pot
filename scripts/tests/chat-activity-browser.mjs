import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {chatActivityFixture} from './chat-activity-fixture.mjs';

const baseline=process.argv.includes('--baseline');
const output=resolve(process.env.POT_ACTIVITY_OUTPUT||'.pot-artifacts/chat-activity');
await mkdir(output,{recursive:true});
const source=await sourceServer({root:process.env.POT_ACTIVITY_SOURCE||resolve('.')}),browser=await launchBrowser(),results=[];
try{
 for(const phone of [false,true])for(const intent of ['latest','reading']){
  const fixture=await chatActivityFixture(source.origin),context=await browser.newContext({viewport:phone?{width:390,height:852}:{width:1280,height:800},isMobile:phone,hasTouch:phone,serviceWorkers:'block'}),page=await context.newPage(),errors=[];
  page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message));
  await context.route('**/*',route=>new URL(route.request().url()).origin===fixture.origin?route.continue():route.abort());
  await context.addInitScript(()=>{
   window.activitySamples=[];window.activityPhase='startup';window.activityRecording=false;
   const ids=new WeakMap();let next=0;
   const id=node=>{if(!node)return null;if(!ids.has(node))ids.set(node,++next);return ids.get(node);};
   window.activitySample=()=>{
    const h=document.querySelector('.history:not([data-preparation-surface])'),box=h?.getBoundingClientRect(),rows=[...h?.querySelectorAll('article.message')||[]];
    const row=h?.querySelector('[data-window-key="activity:active-request"]')||h?.querySelector('[data-window-key="reply:active-request"]'),status=row?.querySelector('.reply-status'),line=status?.firstElementChild,r=row?.getBoundingClientRect();
    const first=rows.find(n=>n.getBoundingClientRect().bottom>box?.top&&n.getBoundingClientRect().top<box?.bottom);
    return {phase:activityPhase,at:Date.now(),visible:!!h&&getComputedStyle(h).visibility==='visible'&&!document.querySelector('#app.show-directory'),following:document.querySelector('.latest-button')?.hidden,scrollTop:h?.scrollTop,extent:h?.scrollHeight,first:first?.dataset.windowKey,firstTop:first?.getBoundingClientRect().top,row:id(row),bubble:id(row?.querySelector('.reply-bubble')),status:id(status),line:id(line),height:r?.height,statusHeight:status?.getBoundingClientRect().height,hidden:status?.hidden,icons:[...status?.querySelector('.reply-activity-icon')?.children||[]].filter(node=>getComputedStyle(node).display!=='none').length,text:status?.textContent,inView:!!r&&r.top>=box.top&&r.bottom<=box.bottom,rows:rows.map(n=>({key:n.dataset.windowKey,id:n.dataset.messageId,node:id(n),height:n.getBoundingClientRect().height,text:n.querySelector('.reply-content')?.textContent,activity:n.classList.contains('is-activity')}))};
   };
   const tick=()=>{if(activityRecording)activitySamples.push(activitySample());requestAnimationFrame(tick);};requestAnimationFrame(tick);
  });
  // Read the actual committed cache cursor. An async waitForFunction predicate
  // would succeed on its Promise without waiting for the IndexedDB result.
  const committed=async revision=>page.evaluate(async revision=>{
   const deadline=performance.now()+12000;
   while(performance.now()<deadline){
    const views=await new Promise((resolve,reject)=>{const open=indexedDB.open('pot-chat-cache');open.onerror=()=>reject(open.error);open.onsuccess=()=>{const db=open.result,tx=db.transaction('views'),read=tx.objectStore('views').getAll();let values;read.onsuccess=()=>values=read.result;tx.onerror=()=>reject(tx.error);tx.oncomplete=()=>{db.close();resolve(values);};};});
    if(views.some(view=>view.conversation_id==='alpha-conversation'&&Number(view.cursor)>=revision))return;
    await new Promise(requestAnimationFrame);
   }
   throw Error('No committed conversation cursor '+revision);
  },revision);
  const frames=()=>page.evaluate(async()=>{for(let i=0;i<8;i++)await new Promise(requestAnimationFrame);return activitySample();});
  const control=async data=>{assert((await context.request.post(fixture.origin+'/fixture/control',{data})).ok());return (await(await context.request.get(fixture.origin+'/fixture/state')).json()).revision;};
  const phase=name=>page.evaluate(name=>window.activityPhase=name,name);
  const checkStable=(before,after,label)=>{if(baseline)return;assert(after.visible,label+': history readable');for(const key of ['row','bubble','status','line'])assert.equal(after[key],before[key],label+': stable '+key);assert.equal(after.hidden,false,label+': visible activity');assert.equal(after.icons,1,label+': one activity icon');assert(Math.abs(after.height-before.height)<0.5,label+': fixed activity height');assert(Math.abs(after.statusHeight-before.statusHeight)<0.5,label+': fixed status frame');};
  const marks=[];
  try{
   await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha');
   await page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&document.querySelector('.waiting-dots')&&getComputedStyle(document.querySelector('.history:not([data-preparation-surface])')).visibility==='visible');
   const dismiss=page.getByRole('button',{name:'Not now',exact:true});if(await dismiss.count())await dismiss.click();
   await committed(1);await frames();
   if(intent==='reading'){const box=await page.locator('.history:not([data-preparation-surface])').boundingBox();await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.wheel(0,-120);await page.waitForFunction(()=>document.querySelector('.latest-button')?.hidden===false);await frames();}
   await page.evaluate(()=>window.activityRecording=true);await phase('thinking');const initial=await frames();marks.push(initial);
   await phase('reconnecting');await control({holdEvents:'alpha',close:'events'});
   await page.waitForFunction(()=>document.querySelector('.connection-status')?.textContent.toLowerCase().includes('reconnect'));
   const reconnect=await frames();marks.push(reconnect);checkStable(initial,reconnect,'reconnect');
   if(!baseline)assert(reconnect.text==='Connection interrupted; confirming status','Activity truthfully reports reconnecting');
   if(intent==='latest')await page.screenshot({path:output+`/reconnect-${phone?'phone':'desktop'}-${baseline?'before':'after'}.png`});
   if(!baseline){
    await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}})));
    await page.locator('.is-activity .reply-status').getByText('连接中断，正在确认状态',{exact:true}).waitFor();
    checkStable(initial,await frames(),'pending locale change');
    const fit=await page.locator('.is-activity .reply-status').evaluate(node=>node.scrollHeight<=node.clientHeight+1);
    assert(fit,'Narrow translated nonterminal label fits the fixed frame');
    await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'en'}})));
    await page.locator('.is-activity .reply-status').getByText('Connection interrupted; confirming status',{exact:true}).waitFor();
   }
   await phase('restored');await control({holdEvents:null});await page.waitForFunction(()=>!!document.querySelector('.connection-status.is-connected'));checkStable(initial,await frames(),'restored');
   for(const name of ['prose','round-empty','round-tool','round-settling','round-next','long-progress','parallel-start','parallel-next','terminal-after-prose','terminal-empty','partial-failure']){
    await phase(name);const revision=await control({scenario:name});await committed(revision);const state=await frames();marks.push(state);
    if(['prose','round-empty','round-tool','round-settling','round-next','long-progress','parallel-start'].includes(name))checkStable(initial,state,name);
    if(!baseline&&intent==='latest'&&['long-progress','parallel-start'].includes(name))assert(state.inView,name+': current activity remains visible at Latest');
    if(!baseline&&name==='parallel-start'){
     assert(state.rows.some(row=>row.key==='activity:parallel-request'),'Queued input has its own activity');
     assert(state.rows.findIndex(row=>row.id==='parallel-input')<state.rows.findIndex(row=>row.id==='active-answer'),'Nonempty message order retained');
     await page.evaluate(()=>window.parallelActivity=document.querySelector('[data-window-key="activity:parallel-request"]'));
    }
    if(!baseline&&name==='parallel-next'){
     assert(!state.rows.some(row=>row.key==='activity:active-request'),'Completion clears only the first activity');
     assert(await page.evaluate(()=>parallelActivity===document.querySelector('[data-window-key="activity:parallel-request"]')),'Queued to running retains the second activity node');
    }
    if(!baseline&&name==='terminal-after-prose')assert(state.rows.some(row=>row.text==='A persistent progress paragraph.')&&!state.rows.some(row=>row.activity),'Success preserves prose and retires activity');
    if(!baseline&&name==='terminal-empty')assert(!state.rows.some(row=>row.id==='active-answer'||row.activity),'Silent terminal does not invent prose');
    if(!baseline&&name==='partial-failure'){
     assert(state.rows.some(row=>row.text==='A real partial answer.'),'Failure retains decoded partial prose');
     assert.equal(state.rows.filter(row=>row.activity).length,1,'Failure has one independent diagnostic');
     assert.equal(await page.locator('.is-failed').count(),1,'Prose does not duplicate failure styling');
    }
    if(!baseline&&intent==='reading'&&name!=='partial-failure')assert.equal(state.following,false,name+': reading intent retained');
   }
   if(!baseline&&intent==='reading')for(const state of marks.filter(s=>s.phase!=='partial-failure')){assert.equal(state.first,initial.first,state.phase+': reading anchor identity');assert(Math.abs(state.firstTop-initial.firstTop)<2,state.phase+': reading anchor offset');}
   // Retire the old owner while its stream has no delivery. A late old-owner
   // snapshot must not contaminate another conversation or its draft.
   await control({holdEvents:'alpha'});
   if(phone)await page.locator('.titlebar .narrow-navigation').click();
   await page.locator('.assistant-row').filter({has:page.locator('.assistant-title',{hasText:'Synthetic short'})}).click();
   await page.waitForFunction(()=>document.querySelector('.assistant-row.is-selected .assistant-title')?.textContent==='Synthetic short'&&document.querySelector('[data-message-id="short-3"]'));
   await page.locator('#pot-composer').fill('Keep this scoped draft');
   await control({scenario:'round-next',holdEvents:null});await frames();
   assert.equal(await page.locator('[data-window-key="activity:active-request"]').count(),0,'Retired scope cannot publish activity');
   assert.equal(await page.locator('#pot-composer').inputValue(),'Keep this scoped draft');
   assert.equal(fixture.trace.filter(event=>event.path==='/api/v1/messages'&&event.method==='POST').length,0);
   assert.deepEqual(errors,[]);
   const samples=await page.evaluate(()=>activitySamples);
   if(!baseline){const activePhases=new Set(['thinking','reconnecting','restored','prose','round-empty','round-tool','round-settling','round-next','long-progress','parallel-start']);for(const state of samples.filter(s=>activePhases.has(s.phase)))assert(state.row&&state.status&&!state.hidden,'No nonterminal activity disappearance at '+state.phase);}
   results.push({phone,intent,baseline,marks,errors,posts:0});
   await writeFile(output+`/${phone?'phone':'desktop'}-${intent}.json`,JSON.stringify({samples,http:fixture.trace,errors},null,2));
  }catch(error){await writeFile(output+`/${phone?'phone':'desktop'}-${intent}-failure.json`,JSON.stringify({error:error.stack,samples:await page.evaluate(()=>activitySamples).catch(()=>[]),http:fixture.trace,errors},null,2));throw error;}
  finally{await context.close();await fixture.close();}
 }
 await writeFile(output+'/summary.json',JSON.stringify(results,null,2));
 console.log(JSON.stringify({passed:true,baseline,cases:results.length,errors:results.flatMap(result=>result.errors),posts:0,output}));
}finally{await browser.close();await source.close();}
