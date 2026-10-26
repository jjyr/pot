// Shared product UI with explicit operation descriptions. No model calls.
async page => {
 const assert=(condition,message)=>{if(!condition)throw Error(message);};
 const calls=[],errors=[],onError=error=>errors.push(error.message);page.on('pageerror',onError);
 const assistantCaps={version:1,scope:'owner',operations:['chat.read','chat.send','chat.subscribe','task.read','task.list','task.outcomes']};
 let taskOps=['task.read','task.input','task.events','task.control','task.cancel','task.archive'],paused=false,version=0;
 const info={id:'capability-task',assistant_id:'alpha',title:'Capability-based task',phase:'running',agent_link:{link_id:'peer',node_id:'remote',assistant_id:'worker',kind:'pot',name:'Remote worker'}};
 const frame={kind:'reset',base_cursor:0,view:{state:{cursor:1,boot_id:'boot',conversation_id:'alpha',agent:{phase:'ready',model:''},turn:null,messages:[]},messages:[],history:{has_more:false,has_newer:false}}};
 await page.addInitScript(()=>{const sources=new Set();window.EventSource=class extends EventTarget{constructor(url){super();this.url=String(url);sources.add(this);queueMicrotask(async()=>{this.onopen?.({});if(this.url.includes('/events')){const value=await(await fetch('/api/v1/snapshot')).json();this.dispatchEvent(new MessageEvent('conversation',{data:JSON.stringify(value)}));}});}close(){sources.delete(this);}};window.capabilityRefresh=version=>{for(const source of sources)if(source.url.includes('/changes'))source.dispatchEvent(new MessageEvent('changed',{data:JSON.stringify({tasks:version})}));};localStorage.setItem('pot-system-notifications-browser-v1','false');});
 const access=route=>route.fulfill({json:{authenticated:true}});
 const handler=async route=>{
  const request=route.request(),url=new URL(request.url()),path=url.pathname;
  calls.push({path,method:request.method(),body:request.method()==='POST'?request.postDataJSON():null});
  let value={items:[]};
  if(path==='/api/v1/runtime')value={runtime_id:'fixture',boot_id:'boot'};
  if(path==='/api/v1/spaces')value={active_space_id:'local',items:[{id:'local',kind:'local',name:'Local',connected:true}]};
  if(path==='/api/v1/assistants')value=[{id:'alpha',name:'Local-looking assistant',capabilities:assistantCaps}];
  if(path==='/api/v1/snapshot')value=frame;
  if(path==='/api/v1/tasks/outcomes')value={items:[],cursor:0,has_more:false};
  if(path==='/api/v1/tasks/catalog'||path==='/api/v1/tasks/query')value={items:[info],total:1,counts:{active:1}};
  if(path==='/api/v1/tasks/capability-task')value={run_id:'original-run',request:{text:'Use advertised task operations.'},info:{...info,paused},capabilities:{version:1,scope:'caller',operations:taskOps}};
  if(path==='/api/v1/tasks/capability-task/events')value={events:[],cursor:0,has_more:false};
  if(path==='/api/v1/tasks/capability-task/messages')value=[];
  if(path==='/api/v1/tasks/capability-task/control'){paused=true;value={accepted:true};}
  await route.fulfill({json:value});
 };
 await page.route('**/access/status',access);await page.route('**/api/v1/**',handler);
 try{
  await page.setViewportSize({width:1200,height:900});await page.goto('http://127.0.0.1:17702/?locale=en&theme=light');
  await page.locator('.assistant-row').filter({hasText:'Local-looking assistant'}).waitFor();
  assert(await page.getByRole('button',{name:'Assistant settings',exact:true}).count()===0,'Local placement does not grant configuration capability');
  await page.getByRole('button',{name:'Tasks',exact:true}).first().click();await page.getByRole('button',{name:info.title,exact:true}).click();
  await page.locator('.task-conversation .composer-input').waitFor({state:'visible'});
  assert(!await page.locator('.task-conversation .composer-input').evaluate(node=>node.readOnly),'Linked task exposes advertised input capability');
  await page.getByRole('button',{name:'Task actions',exact:true}).click();await page.getByRole('button',{name:'Pause task',exact:true}).click();
  assert(calls.some(call=>call.path.endsWith('/control')&&call.body.run_id==='original-run'),'Linked task pause uses the original run and shared control API');
  await page.screenshot({path:'output/playwright/task-lifecycle/capabilities-desktop-after.png'});
  await page.setViewportSize({width:390,height:844});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.screenshot({path:'output/playwright/task-lifecycle/capabilities-mobile-after.png'});await page.setViewportSize({width:1200,height:900});
  taskOps=['task.read','task.cancel','task.archive'];await page.evaluate(value=>window.capabilityRefresh(value),++version);
  await page.locator('.task-conversation form.composer').waitFor({state:'hidden'});
  await page.getByRole('button',{name:'Task actions',exact:true}).click();assert(await page.getByRole('button',{name:'Resume task',exact:true}).count()===0,'Removing capability removes its control without changing location');
  assert(await page.getByRole('button',{name:'Cancel task',exact:true}).count()===1,'Independent cancel capability remains available');
  await page.locator('.task-actions').getByRole('button',{name:'Close',exact:true}).click();
  const mark=calls.length;await page.evaluate(value=>window.capabilityRefresh(value),++version);await page.waitForFunction(()=>!document.querySelector('.task-conversation form.composer')?.offsetParent);
  await page.waitForTimeout(80);
  assert(!calls.slice(mark).some(call=>call.path.endsWith('/events')||call.path.endsWith('/messages')),'Unsupported task transcript and input endpoints are not queried');
  assert(errors.length===0,errors.join('\n'));return {passed:true,local_privilege_removed:true,remote_task_controls:true,dynamic_capabilities:true};
 }catch(error){throw Error(error.message+'; DOM: '+(await page.locator('body').innerText()).slice(0,2000)+'; errors: '+errors.join('; ')+'; reads: '+calls.map(call=>call.path).join(','));
 }finally{await page.unroute('**/api/v1/**',handler);await page.unroute('**/access/status',access);page.off('pageerror',onError);}
}
