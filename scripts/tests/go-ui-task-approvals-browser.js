// Disposable production UI fixture; no model, commands or product data are used.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 const errors=[],posts=[],onError=error=>errors.push(error.message);page.on('pageerror',onError);
 let pending=[],rejectDecision=false,revision=1,changeRevision=0;
 const taskTitle=id=>id==='task-one'?'确认仓库访问权限':'读取发布文件';
 const phase=id=>pending.some(item=>item.request.task===id)?'waiting_approval':'running';
 const approval=(id,task,call='shared-call',turn='shared-turn')=>({id,request:{owner:'alpha',task,call_id:call,turn,machine_id:'dev-mac',tool:'exec_command',args:{cmd:'cat /Projects/shared/release.txt',justification:'Read the requested release file outside the workspace.'}}});
 const messages=[{id:'u',role:'user',text:'确认能否访问项目仓库。',status:'complete'},{id:'a',role:'assistant',reply_to:'u',text:'已启动后台任务，正在检查仓库访问权限。',status:'complete'},{id:'t',role:'tool',text:'',status:'complete',tool:{id:'shared-call',turn_id:'shared-turn',name:'get_task',args:{task_id:'task-one'},phase:'completed',result:{phase:'running'}}}];
 await page.addInitScript(()=>{const sources=new Set();window.EventSource=class extends EventTarget{constructor(url){super();this.url=String(url);sources.add(this);queueMicrotask(()=>this.onopen?.({}));}close(){sources.delete(this);}};window.__taskApprovalChanges=value=>{for(const source of sources)if(source.url.includes('/changes'))source.dispatchEvent(new MessageEvent('changed',{data:JSON.stringify(value)}));};});
 const access=route=>route.fulfill({json:{authenticated:true,push_available:false}});
 const handler=async route=>{
  const url=new URL(route.request().url()),path=url.pathname,owner=url.searchParams.get('assistant_id');
  if(route.request().method()==='POST'&&path==='/api/v1/permissions'){
   const body=route.request().postDataJSON();posts.push({body,owner,space:url.searchParams.get('space_id')});
   if(rejectDecision){await route.fulfill({status:503,json:{message:'Decision failed; try again'}});return;}
   pending=pending.filter(item=>item.id!==body.id);
  }
  const data=path==='/api/v1/runtime'?{runtime_id:'fixture',boot_id:'boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Local',connected:true}]}:path==='/api/v1/assistants'?[{id:'alpha',name:'Pot',revision:1},{id:'beta',name:'Other',revision:1}]:path==='/api/v1/snapshot'?{api_version:3,cursor:revision++,boot_id:'boot',conversation_id:owner,messages:owner==='alpha'?messages:[],turn:null,agent:{phase:'ready'}}:path==='/api/v1/permissions'?{pending:pending.filter(item=>item.request.owner===owner),policy:{machines:{}}}:path==='/api/v1/tasks/query'?{items:owner==='alpha'?[{id:'task-one',title:'确认仓库访问权限',assistant_id:'alpha',phase:phase('task-one')}]:[],total:1}:path==='/api/v1/tasks/task-one'?{run_id:'shared-turn',info:{phase:phase('task-one'),title:taskTitle('task-one')},request:{text:taskTitle('task-one')}}:path==='/api/v1/tasks/task-two'?{run_id:'shared-turn',info:{phase:phase('task-two'),title:taskTitle('task-two')},request:{text:taskTitle('task-two')}}:path==='/api/v1/tasks/task-one/events'?{events:[{sequence:1,event:{turn_id:'shared-turn',update:{type:'tool_started',id:'shared-call',name:'exec_command',args:approval('','').request.args}}},{sequence:2,event:{turn_id:'shared-turn',update:{type:'tool_state',id:'shared-call',phase:'waiting_approval'}}}],cursor:2,has_more:false}:path==='/api/v1/tasks/task-one/messages'?[]:path==='/api/v1/usage/summary'?{total:{},quota:{active:false,policy:{period:'none'}},buckets:[]}:{items:[]};
  await route.fulfill({json:data});
 };
 await page.route('**/access/status',access);await page.route('**/api/v1/**',handler);
 const change=async()=>{
  const response=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/v1/permissions'&&response.request().method()==='GET');
  await page.evaluate(version=>window.__taskApprovalChanges({permissions:version,tasks:version}),++changeRevision);
  await (await response).finished();await page.evaluate(()=>new Promise(requestAnimationFrame));
 };
 const dialog=()=>page.getByRole('dialog',{name:'任务需要你授权',exact:true});
 const card=()=>page.locator('.approval-card[data-task-id="task-one"]:visible');
 const back=()=>page.getByRole('button',{name:'返回',exact:true});
 try{
  await page.setViewportSize({width:1200,height:850});await page.goto(new URL('/?locale=zh-CN&theme=light',page.url()).href);
  await page.locator('.background-task').waitFor();
  await page.locator('#pot-composer').fill('保留这条草稿');
  pending=[approval('task-approval','task-one')];await change();
  await dialog().waitFor();await dialog().getByText('任务: 确认仓库访问权限',{exact:true}).waitFor();
  await dialog().getByText('任务需要授权',{exact:true}).waitFor();
  await page.screenshot({path:'output/pr-images/task-approval-desktop.png'});
  await card().getByText('任务需要授权',{exact:true}).waitFor();
  assert(await page.locator('.message-column .tool-approval:not([hidden])').count()===0,'Task approval must not attach to a Chat call with identical call and turn IDs');
  await page.locator('.background-task').getByText('等待授权',{exact:true}).waitFor();
  rejectDecision=true;await card().getByRole('button',{name:'本任务允许',exact:true}).click();
  await card().getByRole('alert').getByText('Decision failed; try again',{exact:true}).waitFor();
  assert(!await card().getByRole('button',{name:'拒绝',exact:true}).isDisabled(),'Failed decision retains usable controls inside the visible Chat card');
  await card().getByRole('button',{name:'拒绝',exact:true}).focus();
  await change();await card().getByRole('alert').getByText('Decision failed; try again',{exact:true}).waitFor();
  assert(await card().getByRole('button',{name:'拒绝',exact:true}).evaluate(node=>document.activeElement===node),'Permission refresh preserves focus in the approval card');
  rejectDecision=false;
  await dialog().getByRole('button',{name:'稍后处理',exact:true}).click();
  await page.locator('.assistant-row').filter({hasText:'Other'}).click();
  assert(!await dialog().isVisible(),'Assistant navigation closes the previous owner approval dialog');
  await page.locator('.assistant-row').filter({hasText:'Pot'}).click();await card().waitFor();
  await page.setViewportSize({width:390,height:850});
  await page.screenshot({path:'output/pr-images/task-approval-mobile.png'});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Task approval fits narrow Chat');
  await card().getByRole('button',{name:'本任务允许',exact:true}).click();
  await dialog().waitFor({state:'hidden'});
  assert(posts.some(post=>post.body.id==='task-approval'&&post.body.action==='allow_task'&&post.owner==='alpha'&&post.space==='local'),'Decision keeps original assistant, Space and exact approval');
  assert(!await page.locator('.background-task').textContent().then(text=>text.includes('等待授权')),'Resolved approval clears the waiting marker');
  pending=[approval('task-detail-approval','task-one'),approval('other-task-approval','task-two','another-call')];await change();await dialog().waitFor();
  await dialog().getByText('任务: 读取发布文件',{exact:true}).waitFor();
  assert((await dialog().locator('.approval-card').count())===2,'Chat dialog names every Task and permits direct review');
  await page.keyboard.press('Escape');await dialog().waitFor({state:'hidden'});
  await change();assert(!await dialog().isVisible(),'Unchanged permission events do not reopen a dismissed dialog');
  await page.locator('.task-approval-launcher').click();await dialog().waitFor();
  await page.screenshot({path:'output/pr-images/task-approval-popup-mobile-after.png'});
  await page.setViewportSize({width:1200,height:850});await page.screenshot({path:'output/pr-images/task-approval-popup-desktop-after.png'});
  await dialog().getByRole('button',{name:'稍后处理',exact:true}).click();
  await page.locator('.background-task').click();
  await page.locator('.task-transcript .tool-call').getByRole('button',{name:'拒绝',exact:true}).waitFor();
  await page.locator('.task-detail-status').getByText('等待授权',{exact:true}).waitFor();
  assert(!await dialog().isVisible(),'Task detail uses its own inline approval');
  await back().click();await page.locator('#pot-composer:visible').waitFor();
  assert(await page.locator('#pot-composer').inputValue()==='保留这条草稿','Task Back restores the originating Chat and draft');
  await dialog().waitFor();await page.keyboard.press('Escape');
  await page.locator('.background-task').click();await page.getByLabel('消息',{exact:true}).waitFor();
  await page.goBack();await page.locator('#pot-composer:visible').waitFor();
  await page.goForward();await page.getByLabel('消息',{exact:true}).waitFor();
  await page.locator('.page-content').getByRole('button',{name:'用量',exact:true}).click();
  await page.getByText('总用量',{exact:true}).waitFor();await back().click();await page.getByLabel('消息',{exact:true}).waitFor();
  await back().click();await page.locator('#pot-composer:visible').waitFor();
  await dialog().waitFor();await page.keyboard.press('Escape');
  await page.getByRole('button',{name:'任务',exact:true}).first().click();
  await page.locator('.task-row').getByText('等待授权',{exact:true}).waitFor();
  await page.getByRole('button',{name:'确认仓库访问权限',exact:true}).click();await page.getByLabel('消息',{exact:true}).waitFor();
  await back().click();await page.locator('.task-list').waitFor();
  await page.getByRole('button',{name:'确认仓库访问权限',exact:true}).click();
  await page.locator('.task-transcript .tool-call').getByRole('button',{name:'拒绝',exact:true}).click();
  assert(posts.some(post=>post.body.id==='task-detail-approval'&&post.body.action==='deny'&&post.owner==='alpha'&&post.space==='local'),'Task detail decision retains original scope');
  await page.getByRole('button',{name:'聊天',exact:true}).first().click();await dialog().waitFor();
  await dialog().getByRole('button',{name:'本任务允许',exact:true}).click();await dialog().waitFor({state:'hidden'});
  assert(posts.some(post=>post.body.id==='other-task-approval'&&post.body.action==='allow_task'&&post.owner==='alpha'&&post.space==='local'),'Chat directly authorizes a Task outside its background strip');
  assert(errors.length===0,'No browser exceptions: '+errors.join('; '));
  return {passed:true,checks:'Task approval modal, named tasks, direct decisions, dismissal and retry, status, owner fencing, Chat draft and Back/Forward origins',posts:posts.length};
 }finally{await page.unroute('**/api/v1/**',handler);await page.unroute('**/access/status',access);page.off('pageerror',onError);}
}
