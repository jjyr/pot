// Real DOM contract for the single account disclosure; all requests are fixtures.
async page=>{
 const context=await page.context().browser().newContext({serviceWorkers:'block'});page=await context.newPage();
 try{
  await page.route('**/github-account-fixture',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><body></body></html>'}));
  await page.goto(STATE.origin+'/github-account-fixture');
  const result=await page.evaluate(async()=>{
   const {createIntegrations}=await import('/components/integrations.mjs');
   const root=document.createElement('main');document.body.replaceChildren(root);
   const assert=(value,message)=>{if(!value)throw Error(message);};
   const controller=new AbortController();let locale='en',rejectSave=false,release;
   const writes=[];
   const account={identity:{id:'fixture-user',username:'octocat'},revision:1};
   const integration={id:'github',name:'GitHub',status:'connected',login_available:true,account};
   const binding={id:'github',enabled:true,assistant:'first',revision:1,status:'baseline',config:{repositories:[],reasons:['mention','custom_reason'],existing:''}};
   const data={items:[integration],notifications:{items:[binding],assistants:[{id:'first',name:'Pot'},{id:'second',name:'Second'}]}};
   const update=createIntegrations({root,signal:controller.signal,locale:()=>locale,scope:{space:'fixture-control'},refresh:async()=>update(data),request:async(path,options)=>{
    assert(options.space==='fixture-control','Requests stay in this Control');writes.push({path,...options});
    if(options.method==='DELETE'){delete integration.account;integration.status='disconnected';return {};}
    await new Promise(resolve=>{release=resolve;});
    if(rejectSave)throw Error('fixture revision conflict');
    assert(options.body.revision===binding.revision,'Draft keeps its starting revision');
    Object.assign(binding,options.body,{revision:binding.revision+1});return {saved:true};
   }});
   const input=label=>root.querySelector(`[aria-label="${label}"]`);
   const change=(element,value,event='input')=>{if(typeof value==='boolean')element.checked=value;else element.value=value;element.dispatchEvent(new Event(event,{bubbles:true}));};
   const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
   const toggle=()=>root.querySelector('.integration-toggle');
   update(data);
   assert(toggle().getAttribute('aria-expanded')==='false'&&root.querySelector('#github-account-settings').hidden,'Initially collapsed');
   assert(toggle().textContent.includes('Notifications → Pot'),'Healthy summary');
   toggle().click();
   assert(toggle().getAttribute('aria-expanded')==='true'&&!root.querySelector('#github-account-settings').hidden,'One click expands all settings');
   assert(!root.querySelector('details')&&input('Process existing notifications on the next check'),'No nested disclosure');
   assert(input('Other reasons (API values, comma separated)').value==='custom_reason','Custom API values survive');
   const repo=input('Repositories (owner/repo, comma separated; blank for all)');repo.focus();change(repo,'jjyr/pot');
   binding.config.repositories=['background/change'];update(data);
   assert(repo===document.activeElement&&repo.value==='jjyr/pot','Background data preserves dirty input and focus');
   locale='zh-CN';window.dispatchEvent(new Event('pot:locale'));
   assert(repo===document.activeElement&&repo.value==='jjyr/pot'&&repo.getAttribute('aria-label').includes('仓库'),'Locale changes labels while preserving controls/focus');
   const save=()=>[...root.querySelectorAll('button')].find(button=>/保存通知设置|Save notification settings/.test(button.textContent));
   rejectSave=true;repo.focus();save().click();assert(save().disabled,'Pending save is protected');assert(repo===document.activeElement,'Pending save preserves focus');
   locale='en';window.dispatchEvent(new Event('pot:locale'));assert(repo===document.activeElement,'Pending locale change preserves focus');release();await tick();
   assert(root.querySelector('[role=alert]').textContent.includes('Could not save')&&repo.value==='jjyr/pot','Failure uses current locale and retains draft');
   rejectSave=false;save().click();release();await tick();
   assert(!root.querySelector('[role=alert]')&&toggle().getAttribute('aria-expanded')==='true','Save succeeds without collapsing');
   // Reason checkboxes retain custom values across reconciliation.
   input('Mentioned my team').click();
   assert(input('Other reasons (API values, comma separated)').value==='custom_reason','Labeled filters preserve custom reasons');
   save().click();release();await tick();
   assert(writes.at(-1).body.config.reasons.includes('team_mention'),'Labels save the actual GitHub API value');
   assert(writes.at(-1).body.config.reasons.includes('custom_reason')&&!writes.at(-1).body.config.existing,'Save preserves reasons; history is opt-in');
   input('Process existing notifications on the next check').click();save().click();release();await tick();
   const historyToken=writes.at(-1).body.config.existing;
   assert(historyToken&&writes.at(-1).body.config.existing_account==='fixture-user','Explicit history request is scoped to the actual account');
   assert(!input('Process existing notifications on the next check').checked,'History request resets after saving');
   save().click();release();await tick();assert(writes.at(-1).body.config.existing===historyToken,'Saving other settings does not request history again');
   binding.status='permission';update(data);toggle().click();
   assert(toggle().textContent.includes('Notification issue'),'Collapsed summary exposes permission failure');toggle().click();
   assert(root.textContent.includes('GitHub denied notification access'),'Actionable permission status');
   binding.status='assistant_missing';update(data);
   assert(root.textContent.includes('Choose an assistant in this Control'),'Missing target is actionable');
   binding.assistant='';binding.status='waiting_assistant';data.notifications.assistants=[];update(data);
   assert(toggle().textContent.includes('waiting for an assistant'),'No assistant waiting state');
   const identity=toggle();[...root.querySelectorAll('button')].find(button=>button.textContent==='Log out').click();await tick();
   assert(!root.querySelector('.integration-toggle')&&!root.querySelector('#github-account-settings')&&identity.getAttribute('aria-expanded')==='true','Sign out is separate from disclosure');
   integration.login={id:'fixture',status:'pending',authorization_url:'https://example.invalid',user_code:'FIXTURE-CODE'};update(data);
   assert(root.querySelector('.integration-code').textContent==='FIXTURE-CODE'&&root.querySelector('.integration-authorization a'),'Device flow instructions stay visible');
   integration.account={...account,identity:{id:'different',username:'other'}};integration.status='connected';delete integration.login;update(data);
   assert(toggle().getAttribute('aria-expanded')==='false','A new account starts collapsed');
   binding.assistant='first';binding.status='baseline';data.notifications.assistants=[{id:'first',name:'Pot'}];update(data);toggle().click();
   change(input('Repositories (owner/repo, comma separated; blank for all)'),'old/draft');save().click();
   binding.config.repositories=['new/account'];integration.account.identity={id:'third',username:'third'};update(data);release();await tick();
   assert(toggle().getAttribute('aria-expanded')==='false'&&input('Repositories (owner/repo, comma separated; blank for all)').value==='new/account','A late save cannot acknowledge another account draft');
   controller.abort();return 'GitHub account DOM checks passed';
  });
  await page.setViewportSize({width:393,height:852});
  await page.locator('.integration-toggle').press('Enter');
  if(await page.locator('.integration-toggle').getAttribute('aria-expanded')!=='true')throw Error('Keyboard disclosure failed');
  if(!await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth))throw Error('Mobile content overflows');
  console.log(result+'; keyboard and mobile layout passed');return {passed:true};
 }finally{await context.close();}
}
