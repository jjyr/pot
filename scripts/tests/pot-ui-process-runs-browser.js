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
 await page.addInitScript(()=>{const sources=[];window.EventSource=class extends EventTarget{constructor(url){super();this.url=String(url);sources.push(this);if(this.url.includes('/events'))window.__processSubscribed=true;queueMicrotask(()=>this.onopen?.({}));}close(){}};window.__processSnapshot=frame=>{for(const source of sources)if(source.url.includes('/events'))source.dispatchEvent(new MessageEvent('conversation',{data:JSON.stringify(frame)}));};Object.defineProperty(navigator,'clipboard',{value:{writeText:async text=>{window.__processCopied=text;}}});});
 const access=route=>route.fulfill({json:{authenticated:true,push_available:false}});
 const handler=route=>{const path=new URL(route.request().url()).pathname;return route.fulfill({json:path==='/api/v1/runtime'?{runtime_id:runtimeID,boot_id:'boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',control_id:runtimeID,name:'Local',kind:'local',connected:true}]}:path==='/api/v1/assistants'?[{id:'alpha',name:'Pot',revision:1}]:path==='/api/v1/snapshot'?frame(snapshot()):path==='/api/v1/history'?{boot_id:'boot',conversation_id:'alpha',cursor:cursor,messages:messages.map((message,index)=>({message,sequence:String(index+1),version:cursor})),history:{before:'1',after:String(messages.length),has_more:false,has_newer:false}}:path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:path==='/api/v1/usage/summary'?{total:{},quota:{active:false,policy:{period:'none'}}}:{items:[]}});};
 await page.route('**/access/status',access);await page.route('**/api/v1/**',handler);
 const node=id=>page.locator(`[data-call-id="${id}"]`),push=()=>page.evaluate(value=>window.__processSnapshot(value),frame(snapshot()));
 const capture=async suffix=>{for(const [size,width]of [['desktop',1200],['mobile',390]]){await page.setViewportSize({width,height:850});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.locator('.history').screenshot({path:`output/pr-images/codex-command-sessions/commands-${size}-${suffix}.png`});}};
 try{
  await page.setViewportSize({width:1200,height:850});await page.goto(`${origin}/?theme=light&locale=zh-CN&conversation=alpha`);
  await page.waitForFunction(()=>window.__processSubscribed);await push();
  await page.getByText('我会检查项目，然后验证构建结果。',{exact:true}).waitFor();
  const group=page.locator('.script-group').first();
  if(!await group.count()){await node('check-done').waitFor();await capture('before');return 'Baseline command/session screenshots captured';}
  await group.waitFor();assert(await page.locator('.script-group').count()===1,'Commands share one group');assert(!await group.evaluate(element=>element.open),'Execution history starts collapsed');
  assert(await group.locator(':scope > summary .tool-title').textContent()==='执行记录','Group has a stable generic title');
  await capture('after');await page.setViewportSize({width:1200,height:850});await group.locator(':scope > summary').click();
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
  runtimeID='process-live';await page.reload();await page.waitForFunction(()=>window.__processSubscribed);await push();
  await group.locator(':scope > summary').click();await node('check').locator(':scope > summary').click();

  middle.tool.output=[{stream:'stdout',text:Array.from({length:90},(_,i)=>`line ${i}`).join('\n')}];middle.tool.result.stdout=middle.tool.output[0].text;
  await push();await node('check').locator(':scope > summary').getByText('执行中',{exact:true}).waitFor();
  await output.getByText('line 89',{exact:false}).waitFor();
  const retained=await output.elementHandle();await output.evaluate(element=>{element.scrollTop=0;element.dispatchEvent(new Event('scroll'));});
  middle.tool.output[0].text+='\nLATEST';middle.tool.result.stdout=middle.tool.output[0].text;await push();await output.getByText('LATEST',{exact:false}).waitFor();
  assert(await output.evaluate((element,old)=>element===old,retained),'Output node survives session updates');assert(await output.evaluate(element=>element.scrollTop)===0,'Paused output following survives updates');
  await node('check').getByRole('button',{name:'回到最新输出',exact:true}).click();assert(await output.evaluate(element=>element.scrollTop>0),'Jump to latest resumes following');
  const input=call('send-input','write_stdin',{session_id:'private-process-one',chars:'yes\n'},{session_id:'private-process-one'});messages.push(input);await push();
  await node('send-input').waitFor();assert(await node('send-input').evaluate(element=>element.parentElement.closest('.tool-call').dataset.callId)==='check','Input action is nested beneath its command');
  await node('send-input').locator(':scope > summary').click();await node('send-input').getByText('发送输入\nyes',{exact:false}).waitFor();
  assert(!await node('send-input').locator(':scope > summary').textContent().then(text=>text.includes('private-process-one')),'Action summary hides session ID');
  const failed=poll('failed-poll','private-process-one',{stderr:'Checks failed',exit_code:2});messages.push(failed,secondDone);running=false;await push();
  assert(await group.locator(':scope > summary .tool-failure').count()===0,'Group summaries keep intermediate process failures quiet');
  await node('check').locator('.tool-section').filter({has:page.getByText('输出',{exact:true})}).getByText('Checks failed',{exact:false}).waitFor();
  await node('check').locator(':scope > summary').click();assert(!await node('check').evaluate(element=>element.open),'Completed process failure can be collapsed');await node('check').locator(':scope > summary').getByText('已结束',{exact:true}).waitFor();
  assert(!await node('check').locator(':scope > summary').textContent().then(text=>text.includes('Checks failed')),'Collapsed command summary hides the diagnostic');
  await node('check').locator(':scope > summary').click();await node('check').getByText('Checks failed',{exact:false}).first().waitFor();await node('check').locator(':scope > summary').getByText('失败',{exact:true}).waitFor();
  await page.setViewportSize({width:390,height:850});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Expanded history fits mobile');
  // Ordered Task events exercise command completion and exact approval ancestry.
  await page.evaluate(async()=>{
   const {createTaskConversation}=await import('/components/task-conversation.mjs'),{createTaskTranscript,appendTaskEvents}=await import('/state/task-transcript.mjs');
   const root=document.createElement('div');document.body.append(root);const state=createTaskTranscript(),record={info:{phase:'running'},request:{text:'Verify process sessions'}},noop=()=>{};
   const approval={id:'process-approval',request:{task:'task',turn:'task-turn',call_id:'task-input',machine_id:'test',tool:'write_stdin',args:{session_id:'task-session',chars:'yes\n'}}};
   const view=createTaskConversation({root,task:{id:'task'},record,locale:()=> 'zh-CN',client:{request:async()=>({items:[]})},scope:{space:'local',owner:'alpha'},draft:'',onDraft:noop,toolMemory:new Map(),approvalFor:tool=>tool.id==='task-input'?approval:null,onDecision:noop,onSend:noop,onStop:noop,onPause:noop,onCancel:noop,onUsage:noop,onRefresh:noop,onSettings:noop});
   let sequence=0;const emit=update=>appendTaskEvents(state,[{sequence:++sequence,event:{turn_id:'task-turn',update:{at_ms:Date.now(),...update}}}]);
   try{
    emit({type:'tool_started',id:'task-command',name:'exec_command',args:{cmd:'npm run check'}});emit({type:'tool_finished',id:'task-command',result:{stdout:'start\n',session_id:'task-session'}});view.update({state,record});
    const group=root.querySelector('.script-group');if(group?.dataset.phase!=='running')throw Error('Task session stays running');
    emit({type:'tool_started',id:'task-poll',name:'write_stdin',args:{session_id:'task-session'}});emit({type:'tool_finished',id:'task-poll',result:{stdout:'middle\n',session_id:'task-session'}});
    emit({type:'tool_started',id:'task-input',name:'write_stdin',args:{session_id:'task-session',chars:'yes\n'}});emit({type:'tool_state',id:'task-input',phase:'waiting_approval'});view.update({state,record,approvals:[approval]});await Promise.resolve();
    if(!group.open||!root.querySelector('[data-call-id="task-command"]').open||!root.querySelector('[data-call-id="task-input"]').open)throw Error('Session approval opens command and group ancestors');
    if(root.querySelectorAll('.approval-card').length!==1||root.querySelector('[data-call-id="task-poll"]'))throw Error('One approval and no polling row');
    emit({type:'tool_finished',id:'task-input',result:{stdout:'done\n',exit_code:0}});view.update({state,record,approvals:[]});if(group.dataset.phase!=='completed')throw Error('Final process receipt completes Task group');
   }finally{view.dispose();root.remove();}
  });
  await page.evaluate(async()=>{
   const {createToolCall}=await import('/components/tool-call.mjs');let language='en';const view=createToolCall({locale:()=>language});document.body.append(view.element);view.element.open=true;
   const message={tool:{id:'locale-action',name:'write_stdin',phase:'completed',args:{session_id:'hidden-session',chars:'yes\n',interrupt:true,terminate:true,close_stdin:true,rows:40,cols:100},result:{stdout:'ready',session_id:'hidden-session'}}};
   try{for(const [locale,input,resize]of [['en','Send input','40 rows × 100 columns'],['zh-CN','发送输入','40 行 × 100 列']]){language=locale;view.update(message);const text=[...view.element.querySelectorAll('.tool-section')].map(node=>node.textContent).join('\n');if(!text.includes(input)||!text.includes(resize)||text.includes('hidden-session'))throw Error('Process interactions switch locale and hide IDs');}}finally{view.element.remove();}
  });
  assert(errors.length===0,errors.join('\n'));return {passed:true,errors,checks:'Chat/Task grouping, receipts, input, approvals, live output, quiet failures, expanded diagnostics and mobile layout'};
 }finally{await page.unroute('**/access/status',access);await page.unroute('**/api/v1/**',handler);page.off('pageerror',onError);}
}
