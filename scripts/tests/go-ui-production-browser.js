// Disposable browser contract test: API replies are explicitly mocked here.
// This does not validate backend integration or make a visual parity claim.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 const errors=[],posts=[],reads=[];
 const fileTask='de305d54-75b4-431b-adb2-eb6b9e546014';
 const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 let definitions=[];
 await page.addInitScript(()=>{if(new URLSearchParams(location.search).get('contract')!=='ui-production')return;const sources=new Set();window.EventSource=class extends EventTarget{constructor(url){super();this.url=String(url);sources.add(this);queueMicrotask(()=>this.onopen?.({}));}close(){sources.delete(this);}};window.__potContractChanges=topics=>{for(const source of sources)if(source.url.includes('/changes'))source.dispatchEvent(new MessageEvent('changed',{data:JSON.stringify(topics)}));};window.__potContractSubscriptions=()=>[...sources].filter(source=>source.url.includes('/changes')).length;});
 let rules={text:'original rules',revision:1},thread={owner:'alpha',generation:1,revision:0,pending:false,selection:{profile_id:'p',profile_revision:1,model:'test-model',context_window:null,max_output_tokens:null,reasoning_effort:null},config:{source_profile_id:'p',model:'test-model'}};
 const providers={profiles:[{id:'p',name:'Connection',model:'test-model',revision:1,params:{max_output_tokens:4096}}],connections:[{profile_id:'p',endpoint:'https://provider.example/v1/responses',protocol:'openai_responses',has_key:true}],default_provider_id:'p',directory:[{id:'openai',name:'OpenAI',common:true,endpoint:'https://provider.example/v1/responses',protocol:'openai_responses',models:[{id:'test-model',name:'Test model',context:128000,input:120000,output:8192}]}],model_defaults:{'test-model':{context:128000,input:120000,output:8192}}};
 const handler=async route=>{
  const url=new URL(route.request().url()),path=url.pathname,method=route.request().method();
  if(method==='GET')reads.push({path,owner:url.searchParams.get('assistant_id'),space:url.searchParams.get('space_id')});
  if(method==='POST')posts.push({path,body:route.request().postDataJSON(),owner:url.searchParams.get('assistant_id'),space:url.searchParams.get('space_id')});
  let value;
  if(path==='/api/v1/runtime')value={runtime_id:'contract-test',boot_id:'boot'};
  else if(path==='/api/v1/spaces')value={active_space_id:'local',items:[{id:'local',name:'Local',kind:'local',connected:true}]};
  else if(path==='/api/v1/assistants')value=definitions;
  else if(path==='/api/v1/snapshot')value={cursor:1,api_version:3,boot_id:'boot',conversation_id:url.searchParams.get('assistant_id'),messages:[{id:'file-message',role:'assistant',status:'complete',text:`[Download report](pot-file://${fileTask}/reports/result.txt)`}],turn:null,agent:{phase:'ready',model:'test-model'}};
  else if(path===`/api/v1/tasks/${fileTask}/file`)value={size:7,offset:0,revision:'fixture',data:'cmVwb3J0Cg=='};
  else if(path===`/api/v1/tasks/${fileTask}/file-download`){await route.fulfill({status:200,contentType:'application/octet-stream',headers:{'Content-Disposition':'attachment; filename="result.txt"'},body:'report\n'});return;}
  else if(path.endsWith('/events')||path.endsWith('/changes')){await route.fulfill({status:200,contentType:'text/event-stream',body:': isolated contract test\n\n'});return;}
  else if(path==='/api/v1/providers')value=providers;
  else if(path==='/api/v1/providers/check')value={models:['model-from-api']};
  else if(path==='/api/v1/agent'){if(method==='POST'){const body=route.request().postDataJSON();thread={...thread,selection:body.selection,generation:thread.generation+1,revision:thread.revision+1};}value=thread;}
  else if(path==='/api/v1/instructions'){if(method==='POST')rules={...route.request().postDataJSON(),revision:2};value=rules;}
  else if(path==='/api/v1/permissions')value={policy:{machines:{}},pending:[]};
  else if(path==='/api/v1/tasks/query')value={items:[],total:0,next_offset:null};
  else if(path==='/api/v1/schedules')value={items:[],total:0};
  else if(path==='/api/v1/machines')value={items:[]};
  else if(path==='/api/v1/usage/summary')value={total:{input:4,output:2,requests:1},buckets:[],requests:[],tools:[],compactions:[],quota:{active:false,policy:{period:'none',limit:100,timezone:'UTC',auto_resume:true}}};
  else if(path.startsWith('/api/v1/assistants/')){const body=route.request().postDataJSON();const definition=definitions.find(value=>value.id===path.split('/').pop());Object.assign(definition,{name:body.name,revision:definition.revision+1});value=definition;}
  else {await route.fulfill({status:404,contentType:'application/json',body:JSON.stringify({message:'Unmocked endpoint: '+path})});return;}
  await route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(value)});
 };
 await page.route('**/api/v1/**',handler);
 try {
  await page.setViewportSize({width:1200,height:792});
  await page.goto('http://127.0.0.1:17702/?theme=light&locale=en&contract=ui-production');
  await page.locator('.assistant-directory').getByText('Create your first assistant',{exact:true}).waitFor();
  definitions=[{id:'alpha',name:'Alpha',revision:1},{id:'beta',name:'Beta',revision:1}];
  await page.evaluate(()=>window.__potContractChanges({assistants:1}));
  await page.waitForFunction(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
  assert(await page.title()==='Pot','Production bootstrap selected');
  assert(await page.evaluate(()=>window.__potContractSubscriptions())===1,'One global changes subscription exists without an initial assistant');
  const downloadPromise=page.waitForEvent('download',{timeout:15000});await page.getByRole('link',{name:'Download report',exact:true}).click();const download=await downloadPromise;
  assert(download.suggestedFilename()==='result.txt','Task file uses browser download manager');
  assert(reads.filter(value=>value.path.includes(fileTask)).every(value=>value.owner==='alpha'&&value.space==='local'),'Task file preflight and download retain captured owner and Space');
  await page.evaluate(()=>window.__potContractChanges({assistants:1,usage:2}));
  await page.waitForFunction(()=>!document.querySelector('#pot-composer')?.disabled);
  const directoryReads=reads.filter(value=>value.path==='/api/v1/assistants').length;
  await page.evaluate(()=>window.__potContractChanges({assistants:1,usage:3}));
  // A usage-only topic must not refetch the assistant directory.
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  assert(reads.filter(value=>value.path==='/api/v1/assistants').length===directoryReads,'Unchanged topic versions never refresh unrelated catalogs');
  const input=page.locator('#pot-composer');await input.fill('alpha draft');
  await page.locator('.assistant-row').filter({hasText:'Beta'}).click();await page.waitForFunction(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
  await input.fill('beta draft');await page.locator('.assistant-row').filter({hasText:'Alpha'}).click();
  await page.waitForFunction(()=>document.querySelector('#pot-composer')?.value==='alpha draft');
  await page.getByRole('button',{name:'Assistant settings',exact:true}).click();
  await page.getByLabel('Name',{exact:true}).fill('Edited Alpha');
  await page.getByRole('button',{name:'Machines',exact:true}).click();
  await page.getByRole('button',{name:'Keep editing',exact:true}).click();
  assert(await page.getByLabel('Name',{exact:true}).inputValue()==='Edited Alpha','Dirty guard retains editor');
  await page.getByLabel('Instructions',{exact:true}).fill('updated rules');
  await page.getByRole('button',{name:'Save',exact:true}).click();
  await page.locator('#pot-composer').waitFor({state:'visible'});
  assert(posts.some(value=>value.path==='/api/v1/instructions'&&value.owner==='alpha'&&value.space==='local'),'Rules save retains captured owner');
  assert(await input.inputValue()==='alpha draft','Returning from settings preserves Chat draft');
  await page.getByRole('button',{name:'Assistant settings',exact:true}).click();
  await page.getByLabel('Context window',{exact:true}).fill('64000');await page.getByLabel('Maximum output tokens',{exact:true}).fill('2000');await page.getByLabel('Reasoning effort',{exact:true}).fill('custom-effort');
  await page.getByRole('button',{name:'Save',exact:true}).click();await input.waitFor({state:'visible'});
  const configSave=posts.find(value=>value.path==='/api/v1/agent');
  assert(configSave?.body.selection.context_window===64000&&configSave.body.selection.max_output_tokens===2000&&configSave.body.selection.reasoning_effort==='custom-effort','Assistant selection submits independent nullable overrides');
  assert(configSave.body.request_id&&configSave.body.expected_generation===1&&configSave.body.expected_revision===0,'Assistant save carries stable request identity and both CAS values');
  for(const name of ['Tasks','Machines','Usage','Settings']) {await page.getByRole('button',{name,exact:true}).first().click();await page.locator('.page-content').waitFor();}
  await page.getByRole('button',{name:'Add connection',exact:true}).click();
  await page.getByLabel('Model',{exact:true}).selectOption('test-model');
  assert(await page.getByLabel('Context window',{exact:true}).inputValue()==='','Inherited context remains empty');
  assert(await page.getByLabel('Context window',{exact:true}).getAttribute('placeholder')==='128000','Known model inherits provider-specific context');
  assert(await page.getByLabel('Context window',{exact:true}).isEnabled(),'Inherited context can be overridden directly');
  await page.getByLabel('Reasoning effort',{exact:true}).fill('custom-effort');
  await page.getByLabel('API key',{exact:true}).fill('isolated-fixture-key');
  await page.getByRole('button',{name:'Check available models',exact:true}).click();
  await page.getByRole('option',{name:'model-from-api',exact:true}).waitFor({state:'attached'});
  assert(posts.filter(value=>value.path==='/api/v1/providers').length===0,'Checking never saves a connection');
  assert(posts.filter(value=>value.path==='/api/v1/messages').length===0,'Checking never sends a model message');
  await page.getByRole('button',{name:'Cancel',exact:true}).click();await page.getByRole('button',{name:'Discard changes',exact:true}).click();
  await page.getByRole('button',{name:'Edit',exact:true}).click();await page.getByLabel('Name',{exact:true}).fill('Changed provider');
  await page.getByRole('button',{name:'Cancel',exact:true}).click();await page.getByRole('button',{name:'Discard changes',exact:true}).click();
  await page.getByRole('button',{name:'Chat',exact:true}).click();await input.waitFor({state:'visible'});
  assert(await input.inputValue()==='alpha draft','Independent pages retain Chat draft');
  assert(await page.evaluate(()=>window.__potContractSubscriptions())===1,'Owner switching never duplicates global changes subscriptions');
  assert(errors.length===0,errors.join('\n'));
  return {posts:posts.map(value=>value.path==='/api/v1/providers/check'?{path:value.path,owner:value.owner,space:value.space}:value),errors};
 } finally {await page.unroute('**/api/v1/**',handler);page.off('pageerror',onError);}
}
