// Production DOM components with deterministic transport boundaries. No model
// calls: this tests receipt/SSE ordering, stable activity, and failure styles.
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
  const client={production:true,desktop:true,runtime:{runtime_id:'reply-runtime',boot_id:'boot'},scope:()=>({...context}),setScope:value=>Object.assign(context,value),
   snapshot:async()=>structuredClone(context.owner==='other'?{...empty,owner_id:JSON.stringify(['local','other']),conversation_id:'other'}:fixture.snapshot),
   send:request=>{fixture.requests.push(request);return new Promise((resolve,reject)=>{fixture.resolveSend=resolve;fixture.rejectSend=reject;});},
   cancel:async()=>{},changes:()=>{fixture.changeSubscriptions++;return ()=>{};},sendReceipt:async(request,scope)=>{if(fixture.holdReceipt)return new Promise(()=>{});return {runtime_id:'reply-runtime',owner_id:scope.owner,request_id:request.request_id,conversation_id:request.conversation_id,user_message_id:request.user_message_id||'',retry_user_message_id:request.retry_user_message_id||'',state:'unconfirmed'};},
   subscribe:(deliver,status)=>{fixture.deliver=deliver;fixture.status=status;fixture.subscriptions++;status('connected');queueMicrotask(async()=>deliver(await client.snapshot()));return ()=>{};},
   request:async path=>{
    if(path==='/api/v1/spaces')return {active_space_id:'local',items:[{id:'local',control_id:'reply-runtime',name:'Local',kind:'local',connected:true}]};
    if(path==='/api/v1/runtime')return client.runtime;
    if(path==='/api/v1/agent-links')return {items:[]};
    if(path==='/api/v1/assistants')return [{id:'main',name:'Pot',phase:'ready'},{id:'other',name:'Other assistant',phase:'ready'}].map(assistant=>({...assistant,capabilities:{version:1,scope:'owner',operations:['chat.read','chat.send','chat.subscribe','chat.cancel','assistant.configure','assistant.usage','task.list','task.outcomes','approvals.manage']}}));
    if(path==='/api/v1/permissions')return {pending:[],policy:{machines:{}}};
    if(path==='/api/v1/providers')return {profiles:[],connections:[],directory:[],default_provider_id:''};
    if(path.startsWith('/api/v1/tasks/query'))return {items:[],counts:{}};
    if(path.startsWith('/api/v1/tasks/outcomes'))return {items:[],cursor:0,has_more:false};
    throw Error('Unexpected reply fixture endpoint: '+path);
   },
  };
  fixture.project=value=>{const cursor=String(Number(value.cursor)+1),messages=value.messages.map((message,index)=>({...message,sequence:String(index+1),version:Number(cursor)}));return {...value,cursor,messages,replies:messages.filter(message=>message.role==='user').map(input=>{const output=messages.findLast(message=>message.reply_to===input.id);return {input_id:input.id,state:value.running&&value.turn_id===input.id?'running':output?.status==='failed'?'failed':'completed',...(output?.error?{error:output.error}:{})};}),history:{before:messages.length?'1':'',after:messages.length?String(messages.length):'',has_more:false,has_newer:false}};};
  fixture.publish=value=>{fixture.snapshot=fixture.project({...fixture.snapshot,...value});fixture.deliver(structuredClone(fixture.snapshot));};
  fixture.restart=()=>{fixture.dispose?.();client.chatCacheName='reply-active-fixture-cache-'+(fixture.restarts=(fixture.restarts||0)+1);fixture.snapshot=structuredClone(empty);fixture.dispose=mountApp(root,client);};
  fixture.dispose=mountApp(root,client);
 });
 await page.waitForFunction(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
 await page.locator('#pot-composer').fill('你好');await page.locator('#pot-composer').press('Enter');
await page.waitForFunction(()=>replyFixture.requests.length===1&&document.querySelector('#pot-composer').value==='');
 await page.locator('.history[role="log"] .message-pending').waitFor();
 assert(await page.locator('.role-assistant').count()===0,'No assistant bubble until receiver confirms running');
 await page.evaluate(()=>{replyFixture.user=document.querySelector('.role-user');const request=replyFixture.requests[0];replyFixture.input={id:'input',request_id:request.request_id,role:'user',text:request.text,status:'accepted'};replyFixture.publish({messages:[replyFixture.input],running:true,turn_id:'input'});});
 await page.getByText('思考中',{exact:true}).waitFor();
 await page.evaluate(()=>{replyFixture.bubble=document.querySelector('.is-activity .reply-bubble');});
 assert(await page.evaluate(()=>document.querySelector('.role-user')===replyFixture.user),'WS acceptance retains the user node');
assert(await page.evaluate(()=>document.querySelector('.is-activity .reply-bubble')===replyFixture.bubble),'WS acceptance adopts the optimistic activity bubble');
 await page.evaluate(()=>replyFixture.resolveSend({message_id:'input',duplicate:false}));
 await page.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
 await page.evaluate(()=>replyFixture.publish({messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'streaming',text:'你好！'}]}));
 await page.locator('.reply-content').getByText('你好！',{exact:true}).waitFor();
 assert(await page.evaluate(()=>document.querySelector('.is-activity .reply-bubble')===replyFixture.bubble),'Streaming retains the execution activity bubble');
 assert(await page.locator('.role-assistant').count()===2&&await page.locator('.is-activity').count()===1,'Streaming has prose and one execution activity');
 await page.evaluate(()=>replyFixture.publish({messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'complete',text:'你好！'}]}));
 assert(await page.locator('.role-assistant').count()===2,'Completed prose retains activity until execution settles');
 await page.locator('#pot-composer').fill('保留新草稿');
 await page.evaluate(()=>replyFixture.publish({messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'failed',text:'你好！',error:'model provider returned HTTP 404'}],running:false,turn_id:null}));
 await page.getByText('回复失败',{exact:true}).waitFor();
 assert(await page.locator('.reply-bubble.is-failed').count()===1,'Failure has one distinct bubble');
 assert(await page.evaluate(()=>document.querySelector('.is-activity .reply-bubble')===replyFixture.bubble),'Failure updates the execution activity bubble');
 assert(await page.locator('.role-assistant:not(.is-activity) .reply-content').innerText()==='你好！','Failure preserves partial prose');
 assert(!await page.locator('.connection-status').isVisible(),'Failure is not duplicated below the composer');
 assert(await page.locator('.reply-bubble button').count()===1,'Failure exposes one main action');
 assert(await page.locator('#pot-composer').inputValue()==='保留新草稿','Failure keeps newly typed text');
 const failureDetails=page.locator('.history[role="log"]:not([data-preparation-surface]) .reply-error-details'); await failureDetails.locator(':scope > summary').click(); await page.waitForFunction(()=>document.querySelector('.history[role="log"]:not([data-preparation-surface]) .reply-error-details')?.open); await failureDetails.getByText('model provider returned HTTP 404',{exact:true}).waitFor();
 await page.evaluate(()=>replyFixture.publish({}));
 assert(await failureDetails.evaluate(node=>node.open),'Snapshot refresh retains the expanded diagnostic');
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
 await page.evaluate(()=>replyFixture.active={node:document.querySelector('.is-activity .reply-status'),line:document.querySelector('.is-activity .reply-status-title'),height:document.querySelector('.is-activity').getBoundingClientRect().height});
 await page.evaluate(()=>replyFixture.status('offline'));
 await page.locator('.connection-status').getByRole('button',{name:'实时连接已中断，正在重新连接…',exact:true}).waitFor();
 await page.locator('.is-activity .reply-status').getByText('连接中断，正在确认状态',{exact:true}).waitFor();
 assert(await page.evaluate(()=>{const row=document.querySelector('.is-activity');return row.querySelector('.reply-status')===replyFixture.active.node&&row.querySelector('.reply-status-title')===replyFixture.active.line&&Math.abs(row.getBoundingClientRect().height-replyFixture.active.height)<0.5;}),'Reconnecting preserves the nonterminal status identity and frame');
 assert(await page.locator('.reply-bubble.is-failed').count()===0,'An interrupted connection does not declare the reply failed');
 assert(await page.evaluate(()=>replyFixture.requests.length)===1,'Connection failure never resends input');
 await page.evaluate(()=>replyFixture.status('connected'));
 await page.locator('.is-activity .reply-status').getByText('连接中断，正在确认状态',{exact:true}).waitFor();
 await page.evaluate(()=>replyFixture.publish({}));
 await page.getByText('思考中',{exact:true}).waitFor();
 await page.evaluate(()=>replyFixture.publish({messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'complete',text:'连接正常'}],running:false,turn_id:null}));
 await page.getByText('连接正常',{exact:true}).waitFor();
 assert(await page.locator('.is-activity').count()===0,'Completed execution retires its activity');
 // A new isolated lifecycle avoids rewinding a durable terminal input.
 await page.evaluate(()=>replyFixture.restart());
 await page.waitForFunction(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
 await page.locator('#pot-composer').fill('保留新草稿');
 await page.evaluate(()=>replyFixture.publish({messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'streaming',text:'等待后台完成'}],running:true,turn_id:'input'}));
 await page.getByText('正在回复…',{exact:true}).waitFor();
 await page.evaluate(()=>{
  replyFixture.status('offline');
  replyFixture.retiredDeliver=replyFixture.deliver;replyFixture.retiredStatus=replyFixture.status;
  replyFixture.beforeResume={subscriptions:replyFixture.subscriptions,changes:replyFixture.changeSubscriptions,requests:replyFixture.requests.length};
  // The server finishes while the old stream cannot deliver. Returning to the
  // window must replace both subscriptions and fetch the terminal snapshot.
  replyFixture.snapshot=replyFixture.project({...replyFixture.snapshot,messages:[replyFixture.input,{id:'reply',reply_to:'input',role:'assistant',status:'complete',text:'切回后同步完成'}],running:false,turn_id:null});
 });
 await page.locator('.connection-status').getByRole('button',{name:'实时连接已中断，正在重新连接…',exact:true}).waitFor();
 await page.locator('.is-activity .reply-status').getByText('连接中断，正在确认状态',{exact:true}).waitFor();
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
 await page.evaluate(()=>{replyFixture.beforeRejected=replyFixture.requests.length;});
 await page.locator('#pot-composer').fill('测试发送错误');
 await page.locator('#pot-composer').press('Enter');
 await page.waitForFunction(n=>replyFixture.requests.length===n,await page.evaluate(()=>replyFixture.beforeRejected+1));
 assert(await page.locator('#pot-composer').inputValue()==='','Send commits and clears the composer locally');
 await page.getByRole('button',{name:'已保存的消息',exact:true}).click();
 await page.evaluate(()=>{const error=Error('Request was rejected');error.status=409;replyFixture.rejectSend(error);});
 await page.locator('.outbox-dialog .history:not([data-preparation-surface]) .outbox-status').filter({hasText:'消息未被接收，内容已保留。'}).waitFor();
 assert(await page.locator('.outbox-dialog').getByText('测试发送错误',{exact:true}).count()>=1,'Rejected content is retained in Saved messages');
 assert(await page.locator('.history[role="log"] .is-activity').count()===0,'Rejected send adds no failed assistant activity');
 const retryID=await page.evaluate(()=>replyFixture.requests.at(-1).request_id);
 await page.getByRole('button',{name:'重试',exact:true}).click();
 await page.waitForFunction(n=>replyFixture.requests.length===n,await page.evaluate(()=>replyFixture.beforeRejected+2));
 assert(await page.evaluate(()=>replyFixture.requests.at(-1).request_id)===retryID,'Rejected-send retry retains its original receipt identity');
 await page.getByRole('button',{name:'关闭',exact:true}).click();
 assert(await page.locator('.history[role="log"] .role-user').count()===2,'Retry does not add another optimistic user message');
 await page.evaluate(()=>{replyFixture.holdReceipt=true;});
 await page.evaluate(()=>replyFixture.rejectSend(Error('Connection closed')));
 await page.getByRole('button',{name:'已保存的消息',exact:true}).click();
 await page.locator('.outbox-dialog .history:not([data-preparation-surface]) .outbox-status').filter({hasText:'正在核对接收结果…'}).waitFor();
 assert(await page.getByRole('button',{name:'重试',exact:true}).count()===0,'Unknown send outcomes offer state reconciliation instead of resending');
 await page.getByRole('button',{name:'关闭',exact:true}).click();
 await page.locator('#pot-composer').fill('New local draft');
 assert(await page.getByRole('button',{name:'发送',exact:true}).isEnabled(),'Outbox permits local enqueue after unknown outcome');
 assert(await page.evaluate(()=>replyFixture.requests.length)===await page.evaluate(()=>replyFixture.beforeRejected+2),'Unknown outcome did not replay absent receipt');
  await page.evaluate(async()=>{
    const {createReplyBubble}=await import('/components/reply.mjs');
    const {renderMarkdown}=await import('/components/markdown.mjs');
    const f=window.fluidFixture={};
    const host=document.createElement('div');host.className='message-body';host.style.cssText='width:300px;position:fixed;left:-10000px';document.body.append(host);
    const view=createReplyBubble(host),options={t:key=>key};
    let text='Stable paragraph with **selected text**.\n\n```js\n'+'longLine(); '.repeat(100)+'\n```\n\nTail';
    view.update({text,phase:'streaming'},options);const paragraph=host.querySelector('p'),code=host.querySelector('pre');code.scrollLeft=180;
    const range=document.createRange();range.selectNodeContents(paragraph);getSelection().removeAllRanges();getSelection().addRange(range);const selected=getSelection().toString();
    text+=' extended';view.update({text,phase:'streaming'},options);
    f.markdown={sameParagraph:host.querySelector('p')===paragraph,sameCode:host.querySelector('pre')===code,scroll:code.scrollLeft,selection:getSelection().toString()===selected};
    getSelection().removeAllRanges();
    const samples=['Paragraph\n\n- item\n  - nested\n\n```js\na();\n```\n\n| A | B |\n| --- | --- |\n| C | D |','> Quote\n> **bold**\n\n## Header\n\n---\n\n![link](https://example.test/image.png)'];
    f.incremental=true;
    for(const source of samples){for(let end=1;end<=source.length;end++){view.update({text:source.slice(0,end),phase:'streaming'},options);const expected=document.createElement('div');expected.append(renderMarkdown(source.slice(0,end)));if(host.querySelector('.reply-content').innerHTML!==expected.innerHTML){f.incremental=false;throw Error('Incremental Markdown differs at '+JSON.stringify(source.slice(0,end)));}}}
    host.remove();
  });
  const markdown=await page.evaluate(()=>fluidFixture.markdown);assert(markdown.sameParagraph&&markdown.sameCode&&markdown.scroll===180&&markdown.selection,'Appended text preserves completed blocks, selection and code scrolling');
  assert(await page.evaluate(()=>fluidFixture.incremental),'Incomplete Markdown converges to the full renderer');
 await page.evaluate(()=>replyFixture.dispose());

 await (// Production reply rendering with receiver-confirmed failure envelopes.
async page=>{
 const assert=(value,message)=>{if(!value)throw Error(message);},errors=[];
 const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 await page.setViewportSize({width:1200,height:820});
 await page.evaluate(async()=>{
  const {createReplyBubble}=await import('/components/reply.mjs');
  const {projectChatReplies}=await import('/state/chat-replies.mjs');
  const {translate}=await import('/locales/messages.mjs');
  const root=document.createElement('main');root.style.cssText='max-width:840px;margin:80px auto;padding:24px';document.body.replaceChildren(root);
  const proseHost=document.createElement('div'),host=document.createElement('div');root.append(proseHost,host);const proseBubble=createReplyBubble(proseHost),bubble=createReplyBubble(host);
  const input={id:'request',role:'user',text:'Hello',status:'complete'};
  const fixture=window.outcomeFixture={root,host,bubble,locale:'zh-CN',input,project:projectChatReplies};
  fixture.render=(state='failed',partial='',error='codex_thread_open_failed',project=true)=>{
   const text=JSON.stringify({state,error,partial_output:partial});
   const message={id:'reply',reply_to:input.id,role:'assistant',status:'complete',text};
   const rows=project?projectChatReplies({messages:[input,message],replies:[{input_id:input.id,state,error}]}):[{...message,phase:state,error}];
   const options={t:key=>translate(fixture.locale,key)};
   proseBubble.update(rows.find(row=>row.activity===false)||{text:'',phase:'complete',activity:false},options);
   bubble.update(rows.at(-1),options);
  };
 });
 for(const locale of ['en','zh-CN']){
  await page.evaluate(locale=>{outcomeFixture.locale=locale;outcomeFixture.render();},locale);
  await page.getByText(locale==='en'?'Reply failed':'回复失败',{exact:true}).waitFor();
  assert(await page.locator('.reply-content:visible').count()===0,'Empty remote failures hide protocol JSON');
  await page.locator('.reply-error-details summary').click();
  assert(await page.locator('.reply-error-details pre').innerText()==='codex_thread_open_failed','Remote diagnostic remains in details');
  await page.evaluate(()=>outcomeFixture.render('failed','Partial answer','codex_thread_open_failed: session unavailable'));
  assert(await page.locator('.reply-content:visible').innerText()==='Partial answer','Only actual partial prose is rendered');
  assert(await page.locator('.reply-error-details').evaluate(node=>node.open),'Diagnostic updates retain disclosure state');
  await page.evaluate(()=>outcomeFixture.render('cancelled','Partial answer',''));
  assert(await page.locator('.reply-content:visible').innerText()==='Partial answer'&&await page.locator('.is-failed').count()===0,'Cancellation preserves prose with its stopped state');
 }
 for(const locale of ['en','zh-CN']){
  await page.evaluate(locale=>{outcomeFixture.locale=locale;outcomeFixture.render('failed','','codex_thread_in_use: codex_rpc_rejected_thread_resume (-32600): thread busy-thread already has an active writer');},locale);
  const summary=await page.locator('.reply-error-summary').innerText();
  assert(locale==='en'?summary.includes('Another Codex client on the Codex host')&&summary.includes('archive and restore')&&summary.includes('history is retained'):summary.includes('另一个客户端占用')&&summary.includes('临时归档再恢复')&&summary.includes('聊天记录会保留'),'Writer conflict explains the cause and recovery in the current locale');
  assert(await page.locator('.reply-content:visible').count()===0,'Writer conflict keeps protocol JSON out of prose');
  if(!await page.locator('.reply-error-details').evaluate(node=>node.open))await page.locator('.reply-error-details summary').click();
  assert(await page.locator('.reply-error-details pre').innerText()==='codex_thread_in_use: codex_rpc_rejected_thread_resume (-32600): thread busy-thread already has an active writer','Writer diagnostics remain verbatim');
  assert(await page.locator('.reply-status:visible').getAttribute('role')==='alert','Writer conflict remains an accessible error');
 }

 page.off('pageerror',onError);assert(errors.length===0,errors.join('\n'));
 return {passed:true,locales:['en','zh-CN'],empty_failure:true,partial_output:true,diagnostic_disclosure:true};
}

)(page);

 await (async page=>{
 const assert=(value,message)=>{if(!value)throw Error(message);};
 await page.evaluate(async()=>{
  const {createTurnContinuation}=await import('/components/turn-continuation.mjs');
  const fixture=window.continuationTest={locale:'en',scope:{space:'remote',owner:'alpha'},requests:[]};
  document.body.replaceChildren();
  const draft=document.createElement('textarea');draft.value='Keep this draft';draft.id='saved-draft';document.body.append(draft);
  const client={scope:()=>({...fixture.scope}),continueTurn:(body,scope)=>new Promise((resolve,reject)=>{fixture.requests.push({body,scope});fixture.resolve=resolve;fixture.reject=reject;})};
  const action=document.createElement('button');action.id='continuation-action';
  const render=()=>{const state=fixture.view.state();action.textContent=state.label;action.disabled=state.disabled;action.hidden=!state.available;};
  fixture.view=createTurnContinuation({client,locale:()=>fixture.locale,changed:render});action.onclick=()=>fixture.view.resume();document.body.append(fixture.view.root,action);
  fixture.snapshot={runtime_id:'runtime',owner_id:'remote/alpha',conversation_id:'alpha',cursor:'1',running:false,continuation:{user_message_id:'input',attempt_id:'original',recovery_required:true}};
  fixture.update=()=>{fixture.view.update(fixture.snapshot,{connected:!fixture.offline});render();};fixture.update();
 });
 await page.getByRole('button',{name:'Continue task',exact:true}).click();
 assert(await page.getByRole('button',{name:'Restoring progress…'}).isDisabled(),'Duplicate continuation clicks are disabled');
 await page.evaluate(()=>{continuationTest.locale='zh-CN';continuationTest.update();});
 assert(await page.getByRole('button',{name:'正在恢复进度…'}).isDisabled(),'Pending action uses the current locale');
 await page.evaluate(()=>{const error=Error('original operation result unavailable: effect');error.code='turn_recovery_required';continuationTest.reject(error);});
 await page.getByText('部分工具结果或进程停止状态尚未确认。Pot 已保留进度，请查看详情后重试。',{exact:true}).waitFor();
 assert(await page.locator('#saved-draft').inputValue()==='Keep this draft','Recovery failure preserves the composer draft');
 await page.getByRole('button',{name:'继续任务',exact:true}).click();
 assert(await page.evaluate(()=>continuationTest.requests[0].body.request_id===continuationTest.requests[1].body.request_id),'Acknowledgment retries retain the same continuation identity');
 assert(await page.evaluate(()=>continuationTest.requests[0].scope.space==='remote'&&continuationTest.requests[0].scope.owner==='alpha'&&continuationTest.requests[0].body.user_message_id==='input'),'Continuation binds the original owner, Space and input');
 await page.evaluate(()=>{continuationTest.scope={space:'local',owner:'beta'};continuationTest.view.update({...continuationTest.snapshot,owner_id:'local/beta',continuation:null},{connected:true});continuationTest.reject(Error('late failure'));});
 assert(!await page.locator('.turn-continuation-error').isVisible(),'A late failure cannot appear in another assistant');
 await page.evaluate(()=>{continuationTest.scope={space:'remote',owner:'alpha'};continuationTest.update();});
 await page.getByRole('button',{name:'继续任务',exact:true}).click();
 await page.evaluate(()=>continuationTest.resolve({message_id:'input',duplicate:true}));
 await page.locator('.turn-continuation-error').waitFor({state:'hidden'});
 await page.evaluate(()=>{continuationTest.snapshot.cursor='2';continuationTest.snapshot.continuation.attempt_id='later-interruption';continuationTest.update();});
 await page.getByRole('button',{name:'继续任务',exact:true}).click();
 assert(await page.evaluate(()=>continuationTest.requests[3].body.request_id!==continuationTest.requests[0].body.request_id),'A later interruption gets a new continuation identity');
 await page.evaluate(()=>{continuationTest.reject(Error('network lost'));});
 await page.getByText('暂时无法继续任务，已保存的进度和草稿均保留，请重试。',{exact:true}).waitFor();
 await page.evaluate(()=>{continuationTest.locale='en';continuationTest.offline=true;continuationTest.update();});
 assert(await page.getByRole('button',{name:'Continue task',exact:true}).isDisabled(),'Offline conversations cannot submit continuation');
 await page.getByText('Could not continue the task. Your saved progress and draft are kept. Try again.',{exact:true}).waitFor();
 await page.evaluate(()=>{continuationTest.snapshot.continuation.summary=true;continuationTest.offline=false;continuationTest.update();});
 assert(await page.locator('.turn-continuation-error button').count()===0,'Recovery errors do not add an independent action');
 assert(await page.getByRole('button',{name:'Restore conversation',exact:true}).isEnabled(),'Failed task-result summaries expose their own recovery action');
 await page.getByRole('button',{name:'Restore conversation',exact:true}).click();
 await page.evaluate(()=>continuationTest.resolve({message_id:'wrong-input',duplicate:false}));
 await page.getByText('Could not continue the task. Your saved progress and draft are kept. Try again.',{exact:true}).waitFor();
 assert(await page.getByRole('button',{name:'Restore conversation',exact:true}).isEnabled(),'A mismatched receipt is rejected and remains retryable');
 await page.getByRole('button',{name:'Restore conversation',exact:true}).click();
 await page.evaluate(()=>continuationTest.resolve({message_id:'input',duplicate:'invalid'}));
 await page.getByText('Could not continue the task. Your saved progress and draft are kept. Try again.',{exact:true}).waitFor();
 await page.getByRole('button',{name:'Restore conversation',exact:true}).click();
 await page.evaluate(()=>continuationTest.resolve({message_id:'input',duplicate:false}));
 await page.locator('#continuation-action').waitFor({state:'hidden'});
 return {passed:true,owner_fencing:true,draft_preserved:true,retry_identity:true,live_locale:true};
}

)(page);
 page.off('pageerror',onError);assert(errors.length===0,errors.join('\n'));
 return {passed:true,same_bubble:true,one_failure_style:true,partial_text_preserved:true,send_retry_identity:true,unknown_send_not_replayed:true,owner_fencing:true,reduced_motion:true,errors};
}
