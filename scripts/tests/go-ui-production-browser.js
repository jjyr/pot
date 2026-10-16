// Disposable browser contract test: API replies are explicitly mocked here.
// This does not validate backend integration or make a visual parity claim.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 const errors=[],posts=[];
 const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 let definitions=[{id:'alpha',name:'Alpha',revision:1},{id:'beta',name:'Beta',revision:1}];
 let rules={text:'original rules',revision:1},thread={owner:'alpha',generation:1,config:{profile_id:'p'}};
 const providers={profiles:[{id:'p',name:'Connection',model:'test-model',revision:1,params:{max_output_tokens:4096}}],connections:[{profile_id:'p',endpoint:'https://provider.example/v1/responses',protocol:'openai_responses',has_key:true}],default_provider_id:'p'};
 const handler=async route=>{
  const url=new URL(route.request().url()),path=url.pathname,method=route.request().method();
  if(method==='POST')posts.push({path,body:route.request().postDataJSON(),owner:url.searchParams.get('assistant_id'),space:url.searchParams.get('space_id')});
  let value;
  if(path==='/api/v1/runtime')value={runtime_id:'contract-test',boot_id:'boot'};
  else if(path==='/api/v1/spaces')value={active_space_id:'local',items:[{id:'local',name:'Local',kind:'local',connected:true}]};
  else if(path==='/api/v1/assistants')value=definitions;
  else if(path==='/api/v1/snapshot')value={cursor:1,api_version:3,boot_id:'boot',conversation_id:url.searchParams.get('assistant_id'),messages:[],turn:null,agent:{phase:'ready',model:'test-model'}};
  else if(path.endsWith('/events')||path.endsWith('/changes')){await route.fulfill({status:200,contentType:'text/event-stream',body:': isolated contract test\n\n'});return;}
  else if(path==='/api/v1/providers')value=providers;
  else if(path==='/api/v1/agent')value=thread;
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
  await page.goto('http://127.0.0.1:17702/?theme=light&locale=en');
  await page.waitForFunction(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
  assert(await page.title()==='Pot','Production bootstrap selected');
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
  for(const name of ['Tasks','Machines','Usage','Settings']) {await page.getByRole('button',{name,exact:true}).first().click();await page.locator('.page-content').waitFor();}
  await page.getByRole('button',{name:'Edit',exact:true}).click();await page.getByLabel('Name',{exact:true}).fill('Changed provider');
  await page.getByRole('button',{name:'Cancel',exact:true}).click();await page.getByRole('button',{name:'Discard changes',exact:true}).click();
  await page.getByRole('button',{name:'Chat',exact:true}).click();await input.waitFor({state:'visible'});
  assert(await input.inputValue()==='alpha draft','Independent pages retain Chat draft');
  assert(errors.length===0,errors.join('\n'));
  return {posts,errors};
 } finally {await page.unroute('**/api/v1/**',handler);page.off('pageerror',onError);}
}
