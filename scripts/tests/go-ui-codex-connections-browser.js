// Serve web/src on loopback and open codex-connections.html first. The actual
// standalone entry point runs here; all peer replies and clipboard data are synthetic.
async parentPage => {
 const assert=(value,message)=>{if(!value)throw Error(message);},origin=new URL(parentPage.url()).origin;
 const context=await parentPage.context().browser().newContext({viewport:{width:1280,height:720},serviceWorkers:'block'}),page=await context.newPage(),errors=[],calls=[];
 let releasePreview,releaseConnect,items=[];
 const address='tcp://cloud-pot.tailnet.ts.net:8793#synthetic-invitation';
 await context.addInitScript(()=>{
  localStorage.setItem('pot-go-preview-locale','en');
  window.fixtureClipboard='tcp://cloud-pot.tailnet.ts.net:8793#synthetic-invitation';
  Object.defineProperty(navigator,'clipboard',{value:{readText:async()=>window.fixtureClipboard,writeText:async value=>{window.fixtureClipboard=value;}}});
 });
 page.on('pageerror',error=>errors.push(error.message));
 await page.route('**/api/v1/agent-links**',async route=>{
  const request=route.request(),body=JSON.parse(request.postData()||'{}');calls.push({method:request.method(),body});
  if(body.action==='preview'){
   await new Promise(resolve=>{releasePreview=resolve;});
   return route.fulfill({json:{peer:{id:'pot',node_id:'cloud-node',kind:'pot',name:'Cloud Pot'},endpoint:'tcp://cloud-pot.tailnet.ts.net:8793',fingerprint:'SHA256:synthetic-fixture',expires_at_ms:Date.now()+3600000}}).catch(()=>{});
  }
  if(body.action==='connect'){
   await new Promise(resolve=>{releaseConnect=resolve;});
   items=[{id:'cloud-link',peer:{id:'pot',kind:'pot',name:'Cloud Pot'},status:'connected',revision:1}];return route.fulfill({json:items[0]});
  }
  if(new URL(request.url()).pathname.endsWith('/invitation'))return route.fulfill({json:{address:'tcp://codex.tailnet.ts.net:8793#synthetic-own-invitation'}});
  return route.fulfill({json:{self:{id:'codex',kind:'codex',name:'Codex',address:'tcp://codex.tailnet.ts.net:8793',status:'ready'},network:{listen:'127.0.0.1:8793'},items}});
 });
 try{
  await page.goto(origin+'/codex-connections.html?theme=light#synthetic-owner');
  await page.getByRole('button',{name:'Copy connection address',exact:true}).click();
  await page.getByRole('button',{name:'Copied',exact:true}).waitFor();
  assert(await page.evaluate(()=>fixtureClipboard)==='tcp://codex.tailnet.ts.net:8793#synthetic-own-invitation','Own address stays available through Copy address');
  await page.getByRole('button',{name:'Add Agent',exact:true}).click();
  const editor=page.locator('.agent-connection-setup'),add=editor.getByRole('button',{name:'Add Agent',exact:true});
  assert(await add.isDisabled(),'Cannot connect before live identity verification');
  assert(await editor.locator('select').count()===0,'Codex is the fixed local messaging Agent');
  assert(await editor.locator('.connection-target-value').innerText()==='Codex','The fixed destination is visible');
  await page.evaluate(value=>{window.fixtureClipboard=value;},address);
  await editor.getByRole('button',{name:'Paste',exact:true}).click();
  await page.getByText('Verifying the Agent identity…',{exact:true}).waitFor();
  assert(calls.filter(call=>call.body.action==='connect').length===0,'Paste only previews; it does not connect');
  const aborted=page.waitForEvent('requestfailed',{predicate:request=>request.postData()?.includes('"action":"preview"')});
  await editor.getByLabel('Other Agent address',{exact:true}).fill(address+'-replacement');
  await aborted;releasePreview();
  const nextPreview=page.waitForRequest(request=>request.postData()?.includes('"action":"preview"'));
  await editor.getByLabel('Other Agent address',{exact:true}).fill(address);await nextPreview;
  await page.evaluate(()=>{localStorage.setItem('pot-go-preview-locale','zh-CN');window.dispatchEvent(new Event('pot:locale'));});
  await page.getByText('正在验证对方身份…',{exact:true}).waitFor();releasePreview();
  await page.getByText('身份已验证',{exact:true}).waitFor();
  assert(await page.locator('html').getAttribute('lang')==='zh-CN','The standalone document language follows the active locale');
  assert(await page.getByLabel('对方连接地址',{exact:true}).inputValue()===address,'Async locale refresh retains the pasted address');
  assert(await editor.locator('details[open]').count()===0,'Endpoint and fingerprint details start collapsed');
  assert(!await editor.getByText('SHA256:synthetic-fixture',{exact:true}).isVisible(),'Fingerprint does not dominate the flow');
  await page.screenshot({path:'docs/pr-images/codex-topology-codex-host/codex-add-agent-after.png'});
  await editor.getByRole('button',{name:'添加 Agent',exact:true}).click();
  await editor.getByRole('button',{name:'正在连接…',exact:true}).waitFor();
  await editor.locator('form').evaluate(form=>{form.requestSubmit();form.requestSubmit();});
  assert(calls.filter(call=>call.body.action==='connect').length===1,'Repeated submission does not add twice');
  assert(calls.find(call=>call.body.action==='connect').body.assistant_id==='codex','Connect stays bound to Codex');
  releaseConnect();await editor.waitFor({state:'detached'});
  await page.getByRole('status').filter({hasText:'Agent 已添加，双方助手现在可以互发消息。'}).waitFor();
  await page.locator('.agent-link-row').getByText('Cloud Pot',{exact:true}).waitFor();
  await page.setViewportSize({width:393,height:500});await page.getByRole('button',{name:'添加 Agent',exact:true}).click();
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Standalone editor fits a narrow viewport');
  assert(await page.locator('#connections').evaluate(root=>getComputedStyle(root).overflowY==='auto'),'Standalone host owns vertical scrolling');
  await page.evaluate(()=>window.dispatchEvent(new Event('pagehide')));const stoppedCalls=calls.length;
  await page.waitForTimeout(3300);assert(calls.length===stoppedCalls,'Pagehide stops background refresh');
  assert(errors.length===0,'No browser exceptions: '+errors.join('; '));
  return {passed:true,fixedCodexIdentity:true,pastePreview:true,stalePreviewAborted:true,previewBeforeConnect:true,oneSubmission:true,localeDuringPreview:true,collapsedDetails:true,successFeedback:true,narrowWidth:393,pagehideCleanup:true};
 }catch(error){throw Error(error.message+'\n'+await page.locator('body').textContent()+'\n'+JSON.stringify(calls));}
 finally{releasePreview?.();releaseConnect?.();await context.close();}
}
