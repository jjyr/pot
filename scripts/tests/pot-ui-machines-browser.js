// Local execution policy uses production controls and synthetic IO. Remote
// execution belongs to the receiving Agent Link node and is never configured here.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 await page.route('**/machine-policy-fixture',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><body></body></html>'}));
 await page.goto(new URL('/machine-policy-fixture',page.url()).href);
 await page.evaluate(async()=>{
  const {createPages}=await import('/components/pages.mjs'),{node:n}=await import('/components/primitives.mjs');
  const fixture=window.machineFixture={policy:{machines:{host:{execution_access:'ask',revision:1}},rules:[],task_grants:[],application:{}},calls:[],notices:[],offline:false,hold:false,language:'en'};
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
 await card().getByLabel('Default execution',{exact:true}).selectOption('deny');await page.evaluate(()=>machineFixture.hold=true);await card().getByRole('button',{name:'Save permissions',exact:true}).click();
 await page.waitForFunction(()=>!!machineFixture.release);await page.evaluate(()=>machineFixture.pages.show('machines'));
 assert(await card().getByLabel('Default execution',{exact:true}).isDisabled(),'Pending saves remain disabled through refresh');
 assert(!await page.evaluate(()=>machineFixture.pages.leave()),'Navigation cannot abandon an in-flight permission write');
 await page.evaluate(()=>{machineFixture.hold=false;machineFixture.release();});await page.waitForFunction(()=>document.querySelector('.machine-card select')?.value==='deny'&&document.querySelector('.machine-card .permission-changes')?.hidden);
 assert(await page.evaluate(()=>machineFixture.calls.some(call=>call.method==='POST'&&call.space==='local'&&call.owner===''&&call.body.machine_id==='host'&&call.body.machine_policy.revision===1)),'Permission save captures the local machine and revision independently of Chat');
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
 return {passed:true,localPolicy:true,pendingWriteFence:true,nestedCommandScope:true,localeDraft:true,unknownPolicy:true,recoveryRequired:true,mobileWidth:393};
}
