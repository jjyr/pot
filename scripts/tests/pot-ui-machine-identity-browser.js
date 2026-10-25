// Disposable UI contract: production components, synthetic machine identities.
async page=>{
 const state=STATE,assert=(value,message)=>{if(!value)throw Error(message);};
 await page.route('**/machine-identity-fixture',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><body class="pot-app" style="display:flex;flex-direction:column"><main class="page-host" style="width:100%;height:100dvh;overflow:auto"></main></body></html>'}));
 await page.goto(state.origin+'/machine-identity-fixture');
 await page.evaluate(async()=>{
  const {createPages}=await import('/components/pages.mjs');
  const {createToolCall}=await import('/components/tool-call.mjs');
  const {machinePresentation}=await import('/state/machine-identity.mjs');
  const f=window.identityFixture={locale:'en',writes:[]};
  const capabilities={version:1,scope:'owner',operations:['task.read','task.list','task.events','task.input','schedules.manage','approvals.manage']};
  const task={id:'task-one',title:'Machine identity task',assistant_id:'assistant',machine_id:'opaque-machine',phase:'running'};
  f.client={runtime:{boot_id:'fixture'},spaces:[{id:'local',control_id:'opaque-node',name:'Local',kind:'local'}],assistants:[{id:'assistant',space_id:'local',name:'Assistant',capabilities}],scope:()=>({space:'local',owner:'assistant'}),request:async(path,options={})=>{
   if(path==='/api/v1/runtime')return {runtime_id:'opaque-machine',host:{hostname:'dev-box.local',ip_addresses:['192.168.1.24']}};
   if(path==='/api/v1/permissions')return {pending:[]};
   if(path.includes('/tasks/catalog'))return {items:[task],counts:{active:1},total:1};
   if(path==='/api/v1/tasks/task-one')return {capabilities,info:task,request:{text:'Synthetic task goal'}};
   if(path.includes('/events?'))return {events:[],cursor:0};
   if(path.endsWith('/messages'))return [];
   if(path==='/api/v1/schedules/preview')return {timezone:'UTC',times:[Date.now()+60000]};
   if(path.startsWith('/api/v1/schedules')){if(options.method==='POST')f.writes.push(options.body);return {items:[]};}
   return {items:[]};
  }};
  f.root=document.querySelector('main');f.root.replaceChildren();
  f.pages=createPages({client:f.client,host:f.root,approvalHost:document.createElement('div'),chatApprovalHost:document.createElement('div'),chatVisible:()=>false,locale:()=>f.locale,notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});
  await f.pages.show('tasks');
  const machineLabel=id=>machinePresentation(f.client).label(id,{space:'local'},f.locale);
  f.tool=createToolCall({locale:()=>f.locale,machineLabel});f.root.append(f.tool.element);
  f.message={tool:{id:'call',turn_id:'turn',name:'exec_command',args:{cmd:'pwd'},target:{runtime_id:'opaque-machine'},phase:'running'}};
  f.approval={id:'approval',request:{machine_id:'opaque-machine',tool:'exec_command',args:{cmd:'pwd'}}};
  f.update=()=>f.tool.update(f.message,{approval:f.approval});f.update();
 });
 assert((await page.locator('.task-row .task-meta').innerText()).includes('dev-box.local · 192.168.1.24'),'Task list uses host and IP');
 assert((await page.locator('.tool-context').innerText()).includes('dev-box.local · 192.168.1.24'),'Tool summary uses host and IP');
 assert(await page.locator('.approval-machine').textContent()==='dev-box.local · 192.168.1.24','Approval summary uses host and IP');
 assert(!(await page.locator('.tool-call').innerText()).includes('opaque-machine'),'Tool and approval metadata hide internal machine IDs');
 await page.evaluate(()=>{identityFixture.locale='zh-CN';identityFixture.update();});
 assert(await page.locator('.approval-state').textContent()==='等待授权','Approval text follows the current locale');
 await page.evaluate(()=>{identityFixture.locale='en';identityFixture.tool.element.remove();});
 await page.getByRole('button',{name:'Machine identity task',exact:true}).click();
 await page.locator('.task-conversation').waitFor();
 await page.evaluate(()=>identityFixture.pages.taskMenu());
 await page.getByRole('dialog').getByRole('button',{name:'Task details',exact:true}).click();
 assert((await page.locator('.task-info').innerText()).includes('dev-box.local · 192.168.1.24'),'Task details resolve the original machine');
 await page.evaluate(async()=>{document.querySelector('dialog[open]').close();await identityFixture.pages.show('tasks',{force:true});});
 await page.getByRole('button',{name:'Schedules',exact:true}).click();
 await page.getByRole('button',{name:'New schedule',exact:true}).click();
 const machine=page.getByLabel('Execution machine',{exact:true});
 await page.waitForFunction(()=>document.querySelector('select[aria-label="Execution machine"]')?.options.length===2);
 assert((await machine.locator('option').allTextContents()).includes('dev-box.local · 192.168.1.24'),'Schedule choices show host and IP');
 await machine.selectOption('opaque-machine');
 await page.getByLabel('Title',{exact:true}).fill('Machine identity schedule');
 await page.getByLabel('Goal',{exact:true}).fill('Synthetic fixture only');
 await page.evaluate(()=>{identityFixture.locale='zh-CN';window.dispatchEvent(new Event('pot:locale'));});
 await page.getByLabel('执行机器',{exact:true}).waitFor();
 assert(await page.getByLabel('执行机器',{exact:true}).inputValue()==='opaque-machine','Language switching preserves the selected routing ID');
 await page.getByRole('button',{name:'保存',exact:true}).click();
 await page.waitForFunction(()=>identityFixture.writes.length===1);
 assert(await page.evaluate(()=>identityFixture.writes[0].spec.machine_id)==='opaque-machine','Saving submits the stable machine ID');
 await page.setViewportSize({width:393,height:700});
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Machine labels fit the narrow viewport');
 await page.evaluate(()=>identityFixture.pages.dispose());
 await page.unroute('**/machine-identity-fixture');
 return {passed:true,taskList:true,taskDetails:true,toolContext:true,approvals:true,scheduleSelection:true,locale:true,routingIDPreserved:true};
}
