import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {preparedChatFixture} from './prepared-chat-fixture.mjs';

const output=resolve(process.env.POT_PREPARED_OUTPUT||'.pot-artifacts/prepared-chat');await mkdir(output,{recursive:true});
const source=await sourceServer({root:process.env.POT_PREPARED_SOURCE||resolve('.')}),browser=await launchBrowser(),results=[];
try{
 for(const phone of [false,true]){
  const fixture=await preparedChatFixture(source.origin),context=await browser.newContext({viewport:phone?{width:390,height:852}:{width:1280,height:800},isMobile:phone,hasTouch:phone,serviceWorkers:'block'}),page=await context.newPage(),errors=[];
  page.setDefaultTimeout(15000);page.on('pageerror',error=>errors.push(error.message));
  await context.route('**/*',route=>new URL(route.request().url()).origin===fixture.origin?route.continue():route.abort());
  const control=async value=>{const response=await fetch(fixture.origin+'/fixture/control',{method:'POST',body:JSON.stringify(value)});assert(response.ok);return response.json();};
  const frames=()=>page.evaluate(async()=>{for(let i=0;i<12;i++)await new Promise(requestAnimationFrame);});
  await context.addInitScript(()=>{
   const identities=new WeakMap();let sequence=0;const identity=node=>{if(!node)return null;if(!identities.has(node))identities.set(node,++sequence);return identities.get(node);};
   window.preparedEvidence={timeOrigin:performance.timeOrigin,phase:'startup',frames:[],writes:[],resources:[]};
   const descriptor=Object.getOwnPropertyDescriptor(Element.prototype,'scrollTop');Object.defineProperty(Element.prototype,'scrollTop',{...descriptor,set(value){if(this.classList?.contains('history'))preparedEvidence.writes.push({at:performance.now(),phase:preparedEvidence.phase,from:descriptor.get.call(this),to:value,extent:this.scrollHeight});descriptor.set.call(this,value);}});
   const sample=()=>{const h=document.querySelector('.history:not([data-preparation-surface])'),row=h?.querySelector('[data-message-id="active-answer"]'),file=row?.querySelector('pot-message-file'),resource=row?.querySelector('pot-resource'),diagram=row?.querySelector('pot-mermaid');
    const state={at:performance.now(),phase:preparedEvidence.phase,visible:!!h&&getComputedStyle(h).visibility==='visible',top:h?.scrollTop,extent:h?.scrollHeight,row:identity(row),height:row?.getBoundingClientRect().height,text:row?.querySelector('.reply-content')?.textContent,file:file&&{id:identity(file),height:file.getBoundingClientRect().height,state:file.dataset.state,fallback:file.dataset.preparedFallback},resource:resource&&{id:identity(resource),height:resource.getBoundingClientRect().height,state:resource.dataset.state},diagram:diagram&&{id:identity(diagram),height:diagram.getBoundingClientRect().height,state:diagram.dataset.state,unfinished:diagram.dataset.unfinished},surfaces:document.querySelectorAll('[data-preparation-surface]').length};
    preparedEvidence.frames.push(state);for(const node of document.querySelectorAll('[data-preparation-surface] pot-resource')){const id=identity(node);if(!preparedEvidence.resources.some(item=>item.id===id))preparedEvidence.resources.push({id,at:performance.now()});}requestAnimationFrame(sample);};requestAnimationFrame(sample);
  });
  try{
   await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha');await page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&getComputedStyle(document.querySelector('.history:not([data-preparation-surface])')).visibility==='visible');
   const dismiss=page.getByRole('button',{name:'Not now',exact:true});if(await dismiss.count())await dismiss.click();
   const base='Stable paragraph retained while resources prepare.\n\n```js\nconst value = "'+'x'.repeat(150)+'";\n```';
   await control({scenario:'custom',text:base});await page.locator('.history:not([data-preparation-surface]) [data-message-id="active-answer"] pre').waitFor();await frames();
   await page.evaluate(()=>{const row=document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"]'),paragraph=row.querySelector('.reply-content p'),code=row.querySelector('pre');code.scrollLeft=20;getSelection().setBaseAndExtent(paragraph.firstChild,0,paragraph.firstChild,6);window.retainedPrepared={row,paragraph,code,scroll:code.scrollLeft,selection:getSelection().anchorNode};preparedEvidence.phase='held-preview';});
   await control({scenario:'custom',text:base+'\n\n[Synthetic report](report.md)',holdPreview:true});
   await page.waitForFunction(()=>document.querySelector('[data-preparation-surface] pot-resource[data-state="loading"]'));await frames();
   const held=await page.evaluate(()=>{const saved=retainedPrepared,row=document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"]');return {same:row===saved.row&&row.querySelector('p')===saved.paragraph,resources:row.querySelectorAll('pot-resource').length,visible:getComputedStyle(document.querySelector('.history:not([data-preparation-surface])')).visibility,selection:getSelection().anchorNode===saved.selection};});
   assert.deepEqual(held,{same:true,resources:0,visible:'visible',selection:true},'Pending bytes retain the old live version and selection');
   await control({holdPreview:false});await page.locator('.history:not([data-preparation-surface]) [data-message-id="active-answer"] pot-mermaid[data-state="ready"]').waitFor();await frames();
   const committed=await page.evaluate(()=>{const saved=retainedPrepared,row=document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"]'),resource=row.querySelector('pot-resource'),diagram=row.querySelector('pot-mermaid'),last=preparedEvidence.frames.at(-1);return {sameRow:row===saved.row,sameParagraph:row.querySelector('p')===saved.paragraph,sameCode:row.querySelector('pre')===saved.code,scroll:saved.code.scrollLeft,savedScroll:saved.scroll,selection:getSelection().anchorNode===saved.selection,height:row.getBoundingClientRect().height,measured:Number.parseFloat(row.style.getPropertyValue('--prepared-row-height')),staged:preparedEvidence.resources.some(item=>item.id===last.resource.id),resource:resource.dataset.state,diagram:diagram.dataset.state};});
   assert(committed.sameRow&&committed.sameParagraph&&committed.sameCode&&committed.selection,'Prepared replay preserves actual live Markdown nodes');assert.equal(committed.scroll,committed.savedScroll);assert.equal(committed.resource,'ready');assert.equal(committed.diagram,'ready');assert(committed.staged,'The actual connected candidate resource is transferred');assert(Math.abs(committed.height-committed.measured)<1,'Staged border size equals the committed live row');
   await page.evaluate(()=>getSelection().removeAllRanges());
   await control({scenario:'custom',text:base});await page.waitForFunction(()=>!document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"] pot-message-file'));await frames();
   await page.evaluate(()=>preparedEvidence.phase='deadline-stream');const started=Date.now();
   for(let token=0;token<115;token++){await control({scenario:'custom',text:base+'\n\n[Synthetic slow report](slow.md)\n\nTokens '+token,holdPreview:true});await page.waitForTimeout(50);}
   await page.locator('.history:not([data-preparation-surface]) [data-message-id="active-answer"] pot-message-file.prepared-frame').waitFor();await frames();
   const timeout=await page.evaluate(()=>{const row=document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"]'),file=row.querySelector('pot-message-file'),first=preparedEvidence.frames.find(frame=>frame.phase==='deadline-stream'&&frame.file);return {at:first&&preparedEvidence.timeOrigin+first.at,height:row.getBoundingClientRect().height,frame:file.getBoundingClientRect().height,fallback:!!row.querySelector('[data-prepared-fallback="true"]'),text:row.querySelector('.reply-content').textContent};});
   assert(timeout.at-started>=4800&&timeout.at-started<6500,'Continuous tokens cannot restart or starve the five-second preparation deadline');assert(timeout.frame>=240);assert(timeout.fallback);assert(timeout.text.includes('Tokens 114'),'Newest pending prose progresses after the bounded fallback');
   await page.evaluate(()=>preparedEvidence.phase='late-bytes');await control({holdPreview:false});await page.locator('.history:not([data-preparation-surface]) [data-message-id="active-answer"] pot-mermaid[data-state="ready"]').waitFor();await frames();
   const late=await page.locator('.history:not([data-preparation-surface]) [data-message-id="active-answer"]').evaluate(row=>({height:row.getBoundingClientRect().height,frame:row.querySelector('pot-message-file').getBoundingClientRect().height}));assert.equal(late.frame,timeout.frame);assert.equal(late.height,timeout.height,'Very late recursive rendering stays inside the committed fallback');
   await page.screenshot({path:output+'/'+(phone?'phone':'desktop')+'-prepared.png'});
   const evidence=await page.evaluate(()=>preparedEvidence);assert(evidence.frames.every(frame=>frame.surfaces<=103),'The preparation cohort has a hard row/region bound');assert.deepEqual(errors,[]);
   const posts=fixture.trace.filter(row=>row.kind==='http-start'&&row.method==='POST');assert.equal(posts.length,0);
   const result={phone,held,committed,timeout,late,errors,posts:posts.length};results.push(result);await writeFile(output+'/'+(phone?'phone':'desktop')+'-raw.json',JSON.stringify({result,evidence,http:fixture.trace},null,2));
  }catch(error){await writeFile(output+'/'+(phone?'phone':'desktop')+'-failure.json',JSON.stringify({error:error.stack,evidence:await page.evaluate(()=>window.preparedEvidence).catch(()=>null),http:fixture.trace,errors},null,2));throw error;}
  finally{await context.close();await fixture.close();}
 }
 await writeFile(output+'/summary.json',JSON.stringify(results,null,2));console.log(JSON.stringify({passed:true,cases:results.length,results,output}));
}finally{await browser.close();await source.close();}
