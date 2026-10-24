// Production reply rendering with receiver-confirmed failure envelopes.
async page=>{
 const assert=(value,message)=>{if(!value)throw Error(message);},errors=[];
 const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 await page.setViewportSize({width:1200,height:820});
 await page.evaluate(async()=>{
  const {createReplyBubble}=await import('/components/reply.mjs');
  const {projectChatReplies}=await import('/state/chat-replies.mjs');
  const {translate}=await import('/locales/messages.mjs');
  const root=document.createElement('main');root.style.cssText='max-width:840px;margin:80px auto;padding:24px';document.body.replaceChildren(root);
  const host=document.createElement('div');root.append(host);const bubble=createReplyBubble(host);
  const input={id:'request',role:'user',text:'Hello',status:'complete'};
  const fixture=window.outcomeFixture={root,host,bubble,locale:'zh-CN',input,project:projectChatReplies};
  fixture.render=(state='failed',partial='',error='codex_thread_open_failed',project=true)=>{
   const text=JSON.stringify({state,error,partial_output:partial});
   const message={id:'reply',reply_to:input.id,role:'assistant',status:'complete',text};
   const row=project?projectChatReplies({messages:[input,message],replies:[{input_id:input.id,state,error}]}).at(-1):{...message,phase:state,error};
   bubble.update(row,{t:key=>translate(fixture.locale,key)});
  };
 });
 for(const locale of ['en','zh-CN']){
  await page.evaluate(locale=>{outcomeFixture.locale=locale;outcomeFixture.render();},locale);
  await page.getByText(locale==='en'?'Reply failed':'回复失败',{exact:true}).waitFor();
  assert(!await page.locator('.reply-content').isVisible(),'Empty remote failures hide protocol JSON');
  await page.locator('.reply-error-details summary').click();
  assert(await page.locator('.reply-error-details pre').innerText()==='codex_thread_open_failed','Remote diagnostic remains in details');
  await page.evaluate(()=>outcomeFixture.render('failed','Partial answer','codex_thread_open_failed: session unavailable'));
  assert(await page.locator('.reply-content').innerText()==='Partial answer','Only actual partial prose is rendered');
  assert(await page.locator('.reply-error-details').evaluate(node=>node.open),'Diagnostic updates retain disclosure state');
  await page.evaluate(()=>outcomeFixture.render('cancelled','Partial answer',''));
  assert(await page.locator('.reply-content').innerText()==='Partial answer'&&await page.locator('.is-failed').count()===0,'Cancellation preserves prose with its stopped state');
 }
 for(const locale of ['en','zh-CN']){
  await page.evaluate(locale=>{outcomeFixture.locale=locale;outcomeFixture.render('failed','','codex_thread_in_use: codex_rpc_rejected_thread_resume (-32600): thread busy-thread already has an active writer');},locale);
  const summary=await page.locator('.reply-error-summary').innerText();
  assert(locale==='en'?summary.includes('Another Codex client on the Codex host')&&summary.includes('archive and restore')&&summary.includes('history is retained'):summary.includes('另一个客户端占用')&&summary.includes('临时归档再恢复')&&summary.includes('聊天记录会保留'),'Writer conflict explains the cause and recovery in the current locale');
  assert(!await page.locator('.reply-content').isVisible(),'Writer conflict keeps protocol JSON out of prose');
  if(!await page.locator('.reply-error-details').evaluate(node=>node.open))await page.locator('.reply-error-details summary').click();
  assert(await page.locator('.reply-error-details pre').innerText()==='codex_thread_in_use: codex_rpc_rejected_thread_resume (-32600): thread busy-thread already has an active writer','Writer diagnostics remain verbatim');
  assert(await page.locator('.reply-status').getAttribute('role')==='alert','Writer conflict remains an accessible error');
 }
 page.off('pageerror',onError);assert(errors.length===0,errors.join('\n'));
 return {passed:true,locales:['en','zh-CN'],empty_failure:true,partial_output:true,diagnostic_disclosure:true};
}
