// Playwright CLI contract against synthetic data and the real shared Chat/Task UI.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 const errors=[],onError=error=>errors.push(error.message);page.on('pageerror',onError);
 const origin=new URL(page.url()).origin,at=Date.now()-6000;
 const call=(id,name,args,result,extra={})=>({id:`message-${id}`,role:'tool',text:'',status:'complete',tool:{id,turn_id:'request-u',name,args,result,phase:'completed',started_at_ms:at,finished_at_ms:at+1000,target:{runtime_id:'test-machine'},workdir:'/workspace/pot',output:[],...extra}});
 const command=(id,cmd,session)=>call(id,'exec_command',{cmd},{stdout:'Starting checks\n',session_id:session,exit_code:null});
 const poll=(id,session,result)=>call(id,'write_stdin',{session_id:session},result,{started_at_ms:at+1000,finished_at_ms:at+2000});
 const first=command('check','npm run check','private-process-one'),middle=poll('check-poll','private-process-one',{stdout:'Checking sources\n',session_id:'private-process-one'}),last=poll('check-done','private-process-one',{stdout:'Checks passed\n',exit_code:0});
 const second=command('build','npm run build','private-process-two'),secondDone=poll('build-done','private-process-two',{stdout:'Build passed\n',exit_code:0});
 const sequences=new Map();const envelope=(message,version)=>{if(!sequences.has(message.id))sequences.set(message.id,String(sequences.size+1));return {message,sequence:sequences.get(message.id),version};};
 let runtimeID='process-history';
 let messages=[{id:'u',request_id:'request-u',role:'user',text:'检查项目并验证构建结果。',status:'complete'},{id:'a',role:'assistant',text:'我会检查项目，然后验证构建结果。',reply_to:'u',status:'complete'},first,middle,last,second,secondDone],cursor=0,running=false;
 const snapshot=()=>({api_version:3,cursor:++cursor,boot_id:'boot',conversation_id:'alpha',messages,turn:running?{user_message_id:'u',state:'running'}:null,agent:{phase:'ready',model:'test'}});
 const frame=value=>({kind:'reset',base_cursor:0,view:{replies:[],state:{...value,messages:[]},messages:value.messages.map((message,index)=>envelope(message,value.cursor)),history:{before:'1',after:String(value.messages.length),has_more:false,has_newer:false}}});
 await page.addInitScript(()=>{const sources=[];window.EventSource=class extends EventTarget{constructor(url){super();this.url=String(url);sources.push(this);if(this.url.includes('/events'))window.__processSubscribed=true;queueMicrotask(()=>{this.onopen?.({});if(this.url.includes('/changes'))this.dispatchEvent(new MessageEvent('changed',{data:'{}',lastEventId:'boot:1'}));});}close(){}};window.__processSnapshot=frame=>{for(const source of sources)if(source.url.includes('/events'))source.dispatchEvent(new MessageEvent('conversation',{data:JSON.stringify(frame)}));};Object.defineProperty(navigator,'clipboard',{value:{writeText:async text=>{window.__processCopied=text;}}});});
 const access=route=>route.fulfill({json:{authenticated:true,push_available:false}});
 const handler=route=>{const path=new URL(route.request().url()).pathname;return route.fulfill({json:path==='/api/v1/runtime'?{runtime_id:runtimeID,boot_id:'boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',control_id:runtimeID,name:'Local',kind:'local',connected:true}]}:path==='/api/v1/assistants'?[{id:'alpha',name:'Pot',revision:1}]:path==='/api/v1/snapshot'?frame(snapshot()):path==='/api/v1/history'?{boot_id:'boot',conversation_id:'alpha',cursor:cursor,messages:messages.map((message,index)=>({message,sequence:String(index+1),version:cursor})),history:{before:'1',after:String(messages.length),has_more:false,has_newer:false}}:path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:path==='/api/v1/usage/summary'?{total:{},quota:{active:false,policy:{period:'none'}}}:{items:[]}});};
 await page.route('**/access/status',access);await page.route('**/api/v1/**',handler);
 const node=id=>page.locator(`.history[role="log"]:not([data-preparation-surface]) [data-call-id="${id}"]`),push=()=>page.evaluate(value=>window.__processSnapshot(value),frame(snapshot()));
 const capture=async suffix=>{for(const [size,width]of [['desktop',1200],['mobile',390]]){await page.setViewportSize({width,height:850});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.locator('.history[role="log"]:not([data-preparation-surface])').screenshot({path:`output/pr-images/codex-command-sessions/commands-${size}-${suffix}.png`});}};
 try{
  await page.setViewportSize({width:1200,height:850});await page.goto(`${origin}/?theme=light&locale=zh-CN&conversation=alpha`);
  await page.waitForFunction(()=>window.__processSubscribed);await push();
  await page.getByText('我会检查项目，然后验证构建结果。',{exact:true}).waitFor();
  const group=page.locator('.history[role="log"]:not([data-preparation-surface]) .work-record-group').first();
  await group.waitFor();assert(await page.locator('.history[role="log"]:not([data-preparation-surface]) .work-record-group').count()===1,'Commands share one group');assert(!await group.locator(':scope > .work-record-prefix > .work-record-summary').evaluate(element=>element.getAttribute('aria-expanded')==='true'),'Execution history starts collapsed');
  assert(await group.locator(':scope > .work-record-prefix > .work-record-summary .work-record-title').textContent()==='工作记录','Group has a stable generic title');
  await capture('after');await page.setViewportSize({width:1200,height:850});await group.locator(':scope > .work-record-prefix > .work-record-summary').click();
  await node('check').waitFor();await node('build').waitFor();assert(await node('check-poll').count()===0&&await node('check-done').count()===0,'Polls do not add rows');
  for(const id of ['check','build'])assert(!await node(id).locator(':scope > summary').textContent().then(text=>/private-process/.test(text)),'Command summaries hide session IDs');
  await group.screenshot({path:'output/pr-images/codex-command-sessions/commands-desktop-expanded-after.png'});
  await node('check').locator(':scope > summary').click();
  const output=node('check').locator('.tool-section').filter({has:page.getByText('输出',{exact:true})}).locator('pre');
  assert(await output.textContent()==='Starting checks\n\nChecking sources\n\nChecks passed\n','Terminal merges receipts once without repeating stdout labels');
  assert(!await node('check').locator(':scope > .tool-body > div:not(.tool-children)').allTextContents().then(parts=>parts.join('').includes('private-process-one')),'Normal details hide session IDs');
  await node('check').locator(':scope > .tool-body > .tool-raw > summary').click();
  await node('check').locator('.tool-raw .tool-call-id').first().waitFor();
  assert(await node('check').locator('.tool-raw').textContent().then(text=>text.includes('private-process-one')&&text.includes('check-poll')&&text.includes('check-done')),'Raw details retain original IDs and receipts');
  await node('check').locator(':scope > .tool-body > .tool-raw > summary').click();
  // Live updates preserve disclosure, output nodes, paused following and clocks.
  running=true;messages=messages.filter(message=>!['message-check-done','message-build-done'].includes(message.id));
  // The live scenario is a separate disposable Runtime, not a rewind of
  // completed durable receipts in the history scenario.
  runtimeID='process-live';sequences.clear();await page.reload();await page.waitForFunction(()=>window.__processSubscribed);await push();
  await group.locator(':scope > .work-record-prefix > .work-record-summary').click();await node('check').locator(':scope > summary').click();

  await page.evaluate(()=>{const root=document.querySelector('.history[role="log"]:not([data-preparation-surface])');const windows=[...root.querySelectorAll('.message-window')];const rows=[];for(const win of windows){for(const row of[...win.children]){if(row.matches('article.message')&&row.dataset.sequence)rows.push({id:row.dataset.messageId,row,parent:win,order:[...win.children].filter(node=>rows.some(entry=>entry.row===node))});}}const originals=rows.map(entry=>entry.row);const removed=new Set();const observer=new MutationObserver(records=>{for(const record of records){if(record.type!=='childList')continue;for(const node of record.removedNodes){for(const entry of rows){if(node===entry.row||(node.contains&&node.contains(entry.row)))removed.add(entry);}}}});for(const win of windows)observer.observe(win,{childList:true,subtree:true});const assert=()=>{if(removed.size)throw Error('Live session update removed a message row');const live=document.querySelector('.history[role="log"]:not([data-preparation-surface])');if(!live)throw Error('Live history is missing');for(const entry of rows){const current=[...live.querySelectorAll('article.message')].find(node=>node.dataset.messageId===entry.id);if(!current)throw Error('Live session update lost a message row');if(current!==entry.row)throw Error('Live session update replaced a message row');if(entry.row.parentElement!==entry.parent)throw Error('Live session update moved a message row');const siblings=[...entry.parent.children].filter(node=>originals.includes(node));const previous=entry.order.filter(node=>originals.includes(node)&&originals.indexOf(node)<originals.indexOf(entry.row));const position=originals.indexOf(entry.row);const expected=siblings.findIndex(node=>node===entry.row);const ordered=siblings.slice(0,expected);if(ordered.length!==previous.length||ordered.some((node,index)=>node!==previous[index]))throw Error('Live session update reordered message rows');}};window.__processDOMGuard={assert,close:()=>observer.disconnect()};});
  middle.tool.output=[{stream:'stdout',text:Array.from({length:90},(_,i)=>`line ${i}`).join('\n')}];middle.tool.result.stdout=middle.tool.output[0].text;
  await push();await node('check').locator(':scope > summary').getByText('执行中',{exact:true}).waitFor();
  await output.getByText('line 89',{exact:false}).waitFor();
  const retained=await output.elementHandle();await output.evaluate(element=>{element.scrollTop=0;element.dispatchEvent(new Event('scroll'));});
  middle.tool.output[0].text+='\nLATEST';middle.tool.result.stdout=middle.tool.output[0].text;await push();await output.getByText('LATEST',{exact:false}).waitFor();
  assert(await output.evaluate((element,old)=>element===old,retained),'Output node survives session updates');assert(await output.evaluate(element=>element.scrollTop)===0,'Paused output following survives updates');
  await node('check').getByRole('button',{name:'回到最新输出',exact:true}).click();assert(await output.evaluate(element=>element.scrollTop>0),'Jump to latest resumes following');
  const input=call('send-input','write_stdin',{session_id:'private-process-one',chars:'yes\n'},{session_id:'private-process-one'});messages.push(input);await push();
  await node('send-input').waitFor();assert(await node('send-input').evaluate(element=>element.closest('article.message')?.parentElement?.classList.contains('message-window')&&!element.parentElement.closest('.tool-call')),'Input action keeps its chronological row in the work group');
  await node('send-input').locator(':scope > summary').click();assert(await node('send-input').locator('.tool-session-input').count()===0,'Input parameters stay in secondary raw details');
  await node('send-input').locator('.tool-raw>summary').click();await node('send-input').locator('.tool-raw').getByText('yes',{exact:false}).first().waitFor();
  assert(!await node('send-input').locator(':scope > summary').textContent().then(text=>text.includes('private-process-one')),'Action summary hides session ID');
  const failed=poll('failed-poll','private-process-one',{stderr:'Checks failed',exit_code:2});messages.push(failed,secondDone);running=false;await push();
  await group.locator(':scope > .work-record-prefix > .work-record-summary .work-record-failure').waitFor();
  assert(await group.locator(':scope > .work-record-prefix > .work-record-summary .work-record-failure').isVisible(),'Collapsed work summary reports process failure');
  await node('check').locator('.tool-section').filter({has:page.getByText('输出',{exact:true})}).getByText('Checks failed',{exact:false}).waitFor();
  await node('check').locator(':scope > summary').click();await page.waitForFunction(()=>!document.querySelector('.history[role="log"]:not([data-preparation-surface]) [data-call-id="check"]')?.open);assert(!await node('check').evaluate(element=>element.open),'Completed process failure can be collapsed');await node('check').locator(':scope > summary').getByText('失败',{exact:true}).waitFor();
  assert(!await node('check').locator(':scope > summary').textContent().then(text=>text.includes('Checks failed')),'Collapsed command summary hides the diagnostic');
  await node('check').locator(':scope > summary').click();await node('check').getByText('Checks failed',{exact:false}).first().waitFor();await node('check').locator(':scope > summary').getByText('失败',{exact:true}).waitFor();
  await page.setViewportSize({width:390,height:850});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Expanded history fits mobile');
  await page.evaluate(()=>window.__processDOMGuard.assert());
  // Ordered Task events exercise command completion and exact approval ancestry.
  await page.evaluate(async()=>{
   const {createTaskConversation}=await import('/components/task-conversation.mjs'),{createTaskTranscript,appendTaskEvents}=await import('/state/task-transcript.mjs');
   const root=document.createElement('div');document.body.append(root);const state=createTaskTranscript(),record={info:{phase:'running'},request:{text:'Verify process sessions'}},noop=()=>{};
   const approval={id:'process-approval',request:{task:'task',turn:'task-turn',call_id:'task-input',machine_id:'test',tool:'write_stdin',args:{session_id:'task-session',chars:'yes\n'}}};
const view=createTaskConversation({root,task:{id:'task'},record,locale:()=> 'zh-CN',client:{request:async()=>({items:[]})},scope:{space:'local',owner:'alpha'},draft:'',onDraft:noop,toolMemory:new Map(),approvalFor:tool=>tool.id==='task-input'&&tool.phase==='waiting_approval'?approval:null,onDecision:noop,onSend:noop,onStop:noop,onPause:noop,onCancel:noop,onUsage:noop,onRefresh:noop,onSettings:noop});
let sequence=0;const emit=update=>appendTaskEvents(state,[{sequence:++sequence,event:{turn_id:'task-turn',update:{at_ms:Date.now(),...update}}}]);
const live=selector=>[...root.querySelectorAll(selector)].filter(node=>!node.closest('[data-preparation-surface]'));
const frame=()=>new Promise(resolve=>requestAnimationFrame(()=>resolve()));
const until=async test=>{for(let i=0;i<120;i++){const value=test();if(value)return value;await frame();}throw Error('Timed out');};
const positive=node=>{const rect=node.getBoundingClientRect();return rect.height>0&&rect.width>0;};
const articleOf=id=>live(`[data-call-id="${id}"]`)[0]?.closest('article.message')||null;
try{
 emit({type:'tool_started',id:'task-command',name:'exec_command',args:{cmd:'npm run check'}});emit({type:'tool_finished',id:'task-command',result:{stdout:'start\n',session_id:'task-session'}});view.update({state,record});
 const group=await until(()=>live('.work-record-group')[0]);await until(()=>{const activity=live('.work-record-activity')[0];return activity&&!activity.hidden;});
 const commandArticle=articleOf('task-command');if(!commandArticle)throw Error('Missing command article');
 group.querySelector(':scope > .work-record-prefix > .work-record-summary').click();await frame();
 emit({type:'tool_started',id:'task-poll',name:'write_stdin',args:{session_id:'task-session'}});emit({type:'tool_finished',id:'task-poll',result:{stdout:'middle\n',session_id:'task-session'}});
 emit({type:'tool_started',id:'task-input',name:'write_stdin',args:{session_id:'task-session',chars:'yes\n'}});emit({type:'tool_state',id:'task-input',phase:'waiting_approval'});view.update({state,record,approvals:[approval]});
 const inputArticle=await until(()=>articleOf('task-input'));const card=await until(()=>live('.approval-card').filter(positive)[0]);const parent=inputArticle.parentElement;
 if(parent!==view.transcript)throw Error('Input leaves the stable Task transcript host');if(!positive(card))throw Error('Approval card collapsed');if(live('.approval-card').length!==1)throw Error('Expected one live approval');if(live('[data-call-id="task-poll"]').length)throw Error('Polling row still live');
 const before=[...parent.children].filter(node=>node.matches('article.message'));
 group.querySelector(':scope > .work-record-prefix > .work-record-summary').click();await frame();if(!positive(live('.approval-card')[0]))throw Error('Approval lost geometry after fold');
 emit({type:'tool_finished',id:'task-input',result:{stdout:'done\n',exit_code:0}});view.update({state,record,approvals:[]});
 await until(()=>!live('.approval-card').length);await until(()=>live('.work-record-group > .work-record-prefix > .work-record-activity').every(node=>node.hidden));
 const finalInput=articleOf('task-input');if(finalInput!==inputArticle)throw Error('Input article replaced');if(inputArticle.parentElement!==parent)throw Error('Input parent changed');if(articleOf('task-command')!==commandArticle)throw Error('Command article replaced');
 const after=[...parent.children].filter(node=>node.matches('article.message'));if(after.length!==before.length)throw Error('Article count changed');if(!before.every((node,index)=>after[index]===node))throw Error('Article order changed');
}finally{view.dispose();root.remove();}
  });
  await page.evaluate(async()=>{
   const {createToolCall}=await import('/components/tool-call.mjs');let language='en';const view=createToolCall({locale:()=>language});document.body.append(view.element);view.element.open=true;
   const message={tool:{id:'locale-action',name:'write_stdin',phase:'completed',args:{session_id:'hidden-session',chars:'yes\n',interrupt:true,terminate:true,close_stdin:true,rows:40,cols:100},result:{stdout:'ready',session_id:'hidden-session'}}};
   try{for(const [locale,input,resize]of [['en','Send input','40 rows × 100 columns'],['zh-CN','发送输入','40 行 × 100 列']]){language=locale;view.update(message);const text=[...view.element.querySelectorAll('.tool-section')].map(node=>node.textContent).join('\n');if(!text.includes(input)||!text.includes(resize)||text.includes('hidden-session'))throw Error('Process interactions switch locale and hide IDs');}
    message.tool={...message.tool,phase:'unknown',started_at_ms:1000};view.update(message);if(view.element.querySelector('.tool-duration').textContent)throw Error('Unknown execution without a finish time must not invent elapsed time');
   }finally{view.element.remove();}
  });
  assert(errors.length===0,errors.join('\n'));return {passed:true,errors,checks:'Chat/Task grouping, receipts, input, approvals, live output, visible failures, expanded diagnostics and mobile layout'};
 }finally{await page.evaluate(()=>window.__processDOMGuard?.close()).catch(()=>{});await page.unroute('**/access/status',access);await page.unroute('**/api/v1/**',handler);page.off('pageerror',onError);}
}
