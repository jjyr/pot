// Production DOM components with deterministic transport boundaries. No model
// calls: this tests receipt/SSE ordering, one reply identity, and failure styles.
async page=>{
 const assert=(value,message)=>{if(!value)throw Error(message);},errors=[];
 const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 await page.setViewportSize({width:1200,height:820});
 await page.route('**/replies-fixture?*',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><body></body></html>'}));
 await page.goto(new URL('/replies-fixture?locale=zh-CN&theme=light&conversation=main',page.url()).href);
 await page.evaluate(async()=>{
  const {mountApp}=await import('/components/ui.js');
  const root=document.createElement('div');document.body.replaceChildren(root);
  const context={owner:'',space:''},empty={runtime_id:'reply-runtime',boot_id:'boot',owner_id:JSON.stringify(['local','main']),conversation_id:'main',cursor:'1',running:false,turn_id:null,kind:'reset',replies:[],history:{before:'',after:'',has_more:false,has_newer:false},messages:[]};
  const fixture=window.replyFixture={snapshot:empty,requests:[],subscriptions:0,changeSubscriptions:0};
  const client={production:true,runtime:{runtime_id:'reply-runtime',boot_id:'boot'},scope:()=>({...context}),setScope:value=>Object.assign(context,value),
   snapshot:async()=>structuredClone(context.owner==='other'?{...empty,owner_id:JSON.stringify(['local','other']),conversation_id:'other'}:fixture.snapshot),
   send:request=>{fixture.requests.push(request);return new Promise((resolve,reject)=>{fixture.resolveSend=resolve;fixture.rejectSend=reject;});},
   cancel:async()=>{},changes:()=>{fixture.changeSubscriptions++;return ()=>{};},
   subscribe:(deliver,status)=>{fixture.deliver=deliver;fixture.status=status;fixture.subscriptions++;status('connected');queueMicrotask(async()=>deliver(await client.snapshot()));return ()=>{};},
   request:async path=>{
    if(path==='/api/v1/spaces')return {active_space_id:'local',items:[{id:'local',control_id:'reply-runtime',name:'Local',kind:'local',connected:true}]};
    if(path==='/api/v1/assistants')return [{id:'main',name:'Pot',phase:'ready'},{id:'other',name:'Other assistant',phase:'ready'}];
    if(path==='/api/v1/permissions')return {pending:[],policy:{machines:{}}};
    if(path==='/api/v1/providers')return {profiles:[],connections:[],directory:[],default_provider_id:''};
    if(path.startsWith('/api/v1/tasks/query'))return {items:[],counts:{}};
    if(path.startsWith('/api/v1/tasks/outcomes'))return {items:[],cursor:0,has_more:false};
    throw Error('Unexpected reply fixture endpoint: '+path);
   },
  };
  fixture.project=value=>{const cursor=String(Number(value.cursor)+1),messages=value.messages.map((message,index)=>({...message,sequence:String(index+1),version:Number(cursor)}));return {...value,cursor,messages,replies:messages.filter(message=>message.role==='user').map(input=>{const output=messages.findLast(message=>message.reply_to===input.id);return {input_id:input.id,state:value.running&&value.turn_id===input.id?'running':output?.status==='failed'?'failed':'completed',...(output?.error?{error:output.error}:{})};}),history:{before:messages.length?'1':'',after:messages.length?String(messages.length):'',has_more:false,has_newer:false}};};
  fixture.publish=value=>{fixture.snapshot=fixture.project({...fixture.snapshot,...value});fixture.deliver(structuredClone(fixture.snapshot));};
  fixture.restart=()=>{fixture.dispose?.();client.chatCacheName='reply-active-fixture-cache';fixture.snapshot=structuredClone(empty);fixture.dispose=mountApp(root,client);};
  fixture.dispose=mountApp(root,client);
 });
 await page.waitForFunction(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
 await page.locator('#pot-composer').fill('你好');await page.locator('#pot-composer').press('Enter');
 await page.getByText('发送中…',{exact:true}).waitFor();
 assert(await page.locator('.role-assistant').count()===1,'Sending immediately creates one assistant bubble');
 await page.evaluate(()=>{replyFixture.bubble=document.querySelector('.reply-bubble');replyFixture.user=document.querySelector('.role-user');const request=replyFixture.requests[0];replyFixture.input={id:'input',request_id:request.request_id,role:'user',text:request.text,status:'accepted'};replyFixture.publish({messages:[replyFixture.input],running:true,turn_id:'input'});});
 await page.getByText('思考中',{exact:true}).waitFor();
 assert(await page.evaluate(()=>document.querySelector('.reply-bubble')===replyFixture.bubble&&document.querySelector('.role-user')===replyFixture.user),'SSE acceptance adopts the optimistic nodes');
 await page.evaluate(()=>replyFixture.resolveSend({message_id:'input',duplicate:false}));
 await page.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
 await page.evaluate(()=>replyFixture.publish({messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'streaming',text:'你好！'}]}));
 await page.locator('.reply-content').getByText('你好！',{exact:true}).waitFor();
 assert(await page.evaluate(()=>document.querySelector('.reply-bubble')===replyFixture.bubble),'Streaming updates the same bubble');
 assert(await page.locator('.role-assistant').count()===1,'Streaming does not leave a second Thinking placeholder');
 await page.evaluate(()=>replyFixture.publish({messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'complete',text:'你好！'}]}));
 assert(await page.locator('.role-assistant').count()===1,'Turn cleanup keeps feedback in the same completed message');
 await page.locator('#pot-composer').fill('保留新草稿');
 await page.evaluate(()=>replyFixture.publish({messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'failed',text:'你好！',error:'model provider returned HTTP 404'}],running:false,turn_id:null}));
 await page.getByText('回复失败',{exact:true}).waitFor();
 assert(await page.locator('.reply-bubble.is-failed').count()===1,'Failure has one distinct bubble');
 assert(await page.evaluate(()=>document.querySelector('.reply-bubble')===replyFixture.bubble),'Failure updates the same bubble');
 assert(await page.locator('.reply-content').innerText()==='你好！','Failure preserves partial prose');
 assert(!await page.locator('.connection-status').isVisible(),'Failure is not duplicated below the composer');
 assert(await page.locator('.reply-bubble button').count()===1,'Failure exposes one main action');
 assert(await page.locator('#pot-composer').inputValue()==='保留新草稿','Failure keeps newly typed text');
 await page.getByText('错误详情',{exact:true}).click();await page.getByText('model provider returned HTTP 404',{exact:true}).waitFor();
 await page.evaluate(()=>replyFixture.publish({}));
 assert(await page.locator('.reply-error-details').evaluate(node=>node.open),'Snapshot refresh retains the expanded diagnostic');
 await page.getByRole('button',{name:'检查 AI 配置',exact:true}).click();
 await page.getByText('已配置的提供商',{exact:true}).waitFor();
 await page.getByRole('button',{name:'聊天',exact:true}).click();
 await page.getByText('回复失败',{exact:true}).waitFor();
 assert(await page.locator('#pot-composer').inputValue()==='保留新草稿','Opening configuration keeps the chat draft');
 // Start an independent active-reply scenario. Durable failures are not
 // removed or rewound to a running input in a real conversation.
 await page.evaluate(()=>replyFixture.restart());
 await page.waitForFunction(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
 await page.locator('#pot-composer').fill('保留新草稿');
 await page.emulateMedia({reducedMotion:'reduce'});
 await page.evaluate(()=>replyFixture.publish({messages:[replyFixture.input],running:true,turn_id:'input'}));
 await page.getByText('思考中',{exact:true}).waitFor();
 assert(await page.locator('.waiting-dots i').first().evaluate(node=>getComputedStyle(node).animationName)==='none','Reduced Motion disables thinking animation');
 await page.emulateMedia({reducedMotion:'no-preference'});
 await page.evaluate(()=>replyFixture.status('offline'));
 await page.locator('.connection-status').getByText('实时连接已中断，正在重新连接…',{exact:true}).waitFor();
 await page.locator('.reply-bubble .reply-status').waitFor({state:'hidden'});
 assert(await page.locator('.reply-bubble.is-failed').count()===0,'An interrupted connection does not declare the reply failed');
 assert(await page.evaluate(()=>replyFixture.requests.length)===1,'Connection failure never resends input');
 await page.evaluate(()=>replyFixture.status('connected'));
 await page.getByText('思考中',{exact:true}).waitFor();
 await page.evaluate(()=>replyFixture.publish({messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'complete',text:'连接正常'}],running:false,turn_id:null}));
 await page.getByText('连接正常',{exact:true}).waitFor();
 assert(await page.locator('.waiting-dots,.role-assistant .activity-dot').count()===0,'Completed replies stop all activity animation');
 await page.evaluate(()=>{
  replyFixture.publish({messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'streaming',text:'等待后台完成'}],running:true,turn_id:'input'});
  replyFixture.status('offline');
  replyFixture.retiredDeliver=replyFixture.deliver;replyFixture.retiredStatus=replyFixture.status;
  replyFixture.beforeResume={subscriptions:replyFixture.subscriptions,changes:replyFixture.changeSubscriptions,requests:replyFixture.requests.length};
  // The server finishes while the old stream cannot deliver. Returning to the
  // window must replace both subscriptions and fetch the terminal snapshot.
  replyFixture.snapshot=replyFixture.project({...replyFixture.snapshot,messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'complete',text:'切回后同步完成'}],running:false,turn_id:null});
 });
 await page.locator('.connection-status').getByText('实时连接已中断，正在重新连接…',{exact:true}).waitFor();
 await page.locator('.reply-bubble .reply-status').waitFor({state:'hidden'});
 await page.evaluate(()=>{window.dispatchEvent(new Event('focus'));document.dispatchEvent(new Event('visibilitychange'));});
 await page.getByText('切回后同步完成',{exact:true}).waitFor();
 assert(await page.evaluate(()=>replyFixture.subscriptions===replyFixture.beforeResume.subscriptions+1&&replyFixture.changeSubscriptions===replyFixture.beforeResume.changes+1),'Returning renews both streams once for paired lifecycle events');
 assert(await page.evaluate(()=>replyFixture.requests.length===replyFixture.beforeResume.requests),'Recovery does not resend input');
 assert(await page.locator('#pot-composer').inputValue()==='保留新草稿','Recovery preserves the draft');
 await page.evaluate(()=>{replyFixture.retiredStatus('offline');replyFixture.retiredDeliver({...replyFixture.snapshot,cursor:'999',messages:[],running:false,turn_id:null});});
 await page.getByText('切回后同步完成',{exact:true}).waitFor();
 await page.evaluate(()=>{replyFixture.beforeOnline=replyFixture.subscriptions;window.dispatchEvent(new Event('online'));});
 await page.waitForFunction(()=>replyFixture.subscriptions===replyFixture.beforeOnline+1);
 await page.getByText('切回后同步完成',{exact:true}).waitFor();
 await page.locator('.assistant-row').filter({hasText:'Other assistant'}).click();
 await page.waitForFunction(()=>document.querySelector('.window-title').textContent.includes('Other assistant'));
 assert(await page.locator('.reply-bubble').count()===0,'Another assistant cannot inherit the reply');
 await page.locator('.assistant-row').filter({hasText:'Pot'}).click();
 await page.getByText('切回后同步完成',{exact:true}).waitFor();
 assert(await page.locator('#pot-composer').inputValue()==='保留新草稿','Owner navigation restores its own draft');
 await page.locator('#pot-composer').fill('测试发送错误');await page.locator('#pot-composer').press('Enter');
 await page.getByText('发送中…',{exact:true}).waitFor();
 await page.evaluate(()=>{const error=Error('Request was rejected');error.status=409;replyFixture.rejectSend(error);});
 await page.getByText('回复失败',{exact:true}).waitFor();
 assert(await page.locator('#pot-composer').inputValue()==='测试发送错误','A rejected send preserves its draft');
 const retryID=await page.evaluate(()=>replyFixture.requests.at(-1).request_id);
 await page.getByRole('button',{name:'重试',exact:true}).click();
 await page.getByText('发送中…',{exact:true}).waitFor();
 assert(await page.evaluate(()=>replyFixture.requests.at(-1).request_id)===retryID,'Rejected-send retry retains its original receipt identity');
 assert(await page.locator('.role-user').count()===2,'Retry does not add another optimistic user message');
 await page.evaluate(()=>replyFixture.rejectSend(Error('Connection closed')));
 await page.getByText('上一条消息可能已被接受，请先确认后再发送。',{exact:true}).waitFor();
 assert(await page.getByRole('button',{name:'发送',exact:true}).isDisabled(),'Unknown acceptance keeps the next send locked until confirmation');
 assert(await page.getByRole('button',{name:'重试',exact:true}).count()===0,'Unknown send outcomes offer state reconciliation instead of resending');
 await page.evaluate(()=>replyFixture.dispose());
 page.off('pageerror',onError);assert(errors.length===0,errors.join('\n'));
 return {passed:true,same_bubble:true,one_failure_style:true,partial_text_preserved:true,send_retry_identity:true,unknown_send_not_replayed:true,owner_fencing:true,reduced_motion:true,errors};
}
