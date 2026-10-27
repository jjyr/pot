import assert from 'node:assert/strict';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {preparedResourceMatrix,preparedLateResources} from './prepared-resource-matrix.mjs';
import {preparedLifetimes,preparedTaskDisclosure} from './prepared-lifetimes-browser.mjs';

const source=await sourceServer(),browser=await launchBrowser();
try{
 const context=await browser.newContext(),page=await context.newPage(),errors=[];page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message));
 await page.goto(source.origin+'/empty-prepared-fixture');
 await page.evaluate(async()=>{
  const {createReplyBubble}=await import('/components/reply.mjs');
  const {createPreparationSurface,createPreparedRender,withPreparedRender,settlePreparedTree,retirePreparedTree}=await import('/components/prepared-render.mjs');
  const {moveConversationNode}=await import('/components/conversation-dom.mjs');
  document.documentElement.lang='en';document.body.replaceChildren();const sheet=document.createElement('link');sheet.rel='stylesheet';sheet.href='/styles.css';document.head.append(sheet);await new Promise((resolve,reject)=>{sheet.onload=resolve;sheet.onerror=reject;});await document.fonts.ready;
  const app=document.createElement('main');app.className='pot-app';const scroller=document.createElement('div');scroller.className='history';scroller.style.cssText='width:700px;height:500px;flex:none';const column=document.createElement('div');column.className='message-column';const host=document.createElement('div');host.className='message-window';column.append(host);scroller.append(column);app.append(scroller);document.body.append(app);
  const f=window.preparedFixture={acquired:0,released:0,reads:0,retirePreparedTree,withPreparedRender,settlePreparedTree,createPreparedRender,createReplyBubble,moveConversationNode,scroller,host};
  const resource={id:'synthetic-prepared',name:'report.md',entry:'report.md',mime_type:'text/markdown',size:1,download:{path:'report.md'},files:[{path:'report.md',size:1,mime_type:'text/markdown',digest:'fixture'}]};
  const diagram='```mermaid\ngraph TD\n A[Prepare] --> B[Measure]\n```';
  f.options={t:key=>key,messageFiles:async()=>[{id:'binding',href:'report.md',state:'ready',resource}],retryMessageFile:async()=>{},downloadResource:async()=>{},acquireResource:()=>{f.acquired++;return {read:async()=>{f.reads++;return new Blob(['# Report\n\n'+diagram],{type:'text/markdown'});},release:()=>f.released++};}};
  f.message={id:'m',text:'[Report](report.md)',phase:'complete',status:'complete',activity:false};
  f.surface=createPreparationSurface(scroller,host);f.context=createPreparedRender();
  f.build=(context,message)=>withPreparedRender(context,'m',()=>{const row=document.createElement('article');row.className='message role-assistant';row.dataset.messageId='m';const body=document.createElement('div');body.className='message-body';row.append(body);f.surface.host.append(row);const view=createReplyBubble(body);view.update(message,f.options);return {row,view};});
  f.first=f.build(f.context,f.message);
 });
 await page.waitForFunction(()=>!!document.querySelector('pot-mermaid[data-state="ready"]'));
 const first=await page.evaluate(async()=>{const f=preparedFixture,measured=await f.settlePreparedTree(f.first.row,f.context).promise;f.context.phase='live';f.moveConversationNode(f.host,f.first.row);f.resource=f.first.row.querySelector('pot-resource');f.diagram=f.first.row.querySelector('pot-mermaid');f.instance=f.diagram.diagram;f.generation=f.resource.generation;await new Promise(requestAnimationFrame);return {measured,height:f.first.row.getBoundingClientRect().height,acquired:f.acquired,reads:f.reads,released:f.released,resourceReady:f.resource.dataset.state};});
 assert.equal(first.acquired,1);assert.equal(first.reads,1);assert.equal(first.released,0);assert.equal(first.resourceReady,'ready');assert(Math.abs(first.measured.height-first.height)<1,'Same-width staged measurement matches live row');
 const update=await page.evaluate(async()=>{
  const f=preparedFixture;f.next=f.createPreparedRender({previousRoot:f.first.row});f.nextMessage={...f.message,text:f.message.text+'\n\nFresh **tail**.'};f.candidate=f.build(f.next,f.nextMessage);const measured=await f.settlePreparedTree(f.candidate.row,f.next).promise;
  if(!f.first.row.contains(f.resource)||!f.first.row.contains(f.diagram))throw Error('Preparation moved an original island out of the old live version');
  const before={acquired:f.acquired,reads:f.reads,released:f.released};f.next.phase='commit';f.withPreparedRender(f.next,'m',()=>f.first.view.update(f.nextMessage,f.options));f.next.phase='live';f.retirePreparedTree(f.candidate.row);await new Promise(requestAnimationFrame);
  return {before,after:{acquired:f.acquired,reads:f.reads,released:f.released},sameResource:f.first.row.querySelector('pot-resource')===f.resource,sameDiagram:f.first.row.querySelector('pot-mermaid')===f.diagram,sameInstance:f.diagram.diagram===f.instance,sameGeneration:f.resource.generation===f.generation,measured:measured.height,height:f.first.row.getBoundingClientRect().height,text:f.first.row.textContent};
 });
 assert.deepEqual(update.before,{acquired:1,reads:1,released:0});assert.deepEqual(update.after,update.before);assert(update.sameResource&&update.sameDiagram&&update.sameInstance&&update.sameGeneration,'Retained islands keep lease, generation and renderer instance');assert(Math.abs(update.measured-update.height)<1,'Prepared update measurement matches patched live row');assert(update.text.includes('Fresh tail.'));
 const moved=await page.evaluate(async()=>{const f=preparedFixture,other=document.createElement('div');other.style.width=f.host.clientWidth+'px';f.host.parentElement.append(other);other.insertBefore(f.first.row,null);await Promise.resolve();const kept=f.resource.generation===f.generation&&f.diagram.diagram===f.instance&&f.acquired===1&&f.released===0;f.host.insertBefore(f.first.row,null);await Promise.resolve();other.remove();return kept;});
 assert(moved,'Fallback same-task DOM move retains component leases and diagram');
 const retired=await page.evaluate(async()=>{const f=preparedFixture;f.retirePreparedTree(f.first.row);await Promise.resolve();return {released:f.released,diagram:!!f.diagram.diagram};});
 assert.equal(retired.released,1);assert.equal(retired.diagram,false);assert.deepEqual(errors,[]);
 const late=await page.evaluate(async()=>{
  const f=preparedFixture,{createMessageFile}=await import('/components/message-file.mjs'),resolvers=[];
  const resource={id:'late-binding',name:'late.md',entry:'late.md',mime_type:'text/markdown',size:1,files:[{path:'late.md',size:1,mime_type:'text/markdown',digest:'late'}]};
  let acquired=0,released=0;
  const element=createMessageFile({href:'late.md',label:'Late report',block:true},'late-message',{messageFiles:()=>new Promise(resolve=>resolvers.push(resolve)),acquireResource:()=>{acquired++;return {read:async()=>new Blob(['A real late report']),release:()=>released++};},downloadResource:async()=>{}});
  f.host.append(element);element.remove();await Promise.resolve();f.host.append(element);
  resolvers[0]([{id:'old',href:'late.md',state:'ready',resource}]);await new Promise(requestAnimationFrame);
  const staleIgnored=element.dataset.state==='loading'&&!element.querySelector('pot-resource')&&acquired===0;
  resolvers[1]([{id:'new',href:'late.md',state:'ready',resource}]);
  for(let i=0;i<30&&!element.querySelector('pot-resource[data-state="ready"]');i++)await new Promise(requestAnimationFrame);
  f.retirePreparedTree(element);await Promise.resolve();return {staleIgnored,acquired,released};
 });
 assert(late.staleIgnored,'A late binding from a genuinely removed generation cannot populate a reconnected element');assert.equal(late.acquired,1);assert.equal(late.released,1);
 const metadataRace=await page.evaluate(async()=>{
  const f=preparedFixture,{createMessageFile}=await import('/components/message-file.mjs'),reads=[];
  let acquired=0,released=0;
  const resource={id:'completed-binding',name:'race.txt',entry:'race.txt',mime_type:'text/plain',size:1,files:[{path:'race.txt',size:1,mime_type:'text/plain',digest:'race'}]},options={pending:true,messageFiles:()=>new Promise(resolve=>reads.push(resolve)),downloadResource:async()=>{},acquireResource:()=>{acquired++;return {read:async()=>new Blob(['Current saved version']),release:()=>released++};}};
  const element=createMessageFile({href:'race.txt',label:'Race',block:true},'race-message',options);f.host.append(element);
  element.updatePreparedProperties({options:{...options,pending:false}});
  reads[1]([{id:'new',href:'race.txt',state:'ready',resource}]);
  for(let i=0;i<60&&!element.querySelector('pot-resource[data-state="ready"]');i++)await new Promise(requestAnimationFrame);
  reads[0]([{id:'old',href:'race.txt',state:'pending'}]);await new Promise(requestAnimationFrame);
  const result={row:element.row.id,state:element.dataset.state,resource:element.querySelector('pot-resource')?.presentation.resource.id,acquired};f.retirePreparedTree(element);await Promise.resolve();return {...result,released};
 });
 assert.deepEqual(metadataRace,{row:'new',state:'ready',resource:'completed-binding',acquired:1,released:1},'Completion fences an older metadata response without restarting the live preview');
 const deadline=await page.evaluate(async()=>{
  const f=preparedFixture,{createMessageFile}=await import('/components/message-file.mjs'),deadlines=new Map(),started=performance.now();
  const make=()=>{const row=document.createElement('article');row.className='message role-assistant';row.dataset.messageId='deadline';const body=document.createElement('div');body.className='message-body';const file=createMessageFile({href:'slow.md',label:'Slow report',block:true},'deadline-message',{messageFiles:()=>new Promise(()=>{})});body.append(file);row.append(body);f.surface.host.append(row);return {row,file};};
  const first=make(),one=f.createPreparedRender({deadlines}),waiting=f.settlePreparedTree(first.row,one);waiting.promise.catch(()=>{});
  await new Promise(resolve=>setTimeout(resolve,3000));waiting.cancel();f.retirePreparedTree(first.row);
  const second=make(),two=f.createPreparedRender({deadlines});const measured=await f.settlePreparedTree(second.row,two).promise;
  const elapsed=performance.now()-started,slot=second.file.getBoundingClientRect().height;f.moveConversationNode(f.host,second.row);const live=second.row.getBoundingClientRect().height;f.retirePreparedTree(second.row);
  return {elapsed,slot,measured:measured.height,live};
 });
 assert(deadline.elapsed>=4900&&deadline.elapsed<6500,'Replacing a candidate cannot restart the five-second component deadline');assert(deadline.slot>=240);assert(Math.abs(deadline.measured-deadline.live)<1);assert.deepEqual(errors,[]);
 const unfinished=await page.evaluate(async()=>{
  const f=preparedFixture,deadlines=new Map(),started=performance.now(),open='A stable paragraph.\n\n```mermaid\ngraph TD\n A[Prepare] --> B[Commit]\n'+Array.from({length:60},(_,i)=>'%% source '+i).join('\n');
  const one=f.createPreparedRender({deadlines}),first=f.build(one,{...f.message,text:open}),pending=f.settlePreparedTree(first.row,one);pending.promise.catch(()=>{});
  await new Promise(resolve=>setTimeout(resolve,3000));pending.cancel();f.retirePreparedTree(first.row);
  const two=f.createPreparedRender({deadlines}),message={...f.message,text:open+'\n%% another source token'},second=f.build(two,message);await f.settlePreparedTree(second.row,two).promise;
  const elapsed=performance.now()-started,diagram=second.row.querySelector('pot-mermaid'),frame=diagram.getBoundingClientRect().height,height=second.row.getBoundingClientRect().height,sourceHidden=!diagram.querySelector('details').open;
  two.phase='live';f.moveConversationNode(f.host,second.row);
  const three=f.createPreparedRender({deadlines,previousRoot:second.row}),closed={...message,text:message.text+'\n```'},candidate=f.build(three,closed);await f.settlePreparedTree(candidate.row,three).promise;
  three.phase='commit';f.withPreparedRender(three,'m',()=>second.view.update(closed,f.options));three.phase='live';f.retirePreparedTree(candidate.row);
  for(let i=0;i<120&&second.row.querySelector('pot-mermaid')?.dataset.state!=='ready';i++)await new Promise(requestAnimationFrame);
  const result={elapsed,frame,height,sourceHidden,closedFrame:second.row.querySelector('pot-mermaid').getBoundingClientRect().height,closedHeight:second.row.getBoundingClientRect().height,state:second.row.querySelector('pot-mermaid').dataset.state};f.retirePreparedTree(second.row);return result;
 });
 assert(unfinished.elapsed>=4900&&unfinished.elapsed<6500,'Mermaid source tokens retain the component deadline');assert.equal(unfinished.frame,240);assert(unfinished.sourceHidden,'Unfinished diagram source is not the published preview');assert.equal(unfinished.state,'ready');assert.equal(unfinished.closedFrame,unfinished.frame);assert.equal(unfinished.closedHeight,unfinished.height,'Closing after fallback cannot shrink or grow the published canvas');
 const budget=await page.evaluate(async()=>{
  const f=preparedFixture,{createFileImage}=await import('/components/file-image.mjs'),context=f.createPreparedRender(),row=document.createElement('article'),pending=[];let acquired=0,released=0;
  row.preparedContext=context.connectionContext;row.className='message';row.dataset.messageId='budget';
  f.withPreparedRender(context,'budget',()=>{for(let i=0;i<48;i++)row.append(createFileImage({task:'budget',path:i+'.png'},'Synthetic image',{downloadFile:async()=>{},acquireFileImage:()=>{acquired++;return {url:new Promise(resolve=>pending.push(resolve)),release:()=>released++};}}));});f.surface.host.append(row);
  for(let i=0;i<3;i++)await new Promise(requestAnimationFrame);const first=acquired;
  const image='data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==';
  let maxLoading=0;for(let frame=0;frame<120;frame++){maxLoading=Math.max(maxLoading,[...row.children].filter(node=>node.started&&node.dataset.state==='loading').length);for(const resolve of pending.splice(0))resolve(image);if(acquired===48&&[...row.children].every(node=>node.dataset.state==='ready'))break;await new Promise(requestAnimationFrame);}
  const ready=[...row.children].filter(node=>node.dataset.state==='ready').length;f.retirePreparedTree(row);await Promise.resolve();return {first,maxLoading,acquired,released,ready};
 });
 assert.equal(budget.first,32,'One window admits at most 32 component loads at once');assert(budget.maxLoading<=32);assert.equal(budget.ready,48,'Queued components progress when earlier loads settle');assert.equal(budget.released,48,'Retirement releases active and queued candidates exactly once');assert.deepEqual(errors,[]);
 const matrix=await preparedResourceMatrix(page),lateTypes=await preparedLateResources(page);assert.deepEqual(errors,[]);
 await context.close();const lifetimes=await preparedLifetimes(browser,source.origin),taskDisclosure=await preparedTaskDisclosure(browser,source.origin);console.log(JSON.stringify({passed:true,sameNode:true,recursiveReady:true,measurementParity:true,leaseReused:true,trueRemovalReleasedOnce:true,metadataRace,unfinished,budget,matrix,lateTypes,lifetimes,taskDisclosure,errors}));
}finally{await browser.close();await source.close();}
