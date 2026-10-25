// Isolated real HTTP/browser gateway and encrypted Agent Link. Native CLIs are mocks.
// The fixture clipboard supplies real disposable invitations; no routes are intercepted.
async page=>{
 const state=STATE,zh=state.kind==='antigravity',checks=[],assert=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
 await page.setViewportSize({width:1200,height:900});
 await page.goto(state.origin+'/scripts/tests/fixtures/first-connection-live.html?locale='+(zh?'zh-CN':'en'));
 await page.locator('.assistant-connect-new').waitFor();
 const initial=await(await page.request.get(state.origin+'/api/v1/agent-links')).json(),before=await(await page.request.get(state.origin+'/fixture/state')).json();assert(initial.items.length===0,'Real Control begins with zero connections');
 if(state.intent==='manage'){
  await page.getByRole('button',{name:zh?'连接':'Link',exact:true}).click();await page.getByRole('button',{name:zh?'添加 Agent':'Add Agent',exact:true}).click();
 }else{
  await page.locator('.assistant-connect-new').click();await page.locator('[data-agent-kind='+state.kind+']').click();
  assert((await page.locator('.agent-setup-steps').innerText()).includes('pot-setup'),'Real browser exposes installation and invitation steps');
 }
 await page.locator('.connection-input-group button').click();
 if(state.intent==='manage'){
  await page.locator('.agent-connection-setup').waitFor({state:'detached'});await page.locator('.topology-page').waitFor();
  assert(await page.locator('.assistant-management-form').count()===0,'Connection management returns to Link without creating an assistant');
 }else{
  await page.locator('.assistant-management-form input').waitFor();
  assert(await page.evaluate(()=>!firstConnectionLive.client.scope().owner),'Real pairing does not open the advertised default assistant');
  const paired=await(await page.request.get(state.origin+'/api/v1/agent-links')).json();assert(paired.items.length===1&&paired.items[0].peer.kind===state.kind,'Actual signed pairing discovers the selected native adapter');
  assert((await page.locator('.assistant-runtime-summary').innerText()).includes(state.kind==='codex'?'Codex':'Antigravity'),'Actual connection is preselected');
  await page.locator('.assistant-management-form input').fill('First '+state.kind+' review');await page.locator('.editor-actions .primary').click();await page.locator('.window-title').filter({hasText:'First '+state.kind+' review'}).waitFor();
  const created=await(await page.request.get(state.origin+'/fixture/state')).json(),newID=created.assistant_ids.find(id=>!before.assistant_ids.includes(id));
  assert(created.assistant_ids.length===before.assistant_ids.length+1&&!!newID,'Actual receiver persists a new independent assistant');
  assert(await page.evaluate(id=>firstConnectionLive.client.scope().owner.endsWith(':'+id),newID),'Actual response identity is opened');
  await page.getByRole('button',{name:zh?'添加助手':'Add assistant',exact:true}).click();await page.locator('[data-runtime-id="connection:'+paired.items[0].id+'"] .assistant-runtime-choice').click();await page.locator('.assistant-management-form input').fill('Second '+state.kind+' review');await page.locator('.editor-actions .primary').click();await page.locator('.window-title').filter({hasText:'Second '+state.kind+' review'}).waitFor();
  assert((await(await page.request.get(state.origin+'/api/v1/agent-links')).json()).items.length===1,'Second creation reuses the actual connection');
 }
 const after=await(await page.request.get(state.origin+'/fixture/state')).json();assert(after.jobs===0&&after.executor_model===''&&after.executor_effort==='','No native model execution or independent executor configuration');
 if(state.intent==='manage')assert(after.assistant_ids.length===before.assistant_ids.length,'Pure connection leaves receiver assistants unchanged');
 return {passed:true,checks,kind:state.kind,intent:state.intent,transport:'real HTTP and authenticated Agent Link',nativeCLI:'mock metadata only',paidModelCalls:0};
}
