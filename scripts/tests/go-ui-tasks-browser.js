// Run with Playwright CLI against a disposable preview on port 17702.
// HTTP/SSE responses are fixtures; no model or product data is used.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 const before=false,errors=[],reads=[],posts=[];
 const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 const goal='安装 GitHub CLI 并验证可用。先检查操作系统与当前安装状态，再报告安装结果。';
 let phase='running',paused=false,result=null,version=1,offline=false;
 let inputs=[{request_id:'initial',kind:'input',state:'active',text:goal}];
 const event=(sequence,type,value={})=>({sequence,event:{turn_id:'run-one',update:{type,...value}}});
 let events=[event(1,'message_started'),event(2,'text_delta',{text:'正在检查当前安装状态。'}),event(3,'message_finished'),event(4,'tool_started',{id:'probe',name:'exec_command',args:{cmd:'gh --version'}})];
 await page.addInitScript(()=>{const sources=new Set();window.EventSource=class extends EventTarget{constructor(url){super();this.url=String(url);sources.add(this);queueMicrotask(()=>this.onopen?.({}));}close(){sources.delete(this);}};window.__taskChanges=value=>{for(const source of sources)if(source.url.includes('/changes'))source.dispatchEvent(new MessageEvent('changed',{data:JSON.stringify({tasks:value})}));};});
 const routeHandler=async route=>{
  const url=new URL(route.request().url()),path=url.pathname;
  if(path.startsWith('/api/v1/tasks/task-proof'))reads.push({path,after:url.searchParams.get('after'),owner:url.searchParams.get('assistant_id'),space:url.searchParams.get('space_id')});
  if(route.request().method()==='POST'){const body=route.request().postDataJSON();posts.push({path,body});if(path.endsWith('/control'))paused=body.action==='pause';}
  const data=path==='/api/v1/runtime'?{runtime_id:'fixture',boot_id:'boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Local',kind:'local',connected:true}]}:path==='/api/v1/assistants'?[{id:'alpha',name:'Pot',revision:1}]:path==='/api/v1/assistants/bootstrap'?{assistant:null}:path==='/api/v1/snapshot'?{cursor:1,boot_id:'boot',conversation_id:'alpha',messages:[],turn:null,agent:{phase:'ready'}}:path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:path==='/api/v1/tasks/query'?{items:[{id:'task-proof',title:'安装 GitHub CLI 并验证可用',assistant_id:'alpha',machine_id:'fixture-runtime',phase}],total:1}:path==='/api/v1/tasks/task-proof'?{run_id:'run-one',info:{phase,paused,machine_id:'fixture-runtime'},request:{text:goal},result}:path==='/api/v1/tasks/task-proof/messages'?inputs:path==='/api/v1/tasks/task-proof/events'?{events:events.filter(row=>row.sequence>Number(url.searchParams.get('after'))),cursor:events.at(-1)?.sequence||0,has_more:false,unavailable:offline}:{items:[]};
  await route.fulfill({json:data});
 };
 const access=route=>route.fulfill({json:{authenticated:true,push_available:false}});
 await page.route('**/access/status',access);await page.route('**/api/v1/**',routeHandler);
 const push=()=>page.evaluate(value=>window.__taskChanges(value),++version);
 try{
  await page.setViewportSize({width:1200,height:1000});await page.goto('http://127.0.0.1:17702/?theme=light&locale=zh-CN&conversation=alpha');
  await page.getByRole('button',{name:'任务',exact:true}).first().click();await page.getByRole('button',{name:'安装 GitHub CLI 并验证可用',exact:true}).click();
  await page.locator('.task-transcript').getByText('正在检查当前安装状态。',{exact:true}).waitFor();
  await page.getByRole('button',{name:'发送指导',exact:true}).waitFor();
  await page.screenshot({path:`output/pr-images/task-progress/task-desktop-${before?'before':'after'}.png`});
  await page.setViewportSize({width:390,height:1000});await page.screenshot({path:`output/pr-images/task-progress/task-mobile-${before?'before':'after'}.png`});
  if(before)return {capture:'before'};
  assert(await page.getByText(goal,{exact:true}).count()===1,'Original input appears once');
  assert(await page.locator('.task-input').count()===0,'Original input is not repeated below output');
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Narrow task detail fits viewport');
  await page.setViewportSize({width:1200,height:1000});
  const composer=page.getByRole('textbox',{name:'消息',exact:true}),tool=page.locator('.tool-call[data-call-id="probe"]');
  await composer.fill('保留这条尚未发送的指导');await tool.locator(':scope > summary').click();
  await composer.evaluate(node=>{window.__taskComposer=node;});
  inputs.push({request_id:'guide',kind:'guidance',state:'pending',text:'请继续验证工具能否找到 gh。'});
  events.push(event(5,'tool_output',{id:'probe',stream:'stdout',text:'gh version fixture'}),event(6,'tool_finished',{id:'probe',result:{exit_code:0}}),event(7,'message_started'),event(8,'text_delta',{text:'已找到 GitHub CLI，正在验证工具调用。'}));await push();
  await page.locator('.task-transcript').getByText('已找到 GitHub CLI，正在验证工具调用。',{exact:true}).waitFor();await page.getByText('指导 · 待处理',{exact:true}).waitFor();
  assert(await composer.inputValue()==='保留这条尚未发送的指导','Invalidation retains draft');
  assert(await composer.evaluate(node=>node===window.__taskComposer),'Invalidation retains editor node');
  assert(await tool.evaluate(node=>node.open),'Invalidation retains expanded tool');
  assert(await page.locator('.task-event').count()===3,'Incremental updates do not duplicate transcript');
  assert(await page.locator('.task-inputs').evaluate(node=>!!(node.compareDocumentPosition(document.querySelector('.task-transcript'))&Node.DOCUMENT_POSITION_FOLLOWING)),'Guidance appears before execution');
  await page.getByRole('button',{name:'暂停任务',exact:true}).click();await page.getByRole('button',{name:'恢复任务',exact:true}).waitFor();
  assert(posts.at(-1).body.run_id==='run-one'&&posts.at(-1).body.action==='pause','Pause uses original run');
  inputs[1].state='consumed';paused=false;phase='complete';result={text:'安装与工具调用验证完成。',outcome:{kind:'success'}};
  events.push(event(9,'text_delta',{text:'验证已通过。'}),event(10,'message_finished'),event(11,'turn_finished',{outcome:{kind:'success'}}));await push();
  await page.locator('.task-result').getByText(result.text,{exact:true}).waitFor();await page.getByText('指导 · 已送达',{exact:true}).waitFor();
  assert((await page.locator('.task-status').textContent()).includes('complete'),'Completion refreshes status');
  assert(await page.getByRole('button',{name:'暂停任务',exact:true}).count()===0&&await page.getByRole('button',{name:'恢复任务',exact:true}).count()===0,'Completion removes active controls');
  assert(await composer.inputValue()==='保留这条尚未发送的指导','Completion retains draft');
  assert(await page.locator('.task-result').textContent().then(text=>!text.includes('outcome')),'Terminal result shows prose');
  assert(reads.some(read=>read.after==='4')&&reads.some(read=>read.after==='8'),'Event reads advance the cursor');
  assert(reads.every(read=>read.owner==='alpha'&&read.space==='local'),'Refresh retains Task owner and Space');
  offline=true;await push();await page.getByText('原执行机器离线，显示缓存执行输出。',{exact:true}).waitFor();
  assert(await page.locator('.task-result').getByText(result.text,{exact:true}).count()===1,'Offline refresh retains result');
  assert(errors.length===0,'No browser exceptions: '+errors.join('; '));
  return {checks:'original input deduplication, streamed output, delivery state, status/result refresh, pause identity, cursor/owner scope, draft and disclosure preservation, narrow layout, cached offline output'};
 }finally{await page.unroute('**/api/v1/**',routeHandler);await page.unroute('**/access/status',access);page.off('pageerror',onError);}
}
