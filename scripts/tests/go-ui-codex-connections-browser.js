// The Codex plugin uses the same node connection widget. All peer endpoints and
// clipboard values are synthetic; no Codex execution is launched by this test.
async parentPage=>{
 const assert=(value,message)=>{if(!value)throw Error(message);},origin=new URL(parentPage.url()).origin;
 const context=await parentPage.context().browser().newContext({viewport:{width:1280,height:720},serviceWorkers:'block'}),page=await context.newPage(),errors=[],calls=[];
 let releasePreview,releaseConnect,items=[];
 const address='tcp://cloud-pot.tailnet.ts.net:8793#synthetic-invitation';
 await context.addInitScript(()=>{localStorage.setItem('pot-go-preview-locale','en');window.fixtureClipboard='';Object.defineProperty(navigator,'clipboard',{value:{readText:async()=>window.fixtureClipboard,writeText:async value=>{window.fixtureClipboard=value;}}});});
 page.on('pageerror',error=>errors.push(error.message));
 await page.route('**/api/v1/agent-links**',async route=>{
  const request=route.request(),body=JSON.parse(request.postData()||'{}');calls.push({method:request.method(),body});
  if(body.action==='preview'){await new Promise(resolve=>releasePreview=resolve);return route.fulfill({json:{peer:{node_id:'cloud-node',kind:'pot',name:'Cloud Pot'},endpoint:'tcp://cloud-pot.tailnet.ts.net:8793',fingerprint:'SHA256:synthetic-fixture'}}).catch(()=>{});}
  if(body.action==='connect'){await new Promise(resolve=>releaseConnect=resolve);items=[{id:'cloud-link',peer:{node_id:'cloud-node',kind:'pot',name:'Cloud Pot'},status:'connected',revision:1}];return route.fulfill({json:items[0]});}
  if(body.action==='disconnect')items[0].status='disconnected';
  if(body.action==='reconnect')items[0].status='connected';
  if(body.action==='delete')items=[];
  if(new URL(request.url()).pathname.endsWith('/invitation'))return route.fulfill({json:{address:body.endpoint+'#synthetic-own-invitation'}});
  return route.fulfill({json:{self:{node_id:'codex-node',kind:'codex',name:'Codex',address:'',status:'needs_access'},addresses:[{kind:'tailscale',endpoint:'tcp://codex.tailnet.ts.net:8793',requires_setup:true},{kind:'public',interface:'en0',endpoint:'tcp://203.0.113.10:8793'},{kind:'private',interface:'en0',endpoint:'tcp://192.168.1.10:8793'},{kind:'loopback',endpoint:'tcp://127.0.0.1:8793'}],network:{listen:'127.0.0.1:8793'},items,unread:99,unconfirmed:99}});
 });
 try{
  await page.goto(origin+'/codex-connections.html?theme=light#synthetic-owner');
  const rows=page.locator('.agent-link-address-row');
  assert(!await rows.first().isVisible(),'Address choices are hidden by default');
  await page.locator('.agent-link-choose').click();await rows.first().waitFor();
  assert(await rows.count()===4,'Discovery shows Tailscale, public, LAN and local addresses');
  assert((await rows.first().innerText()).includes('Tailscale'),'Tailscale is first');
  assert(!await page.getByRole('button',{name:'Configure access',exact:true}).count(),'Discovery needs no separate access configuration');
  assert(!calls.some(call=>call.method==='POST'),'Discovery does not configure access or create invitations');
  for(const endpoint of ['tcp://codex.tailnet.ts.net:8793','tcp://203.0.113.10:8793','tcp://192.168.1.10:8793','tcp://127.0.0.1:8793']){
   const row=rows.filter({hasText:endpoint});await row.getByRole('button',{name:'Copy connection address',exact:true}).click();await row.getByRole('button',{name:'Copied',exact:true}).waitFor();
   assert(await page.evaluate(()=>fixtureClipboard)===endpoint+'#synthetic-own-invitation','Each copied invitation uses the selected address');
  }
  assert(!calls.find(call=>call.method==='POST').body.assistant_id,'Invitations are not bound to a local assistant');
  assert(!await page.locator('.agent-link-recovery,.agent-link-unread').count(),'Manual message consumer recovery is removed');
  await page.getByRole('button',{name:'Add Agent',exact:true}).click();const editor=page.locator('.agent-connection-setup');
  assert(!await editor.locator('select').count(),'Node setup has no assistant-binding selector');
  await page.evaluate(value=>{window.fixtureClipboard=value;},address);await editor.getByRole('button',{name:'Paste',exact:true}).click();
  await page.getByText('Verifying the Agent identity…',{exact:true}).waitFor();assert(!calls.some(call=>call.body.action==='connect'),'Paste verifies before connecting');
  await page.evaluate(()=>{localStorage.setItem('pot-go-preview-locale','zh-CN');window.dispatchEvent(new Event('pot:locale'));});
  await page.getByText('正在验证对方身份…',{exact:true}).waitFor();releasePreview();
  await page.waitForFunction(()=>document.querySelector('.agent-connection-setup input')?.disabled);
  assert(calls.filter(call=>call.body.action==='connect').length===1,'Verified paste connects automatically once');
  assert(!calls.find(call=>call.body.action==='connect').body.assistant_id,'Connection is independent of the default assistant');
  await editor.locator('form').evaluate(form=>{form.requestSubmit();form.requestSubmit();});assert(calls.filter(call=>call.body.action==='connect').length===1,'Repeated submission does not duplicate the connection');
  releaseConnect();await editor.waitFor({state:'detached'});await page.locator('.agent-link-row').getByText('Cloud Pot',{exact:true}).waitFor();
  await page.locator('.agent-link-row').getByRole('button',{name:'断开连接',exact:true}).click();await page.getByRole('button',{name:'重新连接',exact:true}).waitFor();
  await page.getByRole('button',{name:'重新连接',exact:true}).click();await page.locator('.agent-link-row').getByRole('button',{name:'断开连接',exact:true}).waitFor();
  await page.locator('.agent-link-row').getByRole('button',{name:'删除连接',exact:true}).click();assert(!calls.some(call=>call.body.action==='delete'),'Delete requires confirmation');
  await page.getByRole('dialog').getByRole('button',{name:'删除连接',exact:true}).click();await page.getByText('尚未连接 Agent',{exact:true}).waitFor();
  await page.setViewportSize({width:393,height:500});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Discovered addresses fit a narrow viewport');
  await page.locator('.agent-link-choose').click();await page.getByText('本机',{exact:true}).waitFor();await page.keyboard.press('Escape');await page.getByRole('button',{name:'添加 Agent',exact:true}).click();assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Standalone editor fits a narrow viewport');
  await page.evaluate(()=>window.dispatchEvent(new Event('pagehide')));const count=calls.length;await page.waitForTimeout(3300);assert(calls.length===count,'Closing stops local background refresh');
  assert(!errors.length,'No browser exceptions: '+errors.join('; '));return {passed:true,nodeInvitation:true,automaticConnect:true,localeDuringValidation:true,lifecycle:true,narrowWidth:393};
 }finally{releasePreview?.();releaseConnect?.();await context.close();}
}
