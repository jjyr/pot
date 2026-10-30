import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {preparedChatFixture} from './prepared-chat-fixture.mjs';
// Regression cases first reproduced by independent review against 271ce428.
export async function preparedLifetimes(browser,origin){
const output=resolve(process.env.POT_LIFETIMES_OUTPUT||'.pot-artifacts/prepared-lifetimes'),results=[];await mkdir(output,{recursive:true});
for(const phone of [false,true])for(const test of ['reference-fresh','reference-retained','deadline-remove-readd']){
  const fixture=await preparedChatFixture(origin,{currentReference:true}),context=await browser.newContext({viewport:phone?{width:390,height:852}:{width:1280,height:800},isMobile:phone,hasTouch:phone,serviceWorkers:'block'}),page=await context.newPage(),errors=[],steps=[];
  page.setDefaultTimeout(14000);page.on('pageerror',e=>errors.push(e.message));
  await context.route('**/*',route=>new URL(route.request().url()).origin===fixture.origin?route.continue():route.abort());
  await context.addInitScript(()=>localStorage.setItem('pot-system-notifications-browser-v1','false'));
  const control=async value=>{assert((await context.request.post(fixture.origin+'/fixture/control',{data:value})).ok());};
  const frames=()=>page.evaluate(async()=>{for(let i=0;i<12;i++)await new Promise(requestAnimationFrame);});
  const sample=()=>page.evaluate(()=>{
   const h=document.querySelector('.history:not([data-preparation-surface])'),row=h?.querySelector('[data-message-id="active-answer"]'),m=row?.querySelector('pot-mermaid'),r=row?.getBoundingClientRect(),v=h?.getBoundingClientRect(),ref=h?.querySelector('.message-reference-open');
   return {at:performance.now(),url:location.href,visible:h&&getComputedStyle(h).visibility,busy:!!h?.querySelector('.message-window[aria-busy]'),id:row?.dataset.messageId,sequence:row?.dataset.sequence,windowKey:row?.dataset.windowKey,text:row?.querySelector('.reply-content')?.textContent,highlight:row?.classList.contains('reference-target'),sameRow:row===globalThis.savedLifetimeRow,rowHeight:r?.height,overlap:r&&Math.min(r.bottom,v.bottom)-Math.max(r.top,v.top),mermaid:m&&{connected:m.isConnected,slot:m.renderSlot,source:m.sourceText,state:m.dataset.state,unfinished:m.dataset.unfinished,fallback:m.dataset.preparedFallback,height:m.getBoundingClientRect().height},oldDisconnected:globalThis.savedLifetimeMermaid?!savedLifetimeMermaid.isConnected:undefined,newComponent:m&&globalThis.savedLifetimeMermaid?m!==savedLifetimeMermaid:undefined,reference:ref&&{title:ref.title,sourceRow:ref.closest('article')?.dataset.messageId,sourceSequence:ref.closest('article')?.dataset.sequence,interactive:!ref.disabled&&!ref.closest('[inert]'),overlap:Math.min(ref.getBoundingClientRect().bottom,v.bottom)-Math.max(ref.getBoundingClientRect().top,v.top)},preparing:[...document.querySelectorAll('[data-preparation-surface] pot-mermaid')].map(n=>({slot:n.renderSlot,state:n.dataset.state,source:n.sourceText,height:n.getBoundingClientRect().height}))};
  });
  try{
   await control({scenario:'custom',text:'Independent original answer.'});
   await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha');
   await page.waitForFunction(()=>{const h=document.querySelector('.history:not([data-preparation-surface])');return h&&getComputedStyle(h).visibility==='visible'&&h.querySelector('[data-message-id="active-answer"]')&&!h.querySelector('.message-window[aria-busy]');});await frames();
   await page.evaluate(()=>globalThis.savedLifetimeRow=document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"]'));
   steps.push({phase:'initial',sample:await sample()});
   if(test.startsWith('reference-')){
    if(test==='reference-retained'){
     await control({scenario:'custom',text:'Independent original answer. Token update committed.'});
     await page.waitForFunction(()=>document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"]')?.textContent.includes('Token update committed.')&&!document.querySelector('.history:not([data-preparation-surface]) .message-window[aria-busy]'));await frames();
    }
    const before=await sample();steps.push({phase:'before-click',sample:before});
    assert(before.sameRow);assert.equal(before.reference.title,'Independent exact current-scope answer');assert.equal(before.reference.sourceRow,'active-input');assert(before.reference.interactive);assert(before.reference.overlap>=24);
    await page.locator('.history:not([data-preparation-surface]) [data-message-id="active-input"] .message-reference-open').click();await frames();
    const after=await sample();steps.push({phase:'after-click',sample:after});
    assert.equal(after.id,'active-answer');assert(after.sameRow);assert(after.overlap>=24);assert.equal(after.highlight,true,'Mounted exact reference target must receive its visible locate highlight');
   }else{
    const base='Independent paragraph.',first=base+'\n\n```mermaid\ngraph TD\n A[First lifetime] --> B[Waiting]';
    const start=await page.evaluate(()=>performance.now());await control({scenario:'custom',text:first});
    await page.waitForFunction(()=>document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"] pot-mermaid')?.dataset.preparedFallback==='true');
    const firstSample=await sample();steps.push({phase:'first-fallback',elapsed:firstSample.at-start,sample:firstSample});assert(firstSample.at-start>=4800);assert(firstSample.at-start<6500);
    await page.evaluate(()=>globalThis.savedLifetimeMermaid=document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"] pot-mermaid'));
    await control({scenario:'custom',text:base});
    await page.waitForFunction(()=>!document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"] pot-mermaid')&&!document.querySelector('.history:not([data-preparation-surface]) .message-window[aria-busy]'));await frames();
    const removed=await sample();steps.push({phase:'true-removal-committed',sample:removed});assert(removed.sameRow);assert(removed.oldDisconnected);
    const secondStart=await page.evaluate(()=>performance.now());await control({scenario:'custom',text:base+'\n\n```mermaid\ngraph TD\n C[Second lifetime] --> D[Waiting]'});
    await page.waitForFunction(()=>document.querySelector('.history:not([data-preparation-surface]) [data-message-id="active-answer"] pot-mermaid')?.dataset.preparedFallback==='true');
    const second=await sample();steps.push({phase:'second-fallback',elapsed:second.at-secondStart,sample:second});assert(second.newComponent);assert(second.oldDisconnected);assert(second.sameRow);
    assert(second.at-secondStart>=4800&&second.at-secondStart<6500,'A genuinely removed component must receive a new five-second readiness deadline');
   }
   assert.deepEqual(errors,[]);results.push({phone,test,passed:true,steps,errors});
  }catch(error){results.push({phone,test,passed:false,error:error.stack,steps,last:await sample(),errors});}
  const result=results.at(-1);await page.screenshot({path:output+'/'+(phone?'phone':'desktop')+'-'+test+'.png'});await writeFile(output+'/'+(phone?'phone':'desktop')+'-'+test+'.json',JSON.stringify({...result,http:fixture.trace},null,2));console.log(JSON.stringify({phone,test,passed:result.passed,error:result.error?.split('\n')[0],steps:steps.map(s=>({phase:s.phase,elapsed:s.elapsed,highlight:s.sample.highlight,sameRow:s.sample.sameRow,newComponent:s.sample.newComponent}))}));
  await context.close();await fixture.close();
 }
await writeFile(output+'/summary.json',JSON.stringify({results},null,2));
assert(results.every(result=>result.passed),JSON.stringify(results.filter(result=>!result.passed),null,2));return {cases:results.length,passed:true};
}

// A pending approval may auto-open once; hidden candidates cannot overwrite
// the reader's subsequent explicit close. Uses trusted summary activation.
export async function preparedTaskDisclosure(browser,origin){
const output=resolve(process.env.POT_LIFETIMES_OUTPUT||'.pot-artifacts/prepared-lifetimes')+'/task-disclosure';await mkdir(output,{recursive:true});
const context=await browser.newContext();let page;
try{page=await context.newPage();page.setDefaultTimeout(12000);const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto(origin+'/prepared-task-approval');
 await page.evaluate(async()=>{
  document.documentElement.lang='en';const sheet=document.createElement('link');sheet.rel='stylesheet';sheet.href='/styles.css';document.head.append(sheet);await new Promise((resolve,reject)=>{sheet.onload=resolve;sheet.onerror=reject;});await document.fonts.ready;document.body.replaceChildren();
  const {createTaskConversation}=await import('/components/task-conversation.mjs'),{createTaskTranscript,appendTaskEvents}=await import('/state/task-transcript.mjs');const host=document.createElement('main');host.className='page-content';host.style.cssText='height:700px;width:900px;margin:auto';document.body.append(host);
  const scope={space:'local',owner:'synthetic-owner'},task={id:'synthetic-approval-task',assistant_id:scope.owner,phase:'waiting_approval'},record={run_id:'synthetic-run',request:{text:'Synthetic Task approval disclosure'},info:task,capabilities:{version:1,operations:['task.read','task.events']}};
  const pending={id:'synthetic-approval',revision:1,request:{owner:scope.owner,task:task.id,call_id:'synthetic-call',turn:'synthetic-run',tool:'exec_command',args:{cmd:'Synthetic fixture only'},machine_id:'synthetic-machine'}};
  const state=createTaskTranscript();appendTaskEvents(state,[{sequence:1,event:{turn_id:'synthetic-run',update:{type:'tool_started',id:'synthetic-call',name:'exec_command',args:{cmd:'Synthetic fixture only'}}}},{sequence:2,event:{turn_id:'synthetic-run',update:{type:'tool_state',id:'synthetic-call',phase:'waiting_approval'}}}]);
  const memory=new Map(),decisions=[];const ui=createTaskConversation({root:host,task,record,scope,locale:()=> 'en',client:{runtime:{},request:async()=>({items:[]}),scope:()=>scope},draft:'',onDraft:()=>{},toolMemory:memory,approvalFor:tool=>tool.id==='synthetic-call'?pending:null,onDecision:(...args)=>decisions.push(args),assistantName:'Synthetic owner'});
  const f=window.independentApprovalClose={ui,memory,decisions,events:[],frames:[],phase:'initial'};const ids=new WeakMap();let sequence=0;const id=n=>{if(!n)return null;if(!ids.has(n))ids.set(n,++sequence);return ids.get(n);};f.sample=()=>{const live=ui.history.querySelector('details.tool-call'),row=live?.closest('article'),candidate=[...document.querySelectorAll('[data-preparation-surface] details.tool-call')];return {at:performance.now(),phase:f.phase,live:id(live),open:live?.open,connected:live?.isConnected,prepared:row?.classList.contains('prepared-row'),busy:!!ui.history.querySelector('[aria-busy="true"]'),memory:[...memory].map(([key,value])=>({key,...value})),candidates:candidate.map(n=>({id:id(n),open:n.open,connected:n.isConnected,inert:!!n.closest('[inert]')}))};};
  document.addEventListener('click',event=>{if(!event.target.closest('.task-history .tool-summary'))return;queueMicrotask(()=>f.events.push({kind:'user-click-after-handler',trusted:event.isTrusted,...f.sample()}));});
  const observer=new MutationObserver(()=>f.events.push({kind:'dom-change',...f.sample()}));observer.observe(document.body,{subtree:true,childList:true,attributes:true,attributeFilter:['open','aria-busy']});f.observer=observer;const tick=()=>{f.frames.push(f.sample());requestAnimationFrame(tick);};requestAnimationFrame(tick);ui.update({record,state,fresh:true,approvals:[pending]});ui.start();
 });
 await page.waitForFunction(()=>{const f=independentApprovalClose;return getComputedStyle(f.ui.history).visibility==='visible'&&f.ui.history.querySelector('details.tool-call')?.open&&!f.ui.history.querySelector('[aria-busy="true"]');});
 await page.evaluate(()=>independentApprovalClose.phase='explicit-close');const before=await page.evaluate(()=>independentApprovalClose.sample());await page.locator('.task-history:not([data-preparation-surface]) details.tool-call > summary').click();await page.evaluate(async()=>{for(let i=0;i<30;i++)await new Promise(requestAnimationFrame);});
 const result=await page.evaluate(()=>{const f=independentApprovalClose;return {after:f.sample(),events:f.events,frames:f.frames,decisions:f.decisions};});await writeFile(output+'/raw.json',JSON.stringify({before,...result,errors},null,2));await page.screenshot({path:output+'/after.png'});
 assert.deepEqual(errors,[]);assert(result.events.some(e=>e.kind==='user-click-after-handler'&&e.trusted&&e.memory.some(m=>m.open===false)),'Trusted explicit close must first write closed reading intent');assert.equal(result.after.open,false,'A pending approval candidate must preserve the latest explicit close');assert(result.after.memory.some(m=>m.open===false),'Candidate must not overwrite explicit closed memory');assert.equal(result.decisions.length,0);return {passed:true,before,after:result.after,errors};
}catch(error){await writeFile(output+'/failure.txt',error.stack);throw error;}finally{await context.close();}
}
