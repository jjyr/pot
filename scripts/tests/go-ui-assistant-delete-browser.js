// Run against the disposable TestHistoryBrowserFixture; real HTTP/persistence,
// no user data or model requests. Start on assistant-avatar.html?app=1.
async page => {
  const assert=(value,message)=>{if(!value)throw Error(message);};
  const checks=[];
  const check=(value,message)=>{assert(value,message);checks.push(message);};
  const locale=async value=>page.evaluate(value=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value}})),value);
  await page.emulateMedia({reducedMotion:'reduce'});
  const origin=new URL(page.url()).origin;
  await page.setViewportSize({width:1200,height:900});
  assert(new URL(page.url()).pathname==='/scripts/tests/fixtures/assistant-avatar.html','Use only the disposable fixture');
  let initial=await(await page.request.get(origin+'/api/v1/assistants')).json();
  if(initial.length!==1||initial[0].name!=='History fixture'){
    for(const item of initial){const response=await page.request.post(origin+'/api/v1/assistants/'+item.id+'/delete',{data:{revision:item.revision}});assert(response.ok(),'Fixture cleanup failed');}
    await page.request.post(origin+'/api/v1/assistants',{data:{request_id:'delete-regression-'+Date.now(),name:'History fixture'}});
    await page.reload();initial=await(await page.request.get(origin+'/api/v1/assistants')).json();
  }
  await locale('zh-CN');
  if(!await page.locator('.editor-form').count())await page.getByRole('button',{name:'助手设置',exact:true}).click();
  const originalID=initial[0].id;
  const more=page.locator('.assistant-delete'),remove=more.locator('button'),dialog=page.locator('dialog[open]');
  const name=page.locator('.editor-form input[required]').first();
  await name.fill('Unsaved draft');
  check(await more.count()===1,'Existing assistant exposes bottom disclosure');
  await more.evaluate(node=>node.open=false);
  check(!await remove.isVisible(),'Deletion button is hidden until expanded');
  await more.locator('summary').click();
  await page.screenshot({path:'output/playwright/assistant-delete-desktop-after.png'});
  await remove.click();
  await dialog.waitFor();
  check((await dialog.innerText()).includes('History fixture'),'Confirmation uses saved assistant identity');
  await page.screenshot({path:'output/playwright/assistant-delete-confirm-desktop-after.png'});
  await dialog.getByRole('button',{name:'取消',exact:true}).click();
  check(await name.inputValue()==='Unsaved draft','Cancel preserves the unsaved editor draft');
  await remove.click();await dialog.waitFor();await page.keyboard.press('Escape');
  check(await name.inputValue()==='Unsaved draft','Escape preserves the draft');
  await locale('en');
  check(await remove.textContent()==='Delete assistant','Mounted deletion controls follow locale changes');
  await remove.click();await dialog.waitFor();
  check((await dialog.innerText()).includes('Delete “History fixture”?'),'English confirmation is localized');
  await locale('zh-CN');
  check((await dialog.innerText()).includes('删除“History fixture”？'),'Open confirmation follows locale changes');
  await dialog.getByRole('button',{name:'取消',exact:true}).click();
  let posts=0,release,arrived;
  const pending=new Promise(resolve=>arrived=resolve);
  const held=new Promise(resolve=>release=resolve);
  await page.route('**/api/v1/assistants/*/delete?**',async route=>{posts++;arrived();await held;await route.fulfill({status:409,json:{code:'assistant_busy',message:'fixture busy'}});});
  await remove.click();await dialog.getByRole('button',{name:'删除助手',exact:true}).click();await pending;
  check(await remove.isDisabled(),'Pending deletion disables repeated activation');
  await locale('en');release();
  await page.getByText('End this assistant’s ongoing reply and tasks before deleting it.',{exact:true}).waitFor();
  check(posts===1&&await name.inputValue()==='Unsaved draft','Busy failure retains draft and sends one command');
  await page.unroute('**/api/v1/assistants/*/delete?**');
  await page.setViewportSize({width:390,height:844});await locale('zh-CN');
  await remove.scrollIntoViewIfNeeded();
  await page.screenshot({path:'output/playwright/assistant-delete-mobile-after.png'});
  await remove.click();await dialog.waitFor();
  await page.screenshot({path:'output/playwright/assistant-delete-confirm-mobile-after.png'});
  await dialog.getByRole('button',{name:'删除助手',exact:true}).click();
  await page.locator('.editor-form').waitFor({state:'detached'});
  const list=await(await page.request.get(origin+'/api/v1/assistants')).json();
  check(list.length===0,'Confirmed deletion removes the actual persisted identity');
  await page.getByRole('heading',{name:'添加你的第一个助手',exact:true}).waitFor();
  const reloadURL=new URL(page.url());reloadURL.searchParams.set('locale','zh-CN');await page.goto(reloadURL.href);
  await page.getByRole('heading',{name:'添加你的第一个助手',exact:true}).waitFor();
  check((await(await page.request.get(origin+'/api/v1/assistants')).json()).length===0,'Reload does not recreate the last deleted assistant');
  await page.getByRole('button',{name:'创建助手',exact:true}).click();
  await page.waitForFunction(()=>!document.querySelector('#pot-composer')?.disabled&&!!document.querySelector('.window-title .assistant-identity-name'));
  const replacement=await(await page.request.get(origin+'/api/v1/assistants')).json();
  check(replacement.length===1&&replacement[0].id!==originalID,'Explicit creation uses a new identity');
  await page.setViewportSize({width:1200,height:900});
  await page.request.post(origin+'/api/v1/assistants',{data:{request_id:'survivor-'+Date.now(),name:'Survivor'}});
  // An independent window/API deletion must refresh a selected conversation too.
  const selected=replacement[0];
  const deleted=await page.request.post(origin+'/api/v1/assistants/'+selected.id+'/delete',{data:{revision:selected.revision}});
  check(deleted.ok(),'An independent client can retire the selected idle assistant');
  await page.locator('.window-title').filter({hasText:'Survivor'}).waitFor();
  check((await(await page.request.get(origin+'/api/v1/assistants')).json()).length===1,'Other assistants survive deletion and become selectable');
  return {checks};
}
