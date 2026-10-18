// Disposable production Chat and Task-list fixture; API replies and time are local.
async page=>{
 const state=STATE,context=await page.context().browser().newContext({serviceWorkers:'block'});page=await context.newPage();
 const assert=(value,message)=>{if(!value)throw Error(message);},errors=[],reads=[];
 page.on('pageerror',error=>errors.push(error.message));
 const now=Date.UTC(2026,9,18,6),started=now-754000;
 let revision=1,tasks=[{id:'attachments',assistant_id:'alpha',title:'独立 worktree 实现聊天图片和文档附件',phase:'running',created_at_ms:started-600000,started_at_ms:started},{id:'documents',assistant_id:'alpha',title:'整理并导出项目文档',phase:'running',started_at_ms:now-1680000}];
 await page.addInitScript(now=>{
  window.__taskTimeNow=now;Date.now=()=>window.__taskTimeNow;
  const sources=new Set();window.EventSource=class extends EventTarget{constructor(url){super();this.url=String(url);sources.add(this);queueMicrotask(()=>this.onopen?.({}));}close(){sources.delete(this);}};
  window.__taskTimeChanges=version=>{for(const source of sources)if(source.url.includes('/changes'))source.dispatchEvent(new MessageEvent('changed',{data:JSON.stringify({tasks:version})}));};
 },now);
 await page.route('**/access/status',route=>route.fulfill({json:{authenticated:true}}));
 await page.route('**/api/v1/**',async route=>{
  const url=new URL(route.request().url()),path=url.pathname,owner=url.searchParams.get('assistant_id');reads.push(path);
  const task=tasks.find(task=>path==='/api/v1/tasks/'+task.id);
  const data=path==='/api/v1/runtime'?{runtime_id:'fixture',boot_id:'boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Local',connected:true}]}:path==='/api/v1/assistants'?[{id:'alpha',name:'Pot',revision:1,reply_count:0},{id:'beta',name:'其他助手',revision:1,reply_count:0}]:path==='/api/v1/snapshot'?{api_version:3,cursor:revision++,boot_id:'boot',conversation_id:owner,messages:owner==='alpha'?[{id:'u',role:'user',text:'请处理图片附件和项目文档。',status:'complete'},{id:'a',role:'assistant',reply_to:'u',text:'任务正在进行，有结果我会告诉你。',status:'complete'}]:[],turn:null,agent:{phase:'ready'}}:path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:path==='/api/v1/tasks/outcomes'?{items:[],cursor:0,has_more:false}:path==='/api/v1/tasks/query'?{items:owner==='alpha'?tasks:[],total:owner==='alpha'?tasks.length:0,counts:{active:owner==='alpha'?tasks.filter(task=>['queued','running','waiting','waiting_approval'].includes(task.phase)).length:0,attention:0,archived:0}}:task?{run_id:'run',info:task,request:{text:task.title}}:path.endsWith('/events')?{events:[],cursor:0,has_more:false}:path.endsWith('/messages')?[]:{items:[]};
  await route.fulfill({json:data});
 });
 const change=async()=>{const response=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/v1/tasks/query');await page.evaluate(version=>window.__taskTimeChanges(version),++revision);await response;};
 const elapsed=()=>page.locator('.background-task').filter({hasText:'独立 worktree'}).locator('.background-task-elapsed');
 try{
  await page.emulateMedia({reducedMotion:'reduce'});await page.setViewportSize({width:1200,height:850});await page.goto(state.origin+'/?locale=zh-CN&theme=light&conversation=alpha');
  await page.locator('.background-task').first().waitFor();await page.locator('#pot-composer').fill('保留这条草稿');
  if(!state.before){
   await elapsed().getByText('已用 12分34秒',{exact:true}).waitFor();
   await page.locator('.background-task').filter({hasText:'独立 worktree'}).focus();
   await page.evaluate(()=>window.__retainedTimeRow=document.activeElement);await change();
   await page.waitForFunction(()=>document.activeElement===window.__retainedTimeRow&&document.activeElement.isConnected);
   const count=reads.length;
   await page.evaluate(()=>window.__taskTimeNow+=5000);
   await elapsed().getByText('已用 12分39秒',{exact:true}).waitFor();
   assert(reads.length===count,'Clock updates do not fetch backend projections');
   assert(await page.evaluate(()=>document.activeElement===window.__retainedTimeRow),'Clock updates preserve focused task');
   await page.evaluate(()=>window.__taskTimeNow-=5000);
   await elapsed().getByText('已用 12分34秒',{exact:true}).waitFor();
  }
  await page.locator('#pot-composer').focus();
  for(const [name,width]of [['desktop',1200],['mobile',390]]){
   await page.setViewportSize({width,height:850});await page.locator('.background-tasks').scrollIntoViewIfNeeded();
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Tasks fit '+name);
   if(!state.before){const bounds=await elapsed().boundingBox(),title=await page.locator('.background-task').filter({hasText:'独立 worktree'}).locator('.background-task-title').boundingBox();assert(width<720?bounds.y>=title.y+title.height:bounds.x>=title.x+title.width,'Elapsed time follows responsive layout');}
   if(state.screenshots)await page.screenshot({path:state.screenshots+'/chat-task-time-'+name+(state.before?'-before.png':'-after.png')});
  }
  await page.setViewportSize({width:1200,height:850});await page.getByRole('button',{name:'任务',exact:true}).first().click();await page.locator('.task-row').first().waitFor();
  const listRow=()=>page.locator('.task-row').filter({hasText:'独立 worktree'}),listTime=()=>listRow().locator('.task-list-elapsed');
  if(!state.before)await listTime().getByText('已用 12分34秒',{exact:true}).waitFor();
  for(const [name,width]of [['desktop',1200],['mobile',390]]){
   await page.setViewportSize({width,height:850});
   await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Task list fits '+name);
   if(!state.before){const time=await listTime().boundingBox(),title=await listRow().locator('.task-title-line').boundingBox();assert(width<720?time.y>=title.y+title.height:time.x>=title.x+title.width,'Task-list elapsed time follows responsive layout');}
   if(state.screenshots)await page.screenshot({path:state.screenshots+'/task-list-time-'+name+(state.before?'-before.png':'-after.png')});
  }
  if(state.before)return {passed:true,before:true,errors};
  await page.setViewportSize({width:1200,height:850});
  const search=page.locator('.task-search input');await search.fill('未提交的搜索');await search.focus();
  await listRow().evaluate(node=>window.__retainedListTimeRow=node);const listReads=reads.length;
  await page.evaluate(()=>window.__taskTimeNow+=5000);await listTime().getByText('已用 12分39秒',{exact:true}).waitFor();
  assert(reads.length===listReads,'Task-list clock does not fetch backend projections');
  assert(await search.inputValue()==='未提交的搜索'&&await search.evaluate(node=>document.activeElement===node),'Task-list clock preserves search draft and focus');
  tasks[0].phase='waiting_approval';await change();await listRow().locator('.task-phase').getByText('等待授权',{exact:true}).waitFor();
  assert(await listRow().evaluate(node=>node===window.__retainedListTimeRow),'Task invalidation preserves the task row');
  await listTime().getByText('已用 12分39秒',{exact:true}).waitFor();
  tasks[0].phase='complete';await change();await listTime().waitFor({state:'hidden'});
  tasks[0].phase='running';tasks[0].started_at_ms=0;await change();await listRow().locator('.task-phase').getByText('运行中',{exact:true}).waitFor();assert(await listTime().isHidden(),'Unknown starts omit the Task-list clock');
  tasks[0].started_at_ms=started;await page.evaluate(()=>window.__taskTimeNow-=5000);await change();await listTime().getByText('已用 12分34秒',{exact:true}).waitFor();
  await page.getByRole('button',{name:'聊天',exact:true}).first().click();await elapsed().getByText('已用 12分34秒',{exact:true}).waitFor();
  await page.setViewportSize({width:1200,height:850});
  await page.locator('.background-task').filter({hasText:'独立 worktree'}).click();await page.getByLabel('任务指导',{exact:true}).waitFor();
  await page.getByRole('button',{name:'任务操作',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'任务详情',exact:true}).click();
  await page.getByRole('dialog').getByText('开始时间',{exact:true}).waitFor();
  await page.getByRole('dialog').getByRole('button',{name:'关闭',exact:true}).click();await page.getByRole('button',{name:'返回',exact:true}).click();
  assert(await page.locator('#pot-composer').inputValue()==='保留这条草稿','Task navigation retains Chat draft');
  await page.reload();await elapsed().getByText('已用 12分34秒',{exact:true}).waitFor();
  tasks[0].started_at_ms=0;await change();await elapsed().waitFor({state:'detached'});
  tasks[0].started_at_ms=now-5040000;await change();await elapsed().getByText('已用 1小时24分',{exact:true}).waitFor();
  await page.locator('.assistant-row').filter({hasText:'其他助手'}).click();await page.locator('.background-tasks').waitFor({state:'detached'});
  await page.locator('.assistant-row').filter({hasText:'Pot'}).click();await elapsed().getByText('已用 1小时24分',{exact:true}).waitFor();
  tasks=[];await change();await page.locator('.background-tasks').waitFor({state:'detached'});
  assert(errors.length===0,errors.join('; '));
  return {passed:true,task_list:true,clock_without_requests:true,responsive:true,reload:true,owner_scoping:true,navigation:true,errors};
 }finally{await context.close();}
}
