// Playwright CLI contract against the disposable preview on port 17702.
// HTTP/SSE responses are fixtures; no model or product data is used.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 const errors=[],reads=[],posts=[];const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 const goal='安装 GitHub CLI 并验证可用。先检查操作系统与当前安装状态，再报告安装结果。';
 let phase='running',paused=false,result=null,version=1,offline=false,rejectGuidance=false,failEvents=false,errorText='',pending=[];
 let inputs=[{request_id:'initial',kind:'input',state:'active',text:goal}];
 const event=(sequence,type,value={})=>({sequence,event:{turn_id:'run-one',update:{type,...value}}});
 let events=[event(1,'message_started'),event(2,'text_delta',{text:'我先检查当前系统，以及 gh 是否已经安装。'}),event(3,'message_finished'),event(4,'tool_started',{id:'probe',name:'exec_command',args:{cmd:'gh --version'}})];
 const add=(type,value={})=>events.push(event(events.length+1,type,value));
 await page.addInitScript(()=>{const toUIFrame=value=>({kind:'reset',base_cursor:0,view:{state:{...value,messages:[]},messages:value.messages.map((message,index)=>({message,sequence:String(index+1),version:value.cursor})),history:{before:'1',after:String(value.messages.length),has_more:false,has_newer:false}}});const sources=new Set();window.EventSource=class extends EventTarget{constructor(url){super();this.url=String(url);sources.add(this);queueMicrotask(async()=>{this.onopen?.({});if(this.url.includes('/events')){const target=new URL(this.url,location.href);target.pathname=target.pathname.replace(/\/events$/,'/snapshot');const snapshot=await(await fetch(target)).json();if(sources.has(this))this.dispatchEvent(new MessageEvent('conversation',{data:JSON.stringify(toUIFrame(snapshot))}));}});}close(){sources.delete(this);}};window.__taskChanges=value=>{for(const source of sources)if(source.url.includes('/changes'))source.dispatchEvent(new MessageEvent('changed',{data:JSON.stringify({tasks:value,permissions:value})}));};});
 const routeHandler=async route=>{
  const url=new URL(route.request().url()),path=url.pathname;
  if(path.startsWith('/api/v1/tasks/task-proof'))reads.push({path,after:url.searchParams.get('after'),owner:url.searchParams.get('assistant_id'),space:url.searchParams.get('space_id')});
  if(failEvents&&path.endsWith('/events')){await route.fulfill({status:503,json:{message:'Task transport unavailable'}});return;}
  if(route.request().method()==='POST'){
   const body=route.request().postDataJSON();posts.push({path,body,owner:url.searchParams.get('assistant_id'),space:url.searchParams.get('space_id')});
   if(path.endsWith('/control'))paused=body.action==='pause';
   if(path.endsWith('/interrupt'))phase='interrupted';
   if(path.endsWith('/cancel'))phase='cancelled';
   if(path==='/api/v1/permissions')pending=pending.filter(item=>item.id!==body.id);
   if(path.endsWith('/messages')){if(rejectGuidance){await route.fulfill({status:503,json:{message:'Guidance failed; try again'}});return;}if(!inputs.some(input=>input.request_id===body.request_id))inputs.push({...body,kind:'user_guidance',sender:'alpha',state:'pending'});}
  }
  const data=path==='/api/v1/runtime'?{runtime_id:'fixture',boot_id:'boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Local',kind:'local',connected:true}]}:path==='/api/v1/assistants'?[{id:'alpha',name:'Pot',revision:1}]:path==='/api/v1/assistants/bootstrap'?{assistant:null}:path==='/api/v1/snapshot'?{cursor:1,boot_id:'boot',conversation_id:'alpha',messages:[],turn:null,agent:{phase:'ready'}}:path==='/api/v1/permissions'?{pending,policy:{machines:{}}}:path==='/api/v1/tasks/outcomes'?{items:[],cursor:0,has_more:false}:path==='/api/v1/tasks/query'?{items:[{id:'task-proof',title:'安装 GitHub CLI 并验证可用',assistant_id:'alpha',machine_id:'fixture-runtime',phase}],total:1}:path==='/api/v1/tasks/task-proof'?{run_id:'run-one',info:{phase,paused,machine_id:'fixture-runtime'},request:{text:goal},result,error:errorText,config:{model:'fixture-model'}}:path==='/api/v1/tasks/task-proof/messages'?inputs:path==='/api/v1/tasks/task-proof/events'?{events:events.filter(row=>row.sequence>Number(url.searchParams.get('after'))),cursor:events.at(-1)?.sequence||0,has_more:false,unavailable:offline}:path==='/api/v1/completions'?{items:[{token:'/skill:review',label:'Review',detail:'Review changes'}]}:{items:[]};
  await route.fulfill({json:data});
 };
 const access=route=>route.fulfill({json:{authenticated:true}});
 await page.route('**/access/status',access);await page.route('**/api/v1/**',routeHandler);
 const push=()=>page.evaluate(value=>window.__taskChanges(value),++version);
 const menu=()=>page.getByRole('button',{name:'任务操作',exact:true});
 const openTask=async()=>{await page.getByRole('button',{name:'任务',exact:true}).first().click();await page.getByRole('button',{name:'安装 GitHub CLI 并验证可用',exact:true}).click();await page.locator('.task-conversation').waitFor();};
 try{
  await page.setViewportSize({width:1200,height:1000});await page.goto('http://127.0.0.1:17702/?theme=light&locale=zh-CN&conversation=alpha');await openTask();
  await page.locator('.task-transcript .reply-bubble').getByText('我先检查当前系统，以及 gh 是否已经安装。',{exact:true}).waitFor();
  const composer=page.getByRole('textbox',{name:'任务指导',exact:true}),tool=page.locator('.tool-call[data-call-id="probe"]');
  assert(!await page.locator('.task-goal').evaluate(node=>node.open),'Goal defaults to collapsed');
  assert(await page.locator('.task-transcript').getByText(goal,{exact:true}).count()===0,'Goal is not repeated in the conversation');
  assert(await page.getByRole('button',{name:'停止任务',exact:true}).count()===1,'Empty composer offers Stop');
  assert(await composer.evaluate(node=>document.activeElement!==node),'Opening Task does not focus the composer');
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.screenshot({path:'output/pr-images/task-progress/task-desktop-after.png'});
  await page.setViewportSize({width:390,height:1000});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.screenshot({path:'output/pr-images/task-progress/task-mobile-after.png'});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Narrow task detail fits viewport');
  await page.setViewportSize({width:1200,height:1000});
  await composer.fill('/skill:r');await page.locator('.task-composer-area .completion-option').first().waitFor();const beforeCompletion=posts.length;await composer.press('Enter');assert(await composer.inputValue()==='/skill:review '&&posts.length===beforeCompletion,'Scoped completion chooses a reference without sending');
  assert(await composer.getAttribute('aria-controls')==='pot-task-task-proof-completions','Task and main composers use distinct completion identities');
  await page.locator('.task-goal > summary').click();await composer.fill('保留这条尚未发送的指导');await page.locator('.task-transcript .script-group > summary').click();await tool.locator(':scope > summary').click();
  await composer.evaluate(node=>{window.__taskComposer=node;});
  inputs.push({request_id:'reference',kind:'reference',sender:'peer',state:'pending',text:'已确认仓库访问可用。'});
  pending=[{id:'approval-one',request:{owner:'alpha',task:'task-proof',turn:'run-one',call_id:'probe',machine_id:'fixture-runtime',tool:'exec_command',args:{cmd:'gh --version'},justification:'Verify the installed CLI'}},{id:'other-task',request:{owner:'alpha',task:'elsewhere',turn:'other',call_id:'other'}}];await push();
  await page.locator('.task-status').getByText('等待授权 · 1 项待处理',{exact:true}).waitFor();
  await tool.locator('.approval-card>summary').click();await tool.getByRole('button',{name:'本任务允许',exact:true}).waitFor();
  await page.screenshot({path:'output/pr-images/task-progress/task-approval-desktop-after.png'});await page.setViewportSize({width:390,height:1000});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.screenshot({path:'output/pr-images/task-progress/task-approval-mobile-after.png'});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Inline approvals fit narrow viewport');await page.setViewportSize({width:1200,height:1000});
  assert(await page.locator('.approval-host').textContent()==='','Task approvals have no duplicate outer locator');
  assert(await page.locator('.role-reference').getByText('参考信息 · peer',{exact:true}).count()===1,'Reference provenance is explicit');
  await page.getByRole('button',{name:'查看授权',exact:true}).click();
  await tool.getByRole('button',{name:'本任务允许',exact:true}).click();
  assert(posts.some(post=>post.path==='/api/v1/permissions'&&post.body.id==='approval-one'&&post.body.action==='allow_task'&&post.owner==='alpha'),'Inline approval retains exact Task ownership');
  add('tool_output',{id:'probe',stream:'stdout',text:'gh version fixture'});add('tool_finished',{id:'probe',result:{exit_code:0}});add('input_applied',{id:'reference'});inputs[1].state='consumed';add('message_started');add('text_delta',{text:'已找到 GitHub CLI，正在验证工具调用。'});await push();
  await page.locator('.task-transcript').getByText('已找到 GitHub CLI，正在验证工具调用。',{exact:true}).waitFor();
  assert(await composer.inputValue()==='保留这条尚未发送的指导','Invalidation retains draft');
  assert(await composer.evaluate(node=>node===window.__taskComposer),'Invalidation retains editor node');
  assert(await tool.evaluate(node=>node.open),'Invalidation retains expanded tool');
  assert(await page.locator('.task-goal').evaluate(node=>node.open),'Invalidation retains expanded goal');
  const order=await page.locator('.task-event').allTextContents();assert(order.findIndex(text=>text.includes('参考信息'))<order.findIndex(text=>text.includes('已找到 GitHub CLI')),'Applied reference precedes continuation');
  await menu().click();await page.getByRole('button',{name:'暂停任务',exact:true}).click();await page.locator('.task-status').getByText('已暂停',{exact:true}).waitFor();
  await menu().click();await page.getByRole('button',{name:'任务详情',exact:true}).click();await page.getByText('fixture-model',{exact:true}).waitFor();await page.locator('.task-actions').getByRole('button',{name:'关闭',exact:true}).click();
  assert(posts.some(post=>post.body.run_id==='run-one'&&post.body.action==='pause'),'Pause uses original run');
  await menu().click();await page.getByRole('button',{name:'恢复任务',exact:true}).click();
  // IME candidate confirmation must not submit guidance.
  const postCount=posts.length;await composer.evaluate(node=>{node.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));node.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',keyCode:229,isComposing:true,bubbles:true,cancelable:true}));node.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));});
  assert(posts.length===postCount,'IME confirmation cannot send guidance');
  // A failed send preserves the editor and its request identity for a retry.
  rejectGuidance=true;await page.getByRole('button',{name:'发送指导',exact:true}).click();await page.getByText('Guidance failed; try again',{exact:true}).waitFor();const failedRequest=posts.at(-1).body.request_id;
  assert(await composer.inputValue()==='保留这条尚未发送的指导','Failed send retains draft');rejectGuidance=false;
  await page.getByRole('button',{name:'发送指导',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.task-conversation .composer-input').value==='');
  assert(posts.filter(post=>post.path.endsWith('/messages')).at(-1).body.request_id===failedRequest,'Retry retains guidance request identity');
  await page.locator('.role-user').getByText('你 · 指导',{exact:true}).waitFor();
  await composer.fill('完成时也保留这条草稿 /skill:r');await composer.press('Tab');
  // Long history follows the bottom only until the reader scrolls upward.
  for(let index=0;index<25;index++){add('message_finished');add('message_started');add('text_delta',{text:`验证步骤 ${index+1}：工具返回了预期结果。`});}await push();
  await page.locator('.task-transcript').getByText('验证步骤 25：工具返回了预期结果。',{exact:true}).waitFor();
  await page.locator('.task-history').evaluate(node=>{node.scrollTop=0;node.dispatchEvent(new Event('scroll'));});
  add('text_delta',{text:' 新进展。'});await push();await page.locator('.task-transcript').getByText('验证步骤 25：工具返回了预期结果。 新进展。',{exact:true}).waitFor();
  assert(await page.locator('.task-history').evaluate(node=>node.scrollTop)===0,'Reading history pauses following');
  await page.locator('.task-composer-area .latest-button').click();assert(await page.locator('.task-history').evaluate(node=>node.scrollTop>0),'Latest action resumes following');
  const inputRect=await composer.boundingBox();assert(inputRect.y+inputRect.height<=1000,'Composer stays within viewport during long output');
  // Task guidance uses the same mobile focus/keyboard behavior as main Chat.
  const retainedDraft=await composer.inputValue();await page.setViewportSize({width:390,height:852});
  await composer.fill('Long mobile task guidance. '.repeat(50));await composer.evaluate(node=>node.blur());
  await page.locator('.task-history').evaluate(node=>{node.scrollTop=120;node.dispatchEvent(new Event('scroll'));});
  const focusCDP=await page.context().newCDPSession(page);await focusCDP.send('Emulation.setTouchEmulationEnabled',{enabled:true});
  const touchRect=await composer.boundingBox();await focusCDP.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:touchRect.x+touchRect.width/2,y:touchRect.y+touchRect.height/2}]});await focusCDP.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
  const taskFocus=await composer.evaluate(node=>{node.setSelectionRange(30,40);node.scrollTop=40;return {history:document.querySelector('.task-history').scrollTop,inputScroll:node.scrollTop};});
  for(const [height,top]of [[420,80],[420,110],[852,0]]){
    await page.evaluate(({height,top})=>{Object.defineProperties(window.visualViewport,{height:{configurable:true,value:height},offsetTop:{configurable:true,value:top}});window.visualViewport.dispatchEvent(new Event('resize'));window.visualViewport.dispatchEvent(new Event('scroll'));},{height,top});
    const bounds=await composer.boundingBox();
    assert(bounds.y>top+44&&bounds.y+bounds.height<=top+height,'Task composer stays above the keyboard after a tap');
    assert(await composer.evaluate(node=>document.activeElement===node&&node.selectionStart===30&&node.selectionEnd===40),'Task keyboard transitions retain native focus and selection');
    assert(await composer.evaluate(node=>node.scrollTop)===taskFocus.inputScroll,'Task keyboard panning retains long-draft scrolling');
    assert(await page.locator('.task-history').evaluate(node=>node.scrollTop)===taskFocus.history,'Task keyboard transitions retain history reading position');
  }
  await page.evaluate(()=>{for(const key of ['height','offsetTop'])delete window.visualViewport[key];window.visualViewport.dispatchEvent(new Event('resize'));});
  await focusCDP.send('Emulation.setTouchEmulationEnabled',{enabled:false});await focusCDP.detach();
  await composer.fill(retainedDraft);await composer.evaluate(node=>node.blur());await page.setViewportSize({width:1200,height:1000});
  inputs.at(-1).state='consumed';add('message_finished');add('input_applied',{id:failedRequest});add('message_started');add('text_delta',{text:'安装与工具调用验证完成。'});add('message_finished');add('turn_finished',{outcome:{kind:'success'}});phase='complete';result={text:'安装与工具调用验证完成。',outcome:{kind:'success'}};await push();
  await page.locator('.task-status').getByText('已完成',{exact:true}).waitFor();
  assert(await page.locator('.task-transcript').getByText(result.text,{exact:true}).count()===1,'Final result appears once as a reply');
  assert(await composer.inputValue()==='完成时也保留这条草稿 /skill:r'&&await composer.evaluate(node=>node.readOnly),'Completion retains unsent draft read-only');
  assert(await page.getByRole('button',{name:'发送指导',exact:true}).count()===0,'Ended Tasks cannot receive guidance');await composer.click();await composer.evaluate(node=>node.dispatchEvent(new Event('select',{bubbles:true})));assert(await page.locator('.task-composer-area .completion-list').isHidden(),'Completed drafts cannot be changed through reference completion');
  await page.locator('.task-history').evaluate(node=>{node.scrollTop=0;node.dispatchEvent(new Event('scroll'));});
  await page.getByRole('button',{name:'返回',exact:true}).first().click();await page.getByRole('button',{name:'安装 GitHub CLI 并验证可用',exact:true}).click();await page.locator('.task-status').getByText('已完成',{exact:true}).waitFor();
  await page.waitForFunction(()=>document.querySelector('.task-goal')?.open&&document.querySelector('[data-call-id="probe"]')?.open);
  assert(await composer.inputValue()==='完成时也保留这条草稿 /skill:r','Navigation retains Task draft');
  assert(await page.locator('.task-goal').evaluate(node=>node.open)&&await tool.evaluate(node=>node.open),'Navigation restores disclosures');
  assert(await page.locator('.task-history').evaluate(node=>node.scrollTop)===0,'Navigation restores reading position');
  offline=true;await push();await page.locator('.task-status').getByText('原执行机器离线 · 显示缓存执行输出',{exact:true}).waitFor();
  assert(await page.locator('.task-transcript').getByText(result.text,{exact:true}).count()===1,'Offline refresh retains result');
  offline=false;failEvents=true;await push();await page.locator('.task-status').getByText('连接中断 · 显示已有记录',{exact:true}).waitFor();failEvents=false;await page.getByRole('button',{name:'重试',exact:true}).click();await page.locator('.task-status').getByText('已完成',{exact:true}).waitFor();
  // Fresh fixture runs cover terminal failure, Stop and cancellation controls.
  phase='failed';result=null;errorText='Task fixture failed';inputs=[inputs[0]];events=[event(1,'message_started'),event(2,'text_delta',{text:'保留的部分输出。'}),event(3,'turn_finished',{outcome:{kind:'failed',error:errorText}})];await page.reload();await openTask();await page.locator('.task-status').getByText('失败',{exact:true}).waitFor();await page.locator('.task-transcript .reply-bubble.is-failed').getByText('保留的部分输出。',{exact:true}).waitFor();assert(await page.locator('.task-transcript .reply-bubble').count()===1,'Failure and partial output share one bubble');
  phase='running';errorText='';events=[event(1,'message_started'),event(2,'text_delta',{text:'等待后续操作。'})];await page.reload();await openTask();await page.getByRole('button',{name:'停止任务',exact:true}).click();await page.locator('.task-status').getByText('已中断',{exact:true}).waitFor();assert(posts.at(-1).path.endsWith('/interrupt'),'Empty composer Stop interrupts its Task');
  await menu().click();await page.getByRole('button',{name:'取消任务',exact:true}).click();await page.getByRole('button',{name:'保留任务',exact:true}).waitFor();await page.getByRole('button',{name:'取消任务',exact:true}).click();await page.locator('.task-status').getByText('已取消',{exact:true}).waitFor();assert(posts.at(-1).path.endsWith('/cancel'),'Menu cancellation uses the Task cancellation endpoint');
  assert(reads.some(read=>read.after==='4')&&reads.some(read=>Number(read.after)>4),'Event reads advance the cursor');
  assert(reads.every(read=>read.owner==='alpha'&&read.space==='local'),'Task reads retain owner and Space');
  assert(errors.length===0,'No browser exceptions: '+errors.join('; '));
  return {passed:true,checks:'conversation layout, scoped approvals and provenance, input bookmarks, menu controls, fixed composer, IME, retry identity, scroll following, terminal deduplication, draft/disclosure/navigation retention, mobile layout and cached offline output'};
 }finally{await page.unroute('**/api/v1/**',routeHandler);await page.unroute('**/access/status',access);page.off('pageerror',onError);}
}
