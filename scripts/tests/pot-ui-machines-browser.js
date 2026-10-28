// Local execution policy uses production controls and synthetic IO. Remote
// execution belongs to the receiving Agent Link node and is never configured here.
async (page,{origin=new URL(page.url()).origin}={}) => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 await page.route('**/machine-policy-fixture',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><body></body></html>'}));
 await page.goto(origin+'/machine-policy-fixture');
 await page.evaluate(async()=>{
  const {createPages}=await import('/components/pages.mjs'),{node:n}=await import('/components/primitives.mjs');
  const fixture=window.machineFixture={policy:{machines:{host:{execution_access:'ask',revision:1}},rules:[],task_grants:[],application:{}},calls:[],notices:[],offline:false,hold:false,holdPolicy:false,language:'en'};
  const shell=n('div','pot-app surface-main'),host=n('main'),approvals=n('div');shell.style.display='block';host.className='page-host';host.style.height='100%';shell.append(host,approvals);document.body.append(shell);
  const client={desktop:false,runtime:{boot_id:'fixture-boot'},spaces:[{id:'local',control_id:'local-node',name:'Local Pot',connected:true}],scope:()=>({space:'local',owner:'selected-assistant'}),request:async(path,options)=>{
   fixture.calls.push({path,...options});
   if(path.startsWith('/api/v1/agent-links'))return {self:{node_id:'local-node',kind:'pot',status:'needs_access'},items:[]};
   if(path==='/api/v1/runtime')return {runtime_id:'host',host:{hostname:'Local machine'}};
   if(path==='/api/v1/permissions'){
    if(options.method==='POST'){
     if(fixture.hold)await new Promise(resolve=>fixture.release=resolve);
     const body=options.body;
     if(body.action==='configure'){if(fixture.policy.machines.host.revision!==body.machine_policy.revision)throw Error('Revision conflict');fixture.policy.machines.host={...body.machine_policy,revision:body.machine_policy.revision+1};}
     if(body.action==='save_rule'){fixture.policy.rules.push({...body.rule,id:'local-rule'});fixture.policy.machines.host.revision++;}
     return {};
    }
    if(fixture.holdPolicy)await new Promise(resolve=>fixture.releasePolicy=resolve);
    if(fixture.offline)throw Error('Policy unavailable');return {policy:structuredClone(fixture.policy)};
   }
   throw Error('Unmocked endpoint '+path);
  }};
  fixture.client=client;
  fixture.mount=()=>{fixture.pages?.dispose();host.replaceChildren();fixture.pages=createPages({chatApprovalHost:document.createElement('div'),chatVisible:()=>false,client,host,approvalHost:approvals,locale:()=>fixture.language,notify:text=>fixture.notices.push(text),changed:()=>{},titleChanged:()=>{},openChat:()=>{}});return fixture.pages.show('machines');};
  await fixture.mount();
 });
 const selectMachine=()=>page.locator('.topology-node.machine').click(),card=()=>page.locator('.topology-inspector .machine-card');
 await selectMachine();assert(await card().getByLabel('Default execution',{exact:true}).inputValue()==='ask','Local execution policy is retained');
 await page.evaluate(()=>{machineFixture.originalSelect=document.querySelector('.machine-card select');machineFixture.originalCard=document.querySelector('.topology-inspector .machine-card');});
 await card().getByLabel('Default execution',{exact:true}).selectOption('deny');await page.evaluate(()=>machineFixture.hold=true);await card().getByRole('button',{name:'Save permissions',exact:true}).click();
 await page.waitForFunction(()=>!!machineFixture.release);await page.evaluate(()=>machineFixture.pages.show('machines'));
 assert(await card().getByLabel('Default execution',{exact:true}).isDisabled(),'Pending saves remain disabled through refresh');
 assert(!await page.evaluate(()=>machineFixture.pages.leave()),'Navigation cannot abandon an in-flight permission write');
 assert(await page.evaluate(()=>machineFixture.originalSelect===document.querySelector('.machine-card select')&&machineFixture.originalCard===document.querySelector('.topology-inspector .machine-card')),'Pending save and refresh retain the original controls and card');
 await page.evaluate(()=>{machineFixture.hold=false;machineFixture.release();});await page.waitForFunction(()=>document.querySelector('.machine-card select')?.value==='deny'&&document.querySelector('.machine-card .permission-changes')?.hidden);
 assert(await page.evaluate(()=>machineFixture.calls.some(call=>call.method==='POST'&&call.space==='local'&&call.owner===''&&call.body.machine_id==='host'&&call.body.machine_policy.revision===1)),'Permission save captures the local machine and revision independently of Chat');
 assert(await page.evaluate(()=>machineFixture.originalSelect===document.querySelector('.machine-card select')),'Saved policy updates the original select');
 await page.evaluate(()=>{const f=machineFixture;f.read= f.client.read;f.metadataReads=[];f.client.read=async(path,options)=>{if(['/api/v1/runtime','/api/v1/agent-links'].includes(path))await new Promise(resolve=>f.metadataReads.push(resolve));return f.read(path,options);};f.holdPolicy=true;f.originalSelect.focus();f.postCount=f.calls.filter(call=>call.method==='POST').length;f.policyRefresh=f.pages.refresh(['permissions']);});
 await page.waitForFunction(()=>!!machineFixture.releasePolicy&&machineFixture.metadataReads.length===2);
 await page.waitForFunction(()=>document.querySelector('.machine-card .permission-changes .primary').disabled);
 await card().getByLabel('Default execution',{exact:true}).selectOption('allow');
 assert(await page.evaluate(()=>machineFixture.originalSelect===document.activeElement&&machineFixture.originalSelect===document.querySelector('.machine-card select')),'Pending authority read preserves focused local editing');
 assert(await card().getByRole('button',{name:'Save permissions',exact:true}).isDisabled(),'Unconfirmed authority cannot save a local edit');
 await page.evaluate(()=>document.querySelector('.machine-card .permission-changes .primary').click());
 assert(await page.evaluate(()=>machineFixture.calls.filter(call=>call.method==='POST').length===machineFixture.postCount),'Pending authority cannot issue a command');
 await page.evaluate(()=>{machineFixture.holdPolicy=false;machineFixture.client.read=machineFixture.read;for(const release of machineFixture.metadataReads)release();machineFixture.releasePolicy();return machineFixture.policyRefresh;});
 assert(await card().getByLabel('Default execution',{exact:true}).inputValue()==='allow','Fresh policy preserves a dirty local edit');
 assert(await page.evaluate(()=>machineFixture.originalSelect===document.activeElement),'Fresh policy preserves the original select focus');
 await card().getByRole('button',{name:'Cancel',exact:true}).click();
 assert(await card().getByLabel('Default execution',{exact:true}).inputValue()==='deny','Cancel uses the confirmed saved policy');
 await card().locator('.permission-rules>summary').click();await card().getByRole('button',{name:'Add permission',exact:true}).click();await page.getByLabel('Exact command',{exact:true}).fill('npm test');await page.getByLabel('Working directory',{exact:true}).fill('/Projects/local');await page.getByRole('button',{name:'Save',exact:true}).click();await card().getByText('npm test',{exact:true}).waitFor();
 assert(await page.evaluate(()=>machineFixture.calls.some(call=>call.body?.action==='save_rule'&&call.space==='local'&&call.owner===''&&call.body.expected_revision===2)),'Nested command editor retains the local authority');
 await card().getByLabel('Default execution',{exact:true}).selectOption('allow');await page.evaluate(()=>{machineFixture.language='zh-CN';window.dispatchEvent(new Event('pot:locale'));});await card().getByLabel('默认执行方式',{exact:true}).waitFor();
 assert(await card().getByLabel('默认执行方式',{exact:true}).inputValue()==='allow','Language changes retain the unsaved policy');
 await card().getByRole('button',{name:'取消',exact:true}).click();await page.evaluate(()=>{machineFixture.language='en';machineFixture.offline=true;return machineFixture.mount();});await selectMachine();
 await card().getByText('Execution permissions are unavailable.',{exact:true}).waitFor();assert(!await card().getByLabel('Default execution',{exact:true}).count(),'An unknown first policy read never invents a writable default');
 await page.evaluate(()=>{machineFixture.offline=false;machineFixture.policy.application.host={state:'pending',error:'Cleanup receipt missing',recovery_required:true};return machineFixture.pages.show('machines');});
 assert(await card().getByRole('button',{name:'Owner recovery required',exact:true}).isDisabled(),'Unknown cleanup remains fenced');
 await page.setViewportSize({width:393,height:852});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Narrow local policy fits the viewport');
 await page.evaluate(()=>machineFixture.pages.dispose());
 // The presentation/routing boundary uses real page/tool/approval components,
 // with display text deliberately different from the submitted machine ID.
 await page.setViewportSize({width:1200,height:852});
 await page.evaluate(async()=>{
  const {createPages}=await import('/components/pages.mjs'),{createToolCall}=await import('/components/tool-call.mjs'),{machinePresentation}=await import('/state/machine-identity.mjs');
  const f=window.identityFixture={locale:'en',writes:[]},capabilities={version:1,scope:'owner',operations:['task.read','task.list','task.events','task.input','schedules.manage','approvals.manage']};
  const task={id:'identity-task',title:'Machine identity task',assistant_id:'assistant',machine_id:'opaque-machine',phase:'running'};
  f.client={runtime:{boot_id:'identity-boot'},spaces:[{id:'local',control_id:'identity-node',name:'Local',kind:'local'}],assistants:[{id:'assistant',space_id:'local',name:'Assistant',capabilities}],scope:()=>({space:'local',owner:'assistant'}),request:async(path,options={})=>{
   if(path==='/api/v1/runtime')return {runtime_id:'opaque-machine',host:{hostname:'dev-box.local',ip_addresses:['192.168.1.24']}};
   if(path==='/api/v1/permissions')return {pending:[]};
   if(path.includes('/tasks/catalog'))return {items:[task],counts:{active:1},total:1};
   if(path==='/api/v1/tasks/identity-task')return {capabilities,info:task,request:{text:'Synthetic task goal'}};
   if(path.includes('/events?'))return {events:[],cursor:0};
   if(path.endsWith('/messages'))return [];
   if(path==='/api/v1/schedules/preview')return {timezone:'UTC',times:[Date.now()+60000]};
   if(path.startsWith('/api/v1/schedules')){if(options.method==='POST')f.writes.push({body:options.body,space:options.space,owner:options.owner});return {items:[]};}
   return {items:[]};
  }};
  f.root=document.querySelector('main');f.root.replaceChildren();
  f.pages=createPages({client:f.client,host:f.root,approvalHost:document.createElement('div'),chatApprovalHost:document.createElement('div'),chatVisible:()=>false,locale:()=>f.locale,notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});await f.pages.show('tasks');
  f.tool=createToolCall({locale:()=>f.locale,machineLabel:id=>machinePresentation(f.client).label(id,{space:'local'},f.locale)});f.root.append(f.tool.element);
  f.update=()=>f.tool.update({tool:{id:'identity-call',turn_id:'identity-turn',name:'exec_command',args:{cmd:'pwd'},target:{runtime_id:'opaque-machine'},phase:'running'}},{approval:{id:'identity-approval',request:{machine_id:'opaque-machine',tool:'exec_command',args:{cmd:'pwd'}}}});f.update();
 });
 try{
  const label='dev-box.local · 192.168.1.24';
  assert((await page.locator('.task-row .task-meta').innerText()).includes(label),'Task rows present host/IP');
  assert((await page.locator('.tool-context').innerText()).includes(label)&&await page.locator('.approval-machine').textContent()===label,'Tool and approval present host/IP');
  assert(!(await page.locator('.tool-call').innerText()).includes('opaque-machine'),'Opaque routing ID stays out of tool/approval prose');
  await page.evaluate(()=>{identityFixture.locale='zh-CN';identityFixture.update();});assert(await page.locator('.approval-state').textContent()==='等待授权','Open approval follows its current locale');
  await page.evaluate(()=>{identityFixture.locale='en';identityFixture.tool.element.remove();});await page.getByRole('button',{name:'Machine identity task',exact:true}).click();await page.locator('.task-conversation').waitFor();
  await page.evaluate(()=>identityFixture.pages.taskMenu());await page.getByRole('dialog').getByRole('button',{name:'Task details',exact:true}).click();assert((await page.locator('.task-info').innerText()).includes(label),'Task details resolve the original host/IP');
  await page.evaluate(async()=>{document.querySelector('dialog[open]').close();await identityFixture.pages.show('tasks',{force:true});});
  await page.getByRole('button',{name:'Schedules',exact:true}).click();await page.getByRole('button',{name:'New schedule',exact:true}).click();
  const machine=page.getByLabel('Execution machine',{exact:true});await page.waitForFunction(()=>document.querySelector('select[aria-label="Execution machine"]')?.options.length===2);
  assert((await machine.locator('option').allTextContents()).includes(label),'Schedule choices present host/IP');await machine.selectOption('opaque-machine');
  await page.getByLabel('Title',{exact:true}).fill('Identity schedule');await page.getByLabel('Goal',{exact:true}).fill('Synthetic fixture only');
  await page.evaluate(()=>{identityFixture.locale='zh-CN';window.dispatchEvent(new Event('pot:locale'));});assert(await page.getByLabel('执行机器',{exact:true}).inputValue()==='opaque-machine','Locale preserves the chosen opaque routing ID');
  await page.getByRole('button',{name:'保存',exact:true}).click();await page.waitForFunction(()=>identityFixture.writes.length===1);
  const write=await page.evaluate(()=>identityFixture.writes[0]);assert(write.body.spec.machine_id==='opaque-machine'&&write.space==='local'&&write.owner==='assistant','Schedule submits the opaque ID and original Space/owner');
 }finally{await page.evaluate(()=>identityFixture.pages.dispose());}
 return {passed:true,localPolicy:true,pendingWriteFence:true,nestedCommandScope:true,localeDraft:true,unknownPolicy:true,recoveryRequired:true,mobileWidth:393,identityPresentation:true,scheduleRouting:true};
}
