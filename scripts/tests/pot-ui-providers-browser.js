// Real editor DOM with delayed discovery; address resolution and the explicit
// inference test use the isolated local Go Control and deterministic model.
async page=>{
 const state=STATE,assert=(value,message)=>{if(!value)throw Error(message);},errors=[];
 const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 try{
  await page.setViewportSize({width:1200,height:900});await page.goto(state.origin+'/?theme=light&locale=zh-CN');
  await page.waitForFunction(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
  await page.getByRole('button',{name:'设置',exact:true}).click();await page.getByRole('button',{name:'AI 提供商',exact:true}).waitFor();
  await page.evaluate(async()=>{
   const {createPages}=await import('/components/pages.mjs');
   const f=window.providerFixture={checks:[],tests:[],saves:[],snapshot:{profiles:[{id:'existing',name:'自定义供应商'}],directory:[],provider_types:[{id:'standard-model-providers/custom',plugin_id:'standard-model-providers',name:'自定义供应商',available:true,protocol:'openai_responses',protocols:['openai_responses','openai_chat','anthropic_messages','gemini'],capabilities:{catalog:'http'}}]}};
   f.catalog=ids=>({supported:true,catalog:{read_at:new Date().toISOString(),models:ids.map(id=>({id}))}});
   const client={runtime:{boot_id:'fixture'},assistants:[{id:'fixture'}],scope:()=>({space:'local',owner:''}),request:async(path,options={})=>{
    if(path==='/api/v1/providers'){if(options.method==='POST'){f.saves.push(options.body);throw Error('Save deliberately rejected');}return f.snapshot;}
    if(path==='/api/v1/about')return {};
    if(path==='/api/v1/providers/check')return new Promise((resolve,reject)=>f.checks.push({body:options.body,resolve,reject}));
    if(path==='/api/v1/providers/test')f.tests.push(options.body);
    const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(options.body),signal:options.signal});
    const result=await response.json();if(!response.ok)throw Error(result.message||JSON.stringify(result));return result;
   }};
   f.pages=createPages({client,chatVisible:()=>false,host:document.querySelector('.workspace > .page-host'),approvalHost:document.createElement('div'),chatApprovalHost:document.createElement('section'),locale:()=> 'zh-CN',notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});
   await f.pages.setupAI();
  });
  await page.getByRole('button',{name:'自定义供应商',exact:true}).click();
  assert(await page.getByLabel('名称',{exact:true}).inputValue()==='自定义供应商 2','Default name avoids existing names');
  await page.getByLabel('名称',{exact:true}).fill('自定义供应商');
  await page.locator('main').screenshot({path:state.desktopScreenshot});await page.setViewportSize({width:390,height:852});assert(await page.evaluate(()=>document.documentElement.scrollWidth===innerWidth),'Mobile editor has no horizontal overflow');await page.locator('main').screenshot({path:state.mobileScreenshot});await page.setViewportSize({width:1200,height:900});
  await page.getByLabel('名称',{exact:true}).fill('My gateway');
  await page.getByLabel('请求格式',{exact:true}).selectOption('openai_chat');await page.getByLabel('API 地址',{exact:true}).fill(state.base);await page.getByLabel('API 密钥',{exact:true}).fill('isolated-key');
  await page.waitForFunction(()=>providerFixture.checks.length===1);
  await page.getByLabel('模型 ID',{exact:true}).fill('typed-during-discovery');
  await page.evaluate(()=>providerFixture.checks[0].resolve(providerFixture.catalog(['discovered','typed-during-discovery'])));
  await page.getByRole('option',{name:'discovered',exact:true}).waitFor({state:'attached'});
  assert(await page.getByLabel('模型 ID',{exact:true}).isVisible()&&await page.getByLabel('模型 ID',{exact:true}).inputValue()==='typed-during-discovery','Late model discovery preserves visible manual input');
  assert(await page.getByLabel('名称',{exact:true}).inputValue()==='My gateway','Connection changes preserve edited name');
  assert((await page.locator('.provider-endpoint-preview').innerText()).includes('/v1/chat/completions'),'Backend previews normalized address');
  await page.getByLabel('API 地址',{exact:true}).fill(state.base+'/other/v1');await page.waitForFunction(()=>providerFixture.checks.length===2);
  await page.getByLabel('API 地址',{exact:true}).fill(state.base);await page.waitForFunction(()=>providerFixture.checks.length===3);
  await page.evaluate(()=>providerFixture.checks[2].resolve(providerFixture.catalog(['current-model'])));await page.getByRole('option',{name:'current-model',exact:true}).waitFor({state:'attached'});
  await page.evaluate(()=>providerFixture.checks[1].resolve(providerFixture.catalog(['stale-model'])));
  await page.waitForTimeout(50);assert(await page.getByRole('option',{name:'stale-model',exact:true}).count()===0,'Stale connection response is ignored');
  await page.getByLabel('API 密钥',{exact:true}).fill('isolated-key-2');await page.waitForFunction(()=>providerFixture.checks.length===4);
  await page.evaluate(()=>providerFixture.checks[3].reject(Error('HTTP 401')));await page.getByText('模型获取失败',{exact:true}).waitFor();
  assert(await page.getByLabel('模型 ID',{exact:true}).inputValue()==='typed-during-discovery','Discovery failure retains manual model');
  await page.getByRole('button',{name:'重试',exact:true}).click();await page.waitForFunction(()=>providerFixture.checks.length===5);
  await page.evaluate(()=>providerFixture.checks[4].resolve(providerFixture.catalog(['retry-model'])));await page.getByRole('option',{name:'retry-model',exact:true}).waitFor({state:'attached'});
  await page.getByLabel('API 密钥',{exact:true}).fill('isolated-key');await page.waitForFunction(()=>providerFixture.checks.length===6);await page.evaluate(()=>providerFixture.checks[5].resolve(providerFixture.catalog(['isolated-model'])));
  await page.getByLabel('模型 ID',{exact:true}).fill('isolated-model');await page.getByRole('button',{name:'测试连接',exact:true}).click();try{await page.getByText('连接成功',{exact:true}).waitFor();}catch(error){throw Error(error.message+'; '+await page.locator('.provider-test-row').innerText());}
  await page.locator('main').screenshot({path:state.connectedScreenshot});
  assert(await page.evaluate(()=>providerFixture.tests.length===1&&providerFixture.tests[0].model==='isolated-model'&&providerFixture.saves.length===0),'Explicit inference test uses unsaved model without saving');
  await page.getByLabel('API 地址',{exact:true}).fill(state.base+'/v1/responses');await page.getByRole('button',{name:'使用匹配格式',exact:true}).waitFor();
  assert(await page.getByRole('button',{name:'测试连接',exact:true}).isDisabled(),'Conflicting request format cannot be tested');
  await page.getByRole('button',{name:'使用匹配格式',exact:true}).click();assert(await page.getByLabel('请求格式',{exact:true}).inputValue()==='openai_responses','Mismatch offers explicit protocol correction');
  await page.getByLabel('API 地址',{exact:true}).fill(state.base);await page.getByLabel('请求格式',{exact:true}).selectOption('openai_chat');
  await page.waitForFunction(()=>document.querySelector('.provider-endpoint-preview')?.textContent.includes('/chat/completions'));
  await page.getByRole('button',{name:'保存',exact:true}).click();await page.getByText('Save deliberately rejected',{exact:true}).waitFor();
  assert(await page.evaluate(()=>providerFixture.saves[0].model==='isolated-model'&&providerFixture.saves[0].name==='My gateway'),'Save remains available while discovery is pending');
  const pending=await page.evaluate(()=>providerFixture.checks.length-1);await page.getByRole('button',{name:'取消',exact:true}).click();await page.getByRole('button',{name:'放弃更改',exact:true}).click();
  await page.evaluate(index=>providerFixture.checks[index].resolve(providerFixture.catalog(['after-leave'])),pending);assert(await page.getByRole('option',{name:'after-leave',exact:true}).count()===0,'Leaving disposes background discovery');
  await page.evaluate(()=>providerFixture.pages.dispose());
  assert(errors.length===0,errors.join('\n'));return {passed:true,default_name:true,async_model_discovery:true,preserves_manual_model:true,stale_results_ignored:true,real_endpoint_resolution:true,real_inference_test:true,save_during_discovery:true,errors};
 }finally{await page.evaluate(()=>window.providerFixture?.pages.dispose());page.off('pageerror',onError);}
}
