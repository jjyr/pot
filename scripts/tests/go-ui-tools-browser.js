// Playwright CLI contract test against the disposable preview. Backend routes are
// fixtures; real Go execution, persistence and approvals have separate Go tests.
async parentPage => {
 const context=await parentPage.context().browser().newContext({serviceWorkers:'block'}),page=await context.newPage();
 const origin=new URL(parentPage.url()).origin;
 const assert=(value,message)=>{if(!value)throw Error(message);};
 const errors=[],posts=[];const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 const at=Date.now()-2000,target={runtime_id:'dev-mac'},capabilities={version:1,operations:['chat.read','chat.send','chat.subscribe','approvals.manage','task.list','task.read','task.events','task.messages.read']};
 const tool=(id,name,args,extra={})=>({id:`message-${id}`,role:'tool',text:'',status:'streaming',tool:{id,turn_id:'run-one',name,args,phase:'running',started_at_ms:at,running_at_ms:at,target,workdir:'/workspace/pot',output:[],...extra}});
 const command=tool('call-one','exec_command',{cmd:'pwd && rg --files -g README.md',workdir:'/workspace/pot'});
 let messages=[{id:'u',request_id:'run-one',role:'user',text:'检查一下当前工作区，确认能否看到 Pot 项目。',status:'complete'},{id:'a',role:'assistant',text:'我检查一下当前工作区，确认能否看到 Pot 项目。',status:'complete',reply_to:'u'},command];
 let revision=1,pending=[],rejectApproval=false,finished=false,taskEvents=[],showOthers=false,showTasks=false;
 const snapshot=()=>({api_version:3,cursor:revision++,boot_id:'boot',conversation_id:'alpha',messages,turn:finished?null:{user_message_id:'u',state:'running'},agent:{phase:'ready',model:'test-model'}});
 const frame=value=>({kind:'reset',base_cursor:0,view:{replies:[],state:{...value,messages:[]},messages:value.messages.map((message,index)=>({message,sequence:String(index+1),version:value.cursor})),history:{before:'1',after:String(value.messages.length),has_more:false,has_newer:false}}});
 await page.addInitScript(()=>{const toUIFrame=value=>({kind:'reset',base_cursor:0,view:{replies:[],state:{...value,messages:[]},messages:value.messages.map((message,index)=>({message,sequence:String(index+1),version:value.cursor})),history:{before:'1',after:String(value.messages.length),has_more:false,has_newer:false}}});const sources=new Set();window.EventSource=class extends EventTarget{constructor(url){super();this.url=String(url);sources.add(this);if(this.url.includes('/events'))window.__toolsSubscribed=true;queueMicrotask(async()=>{this.onopen?.({});if(this.url.includes('/events'))this.dispatchEvent(new MessageEvent('conversation',{data:JSON.stringify(await (await fetch('/api/v1/snapshot')).json())}));});}close(){sources.delete(this);}};window.__toolsSnapshot=value=>{for(const source of sources)if(source.url.includes('/events'))source.dispatchEvent(new MessageEvent('conversation',{data:JSON.stringify(toUIFrame(value))}));};Object.defineProperty(navigator,'clipboard',{value:{writeText:async text=>{window.__toolCopied=text;}}});window.__toolsConnection=online=>{for(const source of sources)if(source.url.includes('/events'))(online?source.onopen:source.onerror)?.({});};window.__toolsChanges=value=>{for(const source of sources)if(source.url.includes('/changes'))source.dispatchEvent(new MessageEvent('changed',{data:JSON.stringify(value)}));};});
 const routeHandler=async route=>{
  const url=new URL(route.request().url()),path=url.pathname;
  if(route.request().method()==='POST'){const body=route.request().postDataJSON();posts.push({path,body,owner:url.searchParams.get('assistant_id'),space:url.searchParams.get('space_id')});if(path==='/api/v1/permissions'){if(rejectApproval){await route.fulfill({status:503,json:{message:'Decision failed; try again'}});return;}pending=[];}}
  const data=path==='/api/v1/runtime'?{runtime_id:'test',boot_id:'boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Local',kind:'local',connected:true}]}:path==='/api/v1/assistants'?[{id:'alpha',name:'Pot',revision:1,capabilities},...(showOthers?[{id:'beta',name:'Other',revision:1,capabilities}]:[])]:path==='/api/v1/assistants/bootstrap'?{assistant:null}:path==='/api/v1/snapshot'?frame(snapshot()):path==='/api/v1/history'?{boot_id:'boot',conversation_id:'alpha',cursor:revision,messages:messages.map((message,index)=>({message,sequence:String(index+1),version:revision})),history:{before:'1',after:String(messages.length),has_more:false,has_newer:false}}:path==='/api/v1/permissions'?{pending,policy:{machines:{}}}:path==='/api/v1/usage/summary'?{total:{},quota:{active:false,policy:{period:'none'}}}:path==='/api/v1/tasks/outcomes'?{items:[],cursor:0,has_more:false}:(path==='/api/v1/tasks/query'||path==='/api/v1/tasks/catalog')?{items:showTasks&&(url.searchParams.get('assistant_id')==='alpha'||path==='/api/v1/tasks/catalog')?[{id:'task-proof',title:'Tool transcript',assistant_id:'alpha',phase:'running'}]:[],total:1}:path==='/api/v1/tasks/task-proof'?{run_id:'run-one',capabilities,info:{phase:'running'},request:{text:'Verify tool transcript'}}:path==='/api/v1/tasks/task-proof/events'?{events:taskEvents,cursor:taskEvents.at(-1)?.sequence||0,has_more:false}:path==='/api/v1/tasks/task-proof/messages'?[]:{items:[]};
  await route.fulfill({json:data});
 };
 const access=route=>route.fulfill({json:{authenticated:true}});
 await page.route('**/access/status',access);await page.route('**/api/v1/**',routeHandler);
 const push=async()=>{await page.evaluate(value=>window.__toolsSnapshot(value),snapshot());await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.locator('.script-group').evaluateAll(nodes=>nodes.forEach(node=>node.open=true));};
 const approvalCard=id=>page.locator(`.chat-approvals .approval-card[data-approval-id="${id}"]`);
 const review=async id=>{await approvalCard(id).locator(':scope > summary').click();};
 const call=id=>page.locator(`.tool-call[data-call-id="${id}"]`);
 try{
  await page.setViewportSize({width:1200,height:850});await page.goto(`${origin}/?theme=light&locale=zh-CN&conversation=alpha`);await page.waitForFunction(()=>window.__toolsSubscribed);await push();
  await call('call-one').waitFor();assert(!await call('call-one').evaluate(node=>node.open),'Ordinary tools default to collapsed');
  await call('call-one').locator(':scope > summary').focus();await page.keyboard.press('Enter');
  await call('call-one').locator(':scope > summary').evaluate(node=>node.blur());
  assert(await call('call-one').getByText('pwd && rg --files -g README.md',{exact:true}).count()>=1,'Actual command is visible');
  await page.screenshot({path:'output/pr-images/tool-call-details/chat-tools-desktop-after.png'});
  await page.setViewportSize({width:390,height:850});await page.screenshot({path:'output/pr-images/tool-call-details/chat-tools-mobile-after.png'});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth&&document.querySelector('.history').scrollWidth<=document.querySelector('.history').clientWidth),'No narrow horizontal overflow');
  await page.setViewportSize({width:1200,height:850});
  await call('call-one').locator('.tool-section').first().getByRole('button',{name:'复制',exact:true}).click();assert(await page.evaluate(()=>window.__toolCopied)==='pwd && rg --files -g README.md','Copy uses full actual command');
  await page.evaluate(()=>window.__toolsConnection(false));await call('call-one').getByText('连接中断，正在确认状态',{exact:true}).waitFor();assert(await call('call-one').getAttribute('data-phase')==='running','Connection loss does not invent tool failure');await page.evaluate(()=>window.__toolsConnection(true));
  command.tool.output=[{stream:'stdout',text:Array.from({length:70},(_,i)=>`retained line ${i}`).join('\n')}];await push();
  const output=call('call-one').locator('.tool-section').filter({has:page.getByText('输出',{exact:true})}).locator('pre');
  await output.getByText('retained line 0',{exact:false}).waitFor();
  await output.evaluate(node=>{node.scrollTop=0;node.dispatchEvent(new Event('scroll'));});
  command.tool.output[0].text+='\nnew output';await push();
  await page.waitForFunction(()=>document.querySelector('[data-call-id="call-one"] .tool-code')&&document.querySelector('[data-call-id="call-one"]').textContent.includes('new output'));
  assert(await call('call-one').evaluate(node=>node.open),'Streaming keeps disclosure open');assert(await output.evaluate(node=>node.scrollTop)===0,'Reading output pauses following');
  await call('call-one').getByRole('button',{name:'回到最新输出',exact:true}).click();assert(await output.evaluate(node=>node.scrollTop>0),'Explicit latest output resumes following');
  command.tool.output[0].text='old-content\n'+('long output '.repeat(3000))+'\nLATEST-MARKER';await push();await output.getByText('LATEST-MARKER',{exact:false}).waitFor();
  assert(await call('call-one').getByText('显示已缩短，复制可获取完整保留内容。',{exact:true}).count()>0,'Large output is labeled');
  command.tool.output[0].text='retained line 0\nnew output';
  command.status='complete';command.tool.phase='completed';command.tool.finished_at_ms=at+500;command.tool.result={stdout:'final receipt',stderr:'',exit_code:0};await push();
  await call('call-one').getByText('退出码 0',{exact:true}).waitFor();assert(await call('call-one').locator('.tool-excerpt').textContent()==='pwd && rg --files -g README.md','Result cannot overwrite title');assert(await call('call-one').textContent().then(text=>text.includes('retained line 0')),'Completion preserves stream');
  messages.push(tool('create-task','create_task',{text:'Install GitHub CLI'}, {phase:'completed'}),tool('get-task','get_task',{task_id:'private-task-id'}, {phase:'completed'}),tool('wait-task','wait_task',{task_ids:['private-task-id']}, {phase:'completed'}));await push();
  await call('get-task').waitFor();
  for(const [id,label]of [['create-task','委派任务'],['get-task','查看进度'],['wait-task','等待任务']]){
   assert(await call(id).locator('.tool-title').textContent()===label,'Task operations have readable labels');
   assert(!await call(id).locator('.tool-excerpt').isVisible(),'Redundant argument preview stays hidden');
   assert(!await call(id).locator(':scope > summary').textContent().then(text=>text.includes('private-task-id')),'Collapsed coordination does not expose IDs');
   if(id!=='create-task')assert(await call(id).evaluate(node=>node.getBoundingClientRect().height)<=30,'Desktop coordination row stays compact');
  }
  await page.setViewportSize({width:390,height:850});
  assert(await call('get-task').evaluate(node=>node.getBoundingClientRect().height)<=38,'Narrow coordination row stays on one line');
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Narrow coordination fits viewport');
  await call('get-task').locator(':scope > summary').click();
  await call('get-task').locator('.tool-raw > summary').click();
  await call('get-task').locator('.tool-raw .tool-call-id').first().waitFor();
  await call('get-task').getByText('get_task',{exact:true}).waitFor();
  assert(await call('get-task').locator('.tool-raw .tool-code').first().textContent().then(text=>text.includes('private-task-id')),'Secondary raw details retain actual tool arguments');
  await call('get-task').locator(':scope > summary').click();await page.setViewportSize({width:1200,height:850});
  const parent=tool('exec-one','exec',{code:'const results = await Promise.all([tools.exec_command({cmd:"pwd"}), tools.exec_command({cmd:"git status"})]);\ntext(results);'},{phase:'completed',target:null,workdir:'',result:{completed:false,exec_id:'job',output:''}}),child=tool('child-one','exec_command',{cmd:'git status'},{parent_id:'exec-one',phase:'waiting_approval',running_at_ms:0,target:{runtime_id:'dev-mac'}});
  messages.push(parent,child);pending=[{id:'approval-one',request:{owner:'alpha',turn:'run-one',call_id:'child-one',machine_id:'dev-mac',tool:'exec_command',args:{cmd:'git status'}}}];await push();await page.evaluate(()=>window.__toolsChanges({permissions:2}));
  await review('approval-one');await approvalCard('approval-one').getByRole('button',{name:'本任务允许',exact:true}).waitFor();
  assert(!await call('exec-one').evaluate(node=>node.open),'Chat approval review is independent of tool disclosure');await call('exec-one').evaluate(node=>{node.open=true;node.closest('.script-group').open=true;});await call('child-one').waitFor();assert(await call('exec-one').locator('.tool-children [data-call-id="child-one"]').count()===1,'Composition child is nested exactly once');
  assert(await page.locator('.approval-card').count()===1,'Approval has one shared card in the Chat region');
  await call('call-one').evaluate(node=>node.open=false);await call('exec-one').evaluate(node=>node.scrollIntoView({block:'start'}));
  await page.screenshot({path:'output/pr-images/tool-call-details/nested-approval-desktop-after.png'});
  await call('call-one').evaluate(node=>node.open=true);
  assert(await approvalCard('approval-one').getByRole('button',{name:'始终允许此命令',exact:true}).count()===1,'Ordinary execution offers exact-command persistent authorization');
  rejectApproval=true;await approvalCard('approval-one').getByRole('button',{name:'本任务允许',exact:true}).click();await approvalCard('approval-one').getByText('Decision failed; try again',{exact:true}).waitFor();assert(!await approvalCard('approval-one').getByRole('button',{name:'拒绝',exact:true}).isDisabled(),'Failed decision keeps actions available');
  rejectApproval=false;await approvalCard('approval-one').getByRole('button',{name:'拒绝',exact:true}).click();assert(posts.some(post=>post.body.id==='approval-one'&&post.body.action==='deny'&&post.owner==='alpha'&&post.space==='local'),'Decision binds exact approval and scope');
  child.tool.phase='denied';child.tool.result={isError:true,error:'Execution operation denied'};child.status='failed';await push();await call('child-one').evaluate(node=>node.open=true);await call('child-one').locator(':scope > summary').click();await call('child-one').locator(':scope > summary').getByText('已结束',{exact:true}).waitFor();assert(!await call('child-one').locator(':scope > .tool-body > .tool-failure').isVisible(),'Collapsed errors stay in details');await call('child-one').locator(':scope > summary').click();await call('child-one').locator(':scope > summary').getByText('已拒绝',{exact:true}).waitFor();await call('child-one').locator(':scope > .tool-body > .tool-failure').getByText('Execution operation denied',{exact:true}).waitFor();
  messages.push(tool('session-one','exec_command',{cmd:'npm run dev'},{phase:'completed',result:{stdout:'server ready',stderr:'',exit_code:null,session_id:'process-one'}}));await push();await call('session-one').locator(':scope > summary').getByText('执行中',{exact:true}).waitFor();
  messages.push(tool('patch-one','apply_patch',{patch:'*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End Patch'},{phase:'completed',result:{output:'Updated a.txt'}}));await push();await call('patch-one').locator(':scope > summary').click();assert(await call('patch-one').locator('.tool-diff-add').textContent()==='+new\n','Patch preview highlights actual additions');assert(await call('patch-one').getByText('a.txt · 修改',{exact:true}).count()===1,'Patch preview lists affected files');
  messages.push(tool('image-one','inspect_image',{path:'result.png'},{phase:'completed',result:{content:[{type:'image',mimeType:'image/png',data:'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/nLsAAAAASUVORK5CYII='}]}}));await push();await call('image-one').locator(':scope > summary').click();assert(await call('image-one').locator('img').count()===1,'Supported tool image result renders inline');
  await page.locator('.history').evaluate(node=>{node.scrollTop=0;node.dispatchEvent(new Event('scroll'));});await call('call-one').waitFor();
  await call('call-one').locator('.tool-raw > summary').click();await call('call-one').getByText('exec_command · call-one',{exact:true}).waitFor();
  showOthers=true;await page.evaluate(()=>window.__toolsChanges({assistants:5}));await page.locator('.assistant-row').filter({hasText:'Other'}).click();await page.locator('.assistant-row').filter({hasText:'Pot'}).click();await call('call-one').waitFor();assert(await call('call-one').evaluate(node=>node.open&&node.querySelector('.tool-raw').open),'Navigation restores scoped disclosure state');
  messages.push(tool('grant-one','exec_command',{cmd:'git status'}));pending=[{id:'persistent-one',request:{owner:'alpha',turn:'run-one',call_id:'grant-one',machine_id:'dev-mac',tool:'exec_command',args:{cmd:'git status'}}}];await push();await page.evaluate(()=>window.__toolsChanges({permissions:7}));
  await review('persistent-one');await approvalCard('persistent-one').getByRole('button',{name:'始终允许此命令',exact:true}).click();assert(posts.some(post=>post.body.id==='persistent-one'&&post.body.action==='allow_always'&&post.owner==='alpha'&&post.space==='local'),'Persistent decision binds original approval and Space');
  messages.push(tool('rules-one','apply_patch',{patch:'*** Begin Patch\n*** Add File: /tmp/reviewed-rules\n+Reviewed rules\n*** End Patch',request_unsandboxed:true,justification:'Edit the requested file'}));pending=[{id:'rules-one',request:{owner:'alpha',turn:'run-one',call_id:'rules-one',machine_id:'dev-mac',tool:'apply_patch',args:{patch:'*** Begin Patch\n*** Add File: /tmp/reviewed-rules\n+Reviewed rules\n*** End Patch',request_unsandboxed:true,justification:'Edit the requested file'}}}];await push();await page.evaluate(()=>window.__toolsChanges({permissions:8}));
  await review('rules-one');await approvalCard('rules-one').getByRole('button',{name:'允许一次',exact:true}).waitFor();assert(await approvalCard('rules-one').getByRole('button',{name:'始终允许此命令',exact:true}).count()===0,'Host patches offer one-time review without command grants');
  // Same events feed the shared component in Task details, including terminal unknown.
  taskEvents=[{sequence:1,event:{turn_id:'run-one',update:{type:'tool_started',id:'task-call',name:'exec_command',args:{cmd:'pwd'},at_ms:at}}},{sequence:2,event:{turn_id:'run-one',update:{type:'tool_output',id:'task-call',stream:'stdout',text:'task stream',at_ms:at+5}}},{sequence:3,event:{turn_id:'run-one',update:{type:'turn_finished',outcome:{kind:'unknown',error:'Result uncertain'},at_ms:at+10}}}];
  showTasks=true;await page.getByRole('button',{name:'任务',exact:true}).first().click();await page.getByRole('button',{name:'Tool transcript',exact:true}).click();await page.locator('.task-transcript .script-group > summary').click();await call('task-call').getByText('结果待确认',{exact:true}).waitFor();await call('task-call').locator(':scope > summary').click();await call('task-call').getByText('task stream',{exact:true}).waitFor();
  await page.setViewportSize({width:390,height:850});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Task details fit narrow viewport');
  assert(errors.length===0,'No browser exceptions: '+errors.join('; '));
  return {checks:'disclosure, stream preservation, scroll following, nested approvals, exact decisions, failure recovery, sessions, owner memory, narrow layout and Task rendering',posts:posts.length};
 }finally{await page.unroute('**/api/v1/**',routeHandler);await page.unroute('**/access/status',access);page.off('pageerror',onError);await context.close();}
}
