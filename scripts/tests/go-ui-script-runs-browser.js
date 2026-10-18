// Run through Playwright CLI against the disposable loopback preview.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 const errors=[],onError=error=>errors.push(error.message);page.on('pageerror',onError);
 const at=Date.now()-24000;
 const call=(id,name,args,result,extra={})=>({id:`message-${id}`,role:'tool',text:'',status:'complete',tool:{id,turn_id:'request-u',name,args,result,phase:'completed',started_at_ms:at,finished_at_ms:at+2000,output:[],...extra}});
 const script=(id,cmd)=>call(id,'exec',{code:`const result = await tools.exec_command({cmd: ${JSON.stringify(cmd)}});\ntext(result);`},{completed:false,exec_id:`job-${id}`,output:''});
 const wait=id=>call(`wait-${id}`,'wait',{exec_id:`job-${id}`},{completed:true,output:`${id} output\n`});
 const child=(id,cmd)=>call(`child-${id}`,'exec_command',{cmd},{stdout:`${id} result`,exit_code:0},{parent_id:id});
 let messages=[{id:'u',request_id:'request-u',role:'user',text:'检查项目配置并验证构建结果。',status:'complete'}, {id:'a',role:'assistant',text:'我会检查项目配置，然后验证构建结果。',reply_to:'u',status:'complete'},script('read','cat package.json'),child('read','cat package.json'),wait('read'),script('check','npm run check'),child('check','npm run check'),wait('check'),script('build','npm run build'),child('build','npm run build'),wait('build')];
 let cursor=0,running=false;
 const snapshot=()=>({api_version:3,cursor:++cursor,boot_id:'boot',conversation_id:'alpha',messages,turn:running?{user_message_id:'u',state:'running'}:null,agent:{phase:'ready',model:'test'}});
 await page.addInitScript(()=>{const sources=[];window.EventSource=class extends EventTarget{constructor(url){super();this.url=String(url);sources.push(this);queueMicrotask(()=>this.onopen?.({}));}close(){}};window.__scriptSnapshot=value=>{for(const source of sources)if(source.url.includes('/events'))source.dispatchEvent(new MessageEvent('snapshot',{data:JSON.stringify(value)}));};});
 const access=route=>route.fulfill({json:{authenticated:true,push_available:false}});
 const handler=route=>{const path=new URL(route.request().url()).pathname;return route.fulfill({json:path==='/api/v1/runtime'?{runtime_id:'test',boot_id:'boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Local',kind:'local',connected:true}]}:path==='/api/v1/assistants'?[{id:'alpha',name:'Pot',revision:1}]:path==='/api/v1/snapshot'?snapshot():path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:path==='/api/v1/usage/summary'?{total:{},quota:{active:false,policy:{period:'none'}}}:{items:[]}});};
 await page.route('**/access/status',access);await page.route('**/api/v1/**',handler);
 try{
 await page.setViewportSize({width:1200,height:850});await page.goto('http://127.0.0.1:17702/?theme=light&locale=zh-CN&conversation=alpha');
 await page.locator('[data-call-id="read"]').waitFor({state:'attached'});
 const group=page.locator('.script-group').first(),implemented=await group.count()>0;
 const capture=async suffix=>{for(const [size,width]of [['desktop',1200],['mobile',390]]){await page.setViewportSize({width,height:850});await page.screenshot({path:`docs/pr-images/codex-group-script-executions/scripts-${size}-${suffix}.png`});}};
 assert(implemented,'Script history is available');
 assert(await page.locator('.script-group').count()===1,'Consecutive runs share one group');
 assert(!await group.evaluate(node=>node.open),'Execution history starts collapsed');
 assert(!await group.locator(':scope > summary').textContent().then(text=>/\d+\s*次|const |1\/1/.test(text)),'Closed summary has no counts or code');
 assert(await page.locator('[data-call-id^="wait-"]').count()===0,'Waits update their original execution');
 await capture('after');await page.setViewportSize({width:1200,height:850});
 await group.locator(':scope > summary').click();
 await page.screenshot({path:'docs/pr-images/codex-group-script-executions/script-history-desktop-after.png'});
 const read=page.locator('[data-call-id="read"]');await read.locator(':scope > summary').click();
 await read.locator(':scope > .tool-body > .tool-raw > summary').click();
 assert(await read.locator(':scope > .tool-body > .tool-raw').textContent().then(text=>text.includes('wait-read')&&text.includes('job-read')),'Raw wait receipts remain inspectable');
 assert(await read.locator('.tool-section').allTextContents().then(text=>text.join('\n').includes('read output')),'Collected script output belongs to its run');
 const retained=await group.elementHandle();
 running=true;messages.push(script('verify','npm run verify'),child('verify','npm run verify'));
 messages.at(-1).tool.phase='running';delete messages.at(-1).tool.finished_at_ms;
 await page.evaluate(value=>window.__scriptSnapshot(value),snapshot());
 await group.locator(':scope > summary').getByText('执行中',{exact:true}).waitFor();
 assert(await group.evaluate((node,old)=>node===old,retained),'New runs retain the same group node');
 assert(await read.evaluate(node=>node.open&&node.querySelector(':scope > .tool-body > .tool-raw').open),'New runs preserve open history');
 messages.at(-1).tool.phase='completed';messages.at(-1).tool.finished_at_ms=Date.now();
 await page.evaluate(value=>window.__scriptSnapshot(value),snapshot());
 await group.locator(':scope > summary').getByText('执行中',{exact:true}).waitFor();
 assert(await group.getAttribute('data-phase')==='running','Yielded scripts follow the request ID even when the user message has a different ID');
 messages.at(-1).tool.phase='failed';messages.at(-1).tool.result={exit_code:1,stderr:'Verification failed'};messages.at(-1).tool.finished_at_ms=Date.now();
 messages.push(wait('verify'));running=false;await page.evaluate(value=>window.__scriptSnapshot(value),snapshot());
 await group.locator(':scope > summary').getByText('执行中有失败项',{exact:true}).waitFor();
 await group.locator(':scope > summary').click();
 assert(await group.locator(':scope > summary .tool-failure').textContent()==='Verification failed','A collapsed group retains child failure details');
 await page.setViewportSize({width:390,height:850});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Mobile groups do not overflow');
 await group.locator(':scope > summary .tool-failure').click();
 assert(await page.locator('[data-call-id="child-verify"]').evaluate(node=>node.open),'Selecting a failure reveals its specific operation');
 // Exercise the real Task component with ordered events and inline approval.
 await page.evaluate(async()=>{
  const {createTaskConversation}=await import('/components/task-conversation.mjs');
  const {createTaskTranscript,appendTaskEvents}=await import('/state/task-transcript.mjs');
  const root=document.createElement('div');document.body.append(root);
  const state=createTaskTranscript(),record={info:{phase:'running'},request:{text:'Verify task scripts'}},noop=()=>{};
  const approval={id:'task-approval',request:{task:'task',turn:'task-turn',call_id:'approved-child',machine_id:'test',tool:'exec_command',args:{cmd:'write config'}}};
  const view=createTaskConversation({root,task:{id:'task'},record,locale:()=> 'zh-CN',client:{request:async()=>({items:[]})},scope:{space:'local',owner:'alpha'},draft:'',onDraft:noop,toolMemory:new Map(),approvalFor:tool=>tool.id==='approved-child'?approval:null,onDecision:noop,onSend:noop,onStop:noop,onPause:noop,onCancel:noop,onUsage:noop,onRefresh:noop,onSettings:noop});
  let sequence=0;const emit=update=>appendTaskEvents(state,[{sequence:++sequence,event:{turn_id:'task-turn',update:{at_ms:Date.now(),...update}}}]);
  try{
   emit({type:'tool_started',id:'task-exec',name:'exec',args:{code:'await work()'}});
   emit({type:'tool_finished',id:'task-exec',result:{completed:false,exec_id:'task-job',output:'first\n'}});
   emit({type:'tool_started',id:'task-wait',name:'wait',args:{exec_id:'task-job'}});
   view.update({state,record});
   const group=root.querySelector('.script-group');if(group?.dataset.phase!=='running')throw Error('Task waits remain running in their original group');
   emit({type:'tool_finished',id:'task-wait',result:{completed:true,output:'last\n'}});
   view.update({state,record});
   if(root.querySelectorAll('.script-group').length!==1||root.querySelector('[data-call-id="task-wait"]'))throw Error('Task wait receipts are merged');
   emit({type:'tool_started',id:'approval-exec',name:'exec',args:{code:'await write()'}});
   emit({type:'tool_started',id:'approved-child',parent_id:'approval-exec',name:'exec_command',args:{cmd:'write config'}});
   emit({type:'tool_state',id:'approved-child',phase:'waiting_approval'});
   view.update({state,record,approvals:[approval]});
   await Promise.resolve();
   if(!group.open||!root.querySelector('[data-call-id="approval-exec"]').open||!root.querySelector('[data-call-id="approved-child"]').open)throw Error('Task approvals open all grouped ancestors');
   if(root.querySelectorAll('.approval-card').length!==1)throw Error('Task approval appears exactly once');
  }finally{view.dispose();root.remove();}
 });
 assert(errors.length===0,errors.join('\n'));
 return 'Chat/Task grouping, merged waits, raw receipts, stable disclosure, failure locator, Task approvals and mobile layout passed';
 }finally{await page.unroute('**/api/v1/**',handler);await page.unroute('**/access/status',access);page.off('pageerror',onError);}
}
