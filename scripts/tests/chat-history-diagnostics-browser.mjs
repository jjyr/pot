import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {chatActivityFixture} from './chat-activity-fixture.mjs';

const baseline=process.argv.includes('--baseline'),output=resolve(process.env.POT_HISTORY_OUTPUT||'.pot-artifacts/chat-history-diagnostics');
await mkdir(output,{recursive:true});
const source=await sourceServer({root:process.env.POT_HISTORY_SOURCE||resolve('.')}),browser=await launchBrowser(),results=[];
try{
 for(const phone of [false,true]){
  const fixture=await chatActivityFixture(source.origin),context=await browser.newContext({viewport:phone?{width:390,height:852}:{width:1280,height:800},isMobile:phone,hasTouch:phone,serviceWorkers:'block'}),page=await context.newPage(),errors=[];
  page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message));
  await context.route('**/*',route=>new URL(route.request().url()).origin===fixture.origin?route.continue():route.abort());
  await context.addInitScript(()=>{
   window.historyDiagnosticFrames=[];window.historyDiagnosticPhase='initial';
   const sample=()=>{const h=document.querySelector('.history:not([data-preparation-surface])'),box=h?.getBoundingClientRect();historyDiagnosticFrames.push({phase:historyDiagnosticPhase,at:Date.now(),top:h?.scrollTop,extent:h?.scrollHeight,visible:!!h&&getComputedStyle(h).visibility==='visible',rows:['alpha-690','alpha-692','alpha-694'].map(id=>{const row=h?.querySelector(`[data-message-id="${id}"]`),status=row?.querySelector('.reply-status'),r=row?.getBoundingClientRect();return {id,key:row?.dataset.windowKey,hidden:status?.hidden,status:status?.textContent,text:row?.querySelector('.reply-content')?.textContent,height:r?.height,top:r?.top,inView:!!r&&r.bottom>box.top&&r.top<box.bottom,failed:!!row?.querySelector('.is-failed')};})});requestAnimationFrame(sample);};requestAnimationFrame(sample);
  });
  const ready=()=>page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&document.querySelector('.history:not([data-preparation-surface])')&&getComputedStyle(document.querySelector('.history:not([data-preparation-surface])')).visibility==='visible');
  const sample=()=>page.evaluate(()=>historyDiagnosticFrames.at(-1));
  const verify=async()=>{
   const state=await sample();
   if(!baseline){
    for(const id of ['alpha-690','alpha-692']){
     const row=page.locator(`[data-message-id="${id}"]`);assert.equal(await row.locator('.reply-status').isVisible(),true,id+': history failure remains visible');
     assert.equal(await row.locator('.is-failed').count(),1,id+': one failure presentation');
     assert.equal(await row.getByText('Reply failed',{exact:true}).count(),1);
     assert.equal(await row.locator('.reply-error-details pre').textContent(),id==='alpha-690'?'Synthetic historical HTTP 404':'Synthetic historical empty failure');
    }
    assert.equal(await page.locator('[data-message-id="alpha-690"] .reply-content').innerText(),'Historical partial answer.');
    assert.equal(await page.locator('[data-message-id="alpha-692"] .reply-content').isVisible(),false,'Empty history failure does not invent prose');
    await page.locator('[data-message-id="alpha-694"] .reply-status').getByText('Reply stopped',{exact:true}).waitFor();
    assert.equal(await page.locator('[data-message-id="alpha-694"] .reply-content').innerText(),'Historical cancelled answer.');
   }
   return state;
  };
  try{
   await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha');await ready();
   const dismiss=page.getByRole('button',{name:'Not now',exact:true});if(await dismiss.count())await dismiss.click();
   assert.equal(await page.locator('[data-message-id="alpha-690"]').count(),0,'Historical outcome is absent from initial reset');
   await page.evaluate(()=>window.historyDiagnosticPhase='paging');
   const reachHistory=async()=>{
   for(let i=0;i<40;i++){
    const box=await page.locator('.history:not([data-preparation-surface])').boundingBox();await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.wheel(0,-600);
    const reached=await page.evaluate(async()=>{for(let f=0;f<6;f++)await new Promise(requestAnimationFrame);const row=document.querySelector('[data-message-id="alpha-690"]');if(!row)return false;const r=row.getBoundingClientRect(),h=document.querySelector('.history:not([data-preparation-surface])').getBoundingClientRect();return r.bottom>h.top&&r.top<h.bottom;});
    if(reached)break;
   }
   };
   await reachHistory();
   await page.locator('[data-message-id="alpha-690"]').waitFor();const paged=await verify();
   assert(fixture.trace.some(row=>row.kind==='history-response'&&row.query.includes('before=')),'Outcome arrived through actual HTTP pagination');
   await page.screenshot({path:output+`/history-${phone?'phone':'desktop'}-${baseline?'before':'after'}.png`});
   // Wait for the actual saved view transaction, not merely a queued write.
   const cached=await page.evaluate(async()=>{
    const deadline=performance.now()+12000;
    while(performance.now()<deadline){
     const data=await new Promise((resolve,reject)=>{const open=indexedDB.open('pot-chat-cache');open.onerror=()=>reject(open.error);open.onsuccess=()=>{const db=open.result,tx=db.transaction(['views','conversations','startup']),data={};for(const name of ['views','conversations','startup']){const read=tx.objectStore(name).getAll();read.onsuccess=()=>data[name]=read.result;}tx.oncomplete=()=>{db.close();resolve(data);};};});
     const view=data.views.find(view=>view.conversation_id==='alpha-conversation'&&view.position?.mode==='reading'&&view.window.some(row=>row.id==='alpha-690'));
     if(view&&data.startup.some(value=>value.owner==='alpha')&&Math.abs((view.position.position?.top??-1)-document.querySelector('.history:not([data-preparation-surface])').scrollTop)<2)return {view,replies:data.conversations.find(row=>row.projection?.conversation_id==='alpha-conversation')?.replies};
     await new Promise(requestAnimationFrame);
    }
    throw Error('Historical reading view did not commit');
   });
   await writeFile(output+`/${phone?'phone':'desktop'}-cached-view.json`,JSON.stringify(cached,null,2));
   assert(!cached.replies.some(reply=>['alpha-689','alpha-691','alpha-693'].includes(reply.input_id)),'No receiver lifecycle is fabricated for paged inputs');
   const remotePages=fixture.trace.filter(row=>row.kind==='history-response').length;
   await page.reload();await ready();
   await page.evaluate(()=>window.historyDiagnosticPhase='cached-paging');await reachHistory();
   await page.locator('[data-message-id="alpha-690"]').waitFor();const reloaded=await verify();
   assert.equal(fixture.trace.filter(row=>row.kind==='history-response').length,remotePages,'Repeated history paging reads cached records without another HTTP page');
   assert.deepEqual(errors,[]);assert.equal(fixture.trace.filter(row=>row.path==='/api/v1/messages'&&row.method==='POST').length,0);
   const frames=await page.evaluate(()=>historyDiagnosticFrames);
   results.push({phone,baseline,paged,reloaded,cached,errors,posts:0});
   await writeFile(output+`/${phone?'phone':'desktop'}.json`,JSON.stringify({frames,http:fixture.trace,cached,errors},null,2));
  }catch(error){await writeFile(output+`/${phone?'phone':'desktop'}-failure.json`,JSON.stringify({error:error.stack,body:await page.locator('body').innerText(),frames:await page.evaluate(()=>historyDiagnosticFrames).catch(()=>[]),http:fixture.trace,errors},null,2));throw error;}
  finally{await context.close();await fixture.close();}
 }
 await writeFile(output+'/summary.json',JSON.stringify(results,null,2));console.log(JSON.stringify({passed:true,baseline,cases:results.length,errors:results.flatMap(result=>result.errors),posts:0,output}));
}finally{await browser.close();await source.close();}
