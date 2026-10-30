// Exercises real first-use services and inspects the durable transcript; the
// Python loopback provider separately asserts the welcome in the HTTP payload.
async page=>{
 const state=STATE,assert=(value,message)=>{if(!value)throw Error(message)},errors=[];
 const onError=error=>errors.push(error.message);page.on('pageerror',onError);
const wsTrace=[];const rec=e=>{wsTrace.push(e);if(wsTrace.length>50)wsTrace.shift()};const consoleErrors=[];const onWebSocket=ws=>{rec({event:'open',pathname:(()=>{try{return new URL(ws.url()).pathname}catch{return null}})()});ws.on('framesent',p=>{try{const f=JSON.parse(p.payload);const o={event:'sent',type:f.type,id:f.id,method:f.method,path:f.path};rec(o)}catch{}});ws.on('framereceived',p=>{try{const f=JSON.parse(p.payload);const o={event:'received',type:f.type,id:f.id,status:f.status};if(f.error&&f.error.message)o.error=f.error.message;rec(o)}catch{}});ws.on('close',()=>rec({event:'close'}))};page.on('websocket',onWebSocket);const onConsole=m=>{if(m.type()==='error'){consoleErrors.push(m.text());if(consoleErrors.length>10)consoleErrors.shift()}};page.on('console',onConsole);
 const request=(method,path,body={})=>page.evaluate(({method,path,body})=>new Promise((resolve,reject)=>{
  const socket=new WebSocket(location.origin.replace(/^http/,'ws')+'/api/v1/ws','pot.v1');
  let settled=false;
  const finish=(callback,value)=>{if(settled)return;settled=true;clearTimeout(timer);try{socket.close();}catch(error){}callback(value);};
  const timer=setTimeout(()=>finish(reject,Error('WebSocket probe timed out after 10s: '+method+' '+path)),10000);
  socket.addEventListener('open',()=>{socket.send(JSON.stringify({type:'request',id:'probe',method,path,body}));});
  socket.addEventListener('message',event=>{
   let message;
   try{message=JSON.parse(event.data);}catch(error){return;}
   if(message?.type!=='response'||message.id!=='probe')return;
   if(message.status>=200&&message.status<300){finish(resolve,message.result);}
   else{finish(reject,Error(message.error?.message||('WebSocket probe failed: '+method+' '+path+' -> '+message.status)));}
  });
  socket.addEventListener('error',()=>finish(reject,Error('WebSocket probe failed: '+method+' '+path)));
  socket.addEventListener('close',()=>finish(reject,Error('WebSocket probe closed before response: '+method+' '+path)));
 }),{method,path,body});
 try{
  await page.setViewportSize({width:1200,height:820});await page.goto(state.origin+'/?theme=light&locale=zh-CN');
  await page.locator('#app.access-setup input[type="password"]').waitFor({ state: 'visible' });
  await page.locator('#app.access-setup input[type="password"]').fill(state.token);
  await page.locator('#app.access-setup button').click();
  await page.locator('.assistant-runtime-choice').first().waitFor();
  assert((await request('GET','/api/v1/assistants')).length===0,'No assistant before Provider setup');
  await page.locator('.assistant-runtime-choice').first().click();await page.getByRole('button',{name:'Custom API',exact:true}).click();
  await page.getByLabel('名称',{exact:true}).fill('Onboarding provider');await page.getByLabel('API 地址',{exact:true}).fill(state.endpoint);await page.getByLabel('请求格式',{exact:true}).selectOption('openai_chat');await page.getByLabel('API 密钥',{exact:true}).fill('isolated-key');await page.getByLabel('模型 ID',{exact:true}).fill('isolated-model');await page.getByRole('button',{name:'保存',exact:true}).click();
  await page.getByRole('button',{name:'创建助手',exact:true}).click();
  try{await page.locator('.window-title .avatar-image').waitFor();}catch(error){throw Error(JSON.stringify({title:await page.locator('.window-title').textContent(),headings:await page.locator('h1,h2').allTextContents(),errors:await page.locator('.inline-error').allTextContents(),assistants:await request('GET','/api/v1/assistants')}));}await page.waitForFunction(()=>!document.querySelector('#pot-composer')?.disabled);
  await page.locator('.assistant-start').waitFor({state:'detached'});assert(await page.locator('.assistant-start').count()===0,'Setup CTA disappears after connection');
  let assistants=await request('GET','/api/v1/assistants');assert(assistants.length===1&&/^[a-z]+-[a-z]+$/.test(assistants[0].id)&&assistants[0].name===assistants[0].id.split('-').map(word=>word[0].toUpperCase()+word.slice(1)).join(' '),'Explicit local creation creates exactly one assistant with a shared slug ID and initial name');const pot=assistants[0];
  await page.screenshot({path:state.screenshot});
  const frame=await request('GET','/api/v1/snapshot?assistant_id='+pot.id),snapshot={messages:frame.view.messages.map(value=>value.message)};assert(snapshot.messages.length===1&&snapshot.messages[0].role==='assistant'&&snapshot.messages[0].text.startsWith('你好，我是 '+pot.name+'。'),'Welcome persisted as assistant message');assert(pot.reply_count===0,'Welcome creates no unread notification');
  await Promise.all(['en','zh-CN'].map(locale=>request('POST','/api/v1/assistants/bootstrap',{locale})));
  await page.goto(state.origin+'/?theme=light&locale=en');await page.locator('.assistant-row').filter({hasText:pot.name}).click();await page.getByText('你好，我是 '+pot.name+'。',{exact:true}).waitFor();
  assert((await request('GET','/api/v1/snapshot?assistant_id='+pot.id)).view.messages.length===1,'Reload and locale changes preserve one original welcome');
  await page.locator('.connection-status.is-connected').waitFor({state:'attached'});await page.locator('#pot-composer').fill('以后叫你小白');await page.getByRole('button',{name:'Send',exact:true}).click();try{await page.locator('.window-title').filter({hasText:'小白'}).waitFor();}catch(reason){const failed=await request('GET','/api/v1/snapshot?assistant_id='+pot.id);throw Error(JSON.stringify(failed));}await page.locator('.assistant-row').filter({hasText:'小白'}).waitFor();
  await page.waitForFunction(()=>document.querySelectorAll('.role-assistant').length>=2&&!document.querySelector('.activity'));
  await page.getByRole('button',{name:'Assistant settings',exact:true}).click();assert(await page.getByLabel('Name',{exact:true}).inputValue()==='小白','Chat rename reaches settings');await page.getByRole('button',{name:'Cancel',exact:true}).click();
  assistants=await request('GET','/api/v1/assistants');assert(assistants.length===1&&assistants[0].name==='小白','Concurrent bootstrap and chat rename preserve one assistant');
  await page.screenshot({path:state.avatar_before});
  await page.locator('#pot-composer').fill('把头像换为测试图片');await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.waitForFunction(()=>document.querySelectorAll('.avatar-image').length>=2&&[...document.querySelectorAll('.avatar-image')].every(image=>image.complete&&image.naturalWidth===128)&&!document.querySelector('.activity'));
  await page.getByRole('button',{name:'Send',exact:true}).waitFor();
  assistants=await request('GET','/api/v1/assistants');assert(assistants[0].name==='小白'&&assistants[0].avatar.startsWith('data:image/png;base64,'),'Avatar-only update preserves name and returns normalized image');
  await page.screenshot({path:state.avatar_after});
  await page.reload();await page.waitForFunction(()=>document.querySelector('.window-title .avatar-image')?.naturalWidth===128);
  await page.getByRole('button',{name:'Assistant settings',exact:true}).click();await page.locator('.editor-fields>.avatar>.avatar-image').waitFor();assert(await page.locator('.editor-fields>.avatar>.avatar-image').count()===1,'Persisted avatar reaches settings after reload');await page.getByRole('button',{name:'Cancel',exact:true}).click();
  await page.goto(state.origin+'/?theme=light&locale=zh-CN');await page.locator('.window-title .avatar-image').waitFor();const g = page.locator('.history[role="log"]:not([data-preparation-surface]) .work-record-group').first(); await g.waitFor(); const s = g.locator(':scope > .work-record-prefix > .work-record-summary'); if ((await s.getAttribute('aria-expanded')) !== 'true') { await s.click(); } await g.locator('.tool-title').first().waitFor();
  await page.getByRole('button',{name:'助手设置',exact:true}).click();await page.locator('.editor-fields>.avatar>.avatar-image').waitFor();await page.getByRole('button',{name:'取消',exact:true}).click();
  assert(errors.length===0,errors.join('\n'));return {first_provider_to_chat:true,localized_durable_welcome:true,concurrent_bootstrap:true,chat_rename:true,errors};
 }catch(error){const diagnostic=await page.evaluate(()=>({composer:{value:document.querySelector('#pot-composer')?.value,disabled:document.querySelector('#pot-composer')?.disabled},send:{disabled:document.querySelector('.send-button')?.disabled,title:document.querySelector('.send-button')?.title},status:[...document.querySelectorAll('[role=status],.connection-status,.inline-error,.chat-cache-status')].map(node=>node.textContent),title:document.querySelector('.window-title')?.textContent}));throw Error(error.message+'; '+JSON.stringify({diagnostic,errors,consoleErrors,wsTrace:Array.isArray(wsTrace)?wsTrace.slice(-12):wsTrace}),{cause:error});}finally{page.off('pageerror',onError);page.off('websocket',onWebSocket);page.off('console',onConsole);}
}
