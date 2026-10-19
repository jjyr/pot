// Exercises real first-use services and inspects the durable transcript; the
// Python loopback provider separately asserts the welcome in the HTTP payload.
async page=>{
 const state=STATE,assert=(value,message)=>{if(!value)throw Error(message)},errors=[];
 const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 try{
  await page.setViewportSize({width:1200,height:820});await page.goto(state.origin+'/?theme=light&locale=zh-CN');
  if(await page.getByLabel('Access token',{exact:true}).isVisible()){await page.getByLabel('Access token',{exact:true}).fill(state.token);await page.getByRole('button',{name:'Connect',exact:true}).click();}
  await page.getByRole('button',{name:'连接 AI 供应商',exact:true}).waitFor();
  assert((await(await page.request.get(state.origin+'/api/v1/assistants')).json()).length===0,'No assistant before Provider setup');
  await page.getByRole('button',{name:'连接 AI 供应商',exact:true}).click();await page.getByRole('button',{name:'自定义 API',exact:true}).click();
  await page.getByLabel('名称',{exact:true}).fill('Onboarding provider');await page.getByLabel('API 地址',{exact:true}).fill(state.endpoint);await page.getByLabel('请求格式',{exact:true}).selectOption('openai_chat');await page.getByLabel('API 密钥',{exact:true}).fill('isolated-key');await page.getByLabel('模型 ID',{exact:true}).fill('isolated-model');await page.getByRole('button',{name:'保存',exact:true}).click();
  await page.getByText('你好，我是 Pot。',{exact:true}).waitFor();await page.waitForFunction(()=>!document.querySelector('#pot-composer')?.disabled);
  assert(await page.getByRole('button',{name:'连接 AI 供应商',exact:true}).count()===0,'Setup CTA disappears after connection');
  let assistants=await(await page.request.get(state.origin+'/api/v1/assistants')).json();assert(assistants.length===1&&assistants[0].name==='Pot','First Provider creates exactly one Pot');const pot=assistants[0];
  await page.screenshot({path:state.screenshot});
  const frame=await(await page.request.get(state.origin+'/api/v1/snapshot?assistant_id='+pot.id)).json(),snapshot={messages:frame.view.messages.map(value=>value.message)};assert(snapshot.messages.length===1&&snapshot.messages[0].role==='assistant'&&snapshot.messages[0].text.startsWith('你好，我是 Pot。'),'Welcome persisted as assistant message');assert(pot.reply_count===0,'Welcome creates no unread notification');
  await Promise.all(['en','zh-CN'].map(locale=>page.request.post(state.origin+'/api/v1/assistants/bootstrap',{data:{locale}})));
  await page.goto(state.origin+'/?theme=light&locale=en');await page.locator('.assistant-row').filter({hasText:'Pot'}).click();await page.getByText('你好，我是 Pot。',{exact:true}).waitFor();
  assert((await(await page.request.get(state.origin+'/api/v1/snapshot?assistant_id='+pot.id)).json()).view.messages.length===1,'Reload and locale changes preserve one original welcome');
  await page.locator('#pot-composer').fill('以后叫你小白');await page.getByRole('button',{name:'Send',exact:true}).click();try{await page.locator('.window-title').filter({hasText:'小白'}).waitFor();}catch(reason){const failed=await(await page.request.get(state.origin+'/api/v1/snapshot?assistant_id='+pot.id)).json();throw Error(JSON.stringify(failed));}await page.locator('.assistant-row').filter({hasText:'小白'}).waitFor();
  await page.waitForFunction(()=>document.querySelectorAll('.role-assistant').length>=2&&!document.querySelector('.activity'));
  await page.getByRole('button',{name:'Assistant settings',exact:true}).click();assert(await page.getByLabel('Name',{exact:true}).inputValue()==='小白','Chat rename reaches settings');await page.getByRole('button',{name:'Cancel',exact:true}).click();
  assistants=await(await page.request.get(state.origin+'/api/v1/assistants')).json();assert(assistants.length===1&&assistants[0].name==='小白','Concurrent bootstrap and chat rename preserve one assistant');
  await page.screenshot({path:state.avatar_before});
  await page.locator('#pot-composer').fill('把头像换为测试图片');await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.waitForFunction(()=>document.querySelectorAll('.avatar-image').length>=2&&[...document.querySelectorAll('.avatar-image')].every(image=>image.complete&&image.naturalWidth===128)&&!document.querySelector('.activity'));
  await page.getByRole('button',{name:'Send',exact:true}).waitFor();
  assistants=await(await page.request.get(state.origin+'/api/v1/assistants')).json();assert(assistants[0].name==='小白'&&assistants[0].avatar.startsWith('data:image/png;base64,'),'Avatar-only update preserves name and returns normalized image');
  await page.screenshot({path:state.avatar_after});
  await page.reload();await page.waitForFunction(()=>document.querySelector('.window-title .avatar-image')?.naturalWidth===128);
  await page.getByRole('button',{name:'Assistant settings',exact:true}).click();await page.locator('.editor-fields>.avatar>.avatar-image').waitFor();assert(await page.locator('.editor-fields>.avatar>.avatar-image').count()===1,'Persisted avatar reaches settings after reload');await page.getByRole('button',{name:'Cancel',exact:true}).click();
  await page.goto(state.origin+'/?theme=light&locale=zh-CN');await page.locator('.window-title .avatar-image').waitFor();await page.locator('.tool-title').filter({hasText:'更新助手资料'}).first().waitFor();
  await page.getByRole('button',{name:'助手设置',exact:true}).click();await page.locator('.editor-fields>.avatar>.avatar-image').waitFor();await page.getByRole('button',{name:'取消',exact:true}).click();
  assert(errors.length===0,errors.join('\n'));return {first_provider_to_chat:true,localized_durable_welcome:true,concurrent_bootstrap:true,chat_rename:true,errors};
 }finally{page.off('pageerror',onError);}
}
