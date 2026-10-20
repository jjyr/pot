// Disposable multi-Space UI fixture. No real permissions or machines are changed.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 await page.goto(new URL('/',page.url()).href);
 await page.evaluate(async()=>{
  const {createPages}=await import('/components/pages.mjs'),{node:n}=await import('/components/primitives.mjs');
  const policy=mode=>({machines:{runner:{execution_access:mode,revision:1}},rules:[],task_grants:[],application:{}});
  const fixture=window.machineFixture={policies:{local:policy('ask'),cloud:policy('allow')},calls:[],notices:[],offline:false,hold:false,language:'en'};
  document.body.replaceChildren();const shell=n('div','pot-app surface-main'),host=n('main'),approvals=n('div');shell.style.display='block';host.className='page-host';host.style.height='100%';shell.append(host,approvals);document.body.append(shell);
  const client={desktop:false,runtime:{boot_id:'fixture-boot'},spaces:[{id:'local',control_id:'local-control',name:'Local',connected:true},{id:'cloud',control_id:'cloud-control',name:'Cloud',connected:true}],scope:()=>({space:'local',owner:'selected-assistant'}),request:async(path,options)=>{
   fixture.calls.push({path,...options});const current=fixture.policies[options.space];
   if(fixture.offline&&options.space==='cloud'&&path==='/api/v1/permissions')throw Error('Cloud unavailable');
   if(path.startsWith('/api/v1/agent-links'))return {self:{id:options.space+'-agent',node_id:options.space+'-control',kind:'pot',status:'needs_access'},items:[]};
   if(path==='/api/v1/machines')return {items:[{id:'runner',name:'Identical machine ID',connected:true,tags:['linux']}]};
   if(path==='/api/v1/permissions'){
    if(options.method==='POST'){
     if(fixture.hold)await new Promise(resolve=>fixture.release=resolve);
     const body=options.body;
     if(body.action==='configure'){if(current.machines.runner.revision!==body.machine_policy.revision)throw Error('Revision conflict');current.machines.runner={...body.machine_policy,revision:body.machine_policy.revision+1};}
     if(body.action==='save_rule'){current.rules.push({...body.rule,id:'cloud-rule'});current.machines.runner.revision++;}
     return {};
    }
    return {policy:structuredClone(current)};
   }
   throw Error('Unmocked endpoint '+path);
  }};
  fixture.client=client;
  fixture.mount=()=>{fixture.pages?.dispose();host.replaceChildren();fixture.pages=createPages({chatApprovalHost:document.createElement('div'),chatVisible:()=>false,client,host,approvalHost:approvals,locale:()=>fixture.language,notify:text=>fixture.notices.push(text),changed:()=>{},titleChanged:()=>{},openChat:()=>{}});return fixture.pages.show('machines');};
  await fixture.mount();
 });
 // Permissions render lazily in the selected topology node's inspector.
 const selectMachine=space=>page.locator(`.topology-node[data-node-id='machine:["${space}","runner"]']`).click();
 const local=()=>page.locator('.topology-inspector .machine-card[data-space-id="local"]'),cloud=()=>page.locator('.topology-inspector .machine-card[data-space-id="cloud"]');
 await selectMachine('local');assert(await local().getByLabel('Default execution',{exact:true}).inputValue()==='ask','Selecting the local node loads its own authority');
 await selectMachine('cloud');assert(await cloud().getByLabel('Default execution',{exact:true}).inputValue()==='allow','Same Runtime ID in two Spaces has separate authority');
 await cloud().getByLabel('Default execution',{exact:true}).selectOption('deny');await page.evaluate(()=>machineFixture.hold=true);await cloud().getByRole('button',{name:'Save permissions',exact:true}).click();
 await page.waitForFunction(()=>!!machineFixture.release);await page.evaluate(()=>machineFixture.pages.show('machines'));
 assert(await cloud().getByLabel('Default execution',{exact:true}).isDisabled(),'Pending saves remain disabled through live refresh');
 assert(!await page.evaluate(()=>machineFixture.pages.leave()),'Navigation cannot abandon an in-flight permission write');
 await page.evaluate(()=>{machineFixture.hold=false;machineFixture.release();});await page.waitForFunction(()=>document.querySelector('.machine-card[data-space-id="cloud"] select')?.value==='deny'&&document.querySelector('.machine-card[data-space-id="cloud"] .permission-changes')?.hidden);
 await selectMachine('local');assert(await local().getByLabel('Default execution',{exact:true}).inputValue()==='ask','Cloud write never changes local policy with same machine ID');await selectMachine('cloud');
 assert(await page.evaluate(()=>machineFixture.calls.some(call=>call.method==='POST'&&call.space==='cloud'&&call.owner===''&&call.body.machine_id==='runner'&&call.body.machine_policy.revision===1)),'Save captures exact Space, Runtime and observed revision without Chat owner');
 await cloud().locator('.permission-rules>summary').click();await cloud().getByRole('button',{name:'Add permission',exact:true}).click();await page.getByLabel('Exact command',{exact:true}).fill('npm test');await page.getByLabel('Working directory',{exact:true}).fill('/Projects/cloud');await page.getByRole('button',{name:'Save',exact:true}).click();await cloud().getByText('npm test',{exact:true}).waitFor();
 await selectMachine('local');assert(!await local().getByText('npm test',{exact:true}).count(),'Command rules stay within their owning Space');await selectMachine('cloud');
 assert(await page.evaluate(()=>machineFixture.calls.some(call=>call.body?.action==='save_rule'&&call.space==='cloud'&&call.owner===''&&call.body.rule.machine_id==='runner'&&call.body.expected_revision===2)),'Nested command editor returns and saves to its captured machine and Space');
 await selectMachine('local');await local().getByLabel('Default execution',{exact:true}).selectOption('allow');await page.evaluate(()=>{machineFixture.language='zh-CN';window.dispatchEvent(new Event('pot:locale'));});await local().getByLabel('默认执行方式',{exact:true}).waitFor();
 assert(await local().getByLabel('默认执行方式',{exact:true}).inputValue()==='allow','Locale changes preserve the unsaved inline policy');
 await local().getByRole('button',{name:'取消',exact:true}).click();await page.evaluate(()=>{machineFixture.language='en';machineFixture.offline=true;return machineFixture.pages.show('machines');});
 assert(!await local().getByLabel('Default execution',{exact:true}).isDisabled(),'A healthy Space keeps its permission controls enabled');await selectMachine('cloud');assert(await cloud().getByLabel('Default execution',{exact:true}).isDisabled(),'Disconnected Space fences cached controls without disabling healthy Space');
 await page.evaluate(()=>machineFixture.mount());await selectMachine('cloud');await cloud().getByText('Execution permissions are unavailable.',{exact:true}).waitFor();
 assert(!await cloud().getByLabel('Default execution',{exact:true}).count(),'Failed first policy read never guesses a writable default');
 await page.evaluate(()=>{machineFixture.offline=false;return machineFixture.pages.show('machines');});await cloud().getByLabel('Default execution',{exact:true}).waitFor();
 await page.evaluate(()=>{machineFixture.offline=true;machineFixture.client.spaces[1].control_id='replacement-control';return machineFixture.pages.show('machines');});await cloud().getByText('Execution permissions are unavailable.',{exact:true}).waitFor();
 assert(!await cloud().getByLabel('Default execution',{exact:true}).count(),'Cached policy cannot cross a changed Control identity');
 await page.evaluate(()=>{machineFixture.offline=false;machineFixture.policies.cloud.application.runner={state:'pending',error:'Cleanup receipt missing',recovery_required:true};return machineFixture.pages.show('machines');});
 assert(await cloud().getByRole('button',{name:'Owner recovery required',exact:true}).isDisabled(),'Recovery-required application stays visible and cannot manufacture a retry');
 await page.setViewportSize({width:393,height:852});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Narrow machine cards fit the viewport');
 await page.evaluate(()=>{machineFixture.pages.dispose();});
 return {passed:true,multi_space_identity:true,pending_write_fence:true,nested_command_scope:true,locale_draft:true,partial_offline:true,unknown_policy:true,control_identity:true,recovery_required:true,mobile_width:393};
}
