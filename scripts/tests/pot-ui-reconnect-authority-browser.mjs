// Real production bootstrap, HTTP/SSE and IndexedDB with synthetic identities.
// Never submits a message, creates an assistant, or contacts a product service.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
const output=resolve(process.env.POT_AUTHORITY_OUTPUT||'.pot-artifacts/reconnect-authority');
await mkdir(output,{recursive:true});
const report={passed:false,observations:[],errors:[]};
const child=spawn(process.execPath,[resolve('scripts/tests/pot-ui-cold-start-browser.mjs'),'--serve'],{env:process.env,stdio:['ignore','pipe','pipe']});
let browser;
try{
 const origin=await new Promise((done,reject)=>{let data='';child.stdout.on('data',part=>{data+=part;const match=data.match(/http:\/\/127\.0\.0\.1:\d+/);if(match)done(match[0]);});child.stderr.on('data',part=>report.errors.push(String(part)));child.on('exit',code=>reject(Error('Fixture exited '+code)));});
 const config=async values=>(await fetch(origin+'/__fixture?'+new URLSearchParams(values))).json();
 const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE||'playwright');
 browser=await chromium.launch({headless:true,executablePath:process.env.POT_CHROMIUM_EXECUTABLE,args:['--no-sandbox']});
 for(const viewport of [{width:1280,height:800},{width:390,height:844}]){
  await config({hold:'',revoked:'false',offlineSpace:'false',control:'synthetic-control',releaseHeld:''});
  const context=await browser.newContext({viewport}),page=await context.newPage();page.setDefaultTimeout(15000);
  page.on('pageerror',error=>report.errors.push(error.message));
  const editor=page.locator('textarea.composer-input'),send=page.locator('.send-button');
  const select=async name=>{if(viewport.width<500&&await page.getByRole('button',{name:'Back to Assistants',exact:true}).count())await page.getByRole('button',{name:'Back to Assistants',exact:true}).click();await page.getByText(name,{exact:true}).first().click();await editor.waitFor();await page.waitForFunction(name=>document.querySelector('.history')?.innerText.includes(name.split(' ')[1].toUpperCase()+' cached message 90.'),name);await page.getByText('Draft saved on this device',{exact:false}).waitFor();};
  const waitHeld=async group=>page.waitForFunction(async group=>(await(await fetch('/__fixture')).json()).pending.includes(group),group);
  const capture=async name=>{const value=await page.evaluate(()=>({text:document.body.innerText,draft:document.querySelector('textarea.composer-input')?.value,sendDisabled:document.querySelector('.send-button')?.disabled,sendLabel:document.querySelector('.send-button')?.getAttribute('aria-label'),buttons:[...document.querySelectorAll('button')].map(button=>({label:button.getAttribute('aria-label')||button.textContent,disabled:button.disabled})),inert:[...document.querySelectorAll('[inert]')].map(node=>node.className)}));report.observations.push({name,width:viewport.width,...value,fixture:await config({})});return value;};
  const locked=async name=>{assert(await send.isDisabled(),name);const value=await capture(name);for(const label of ['Tasks','Settings','Add assistant'])assert(value.buttons.filter(button=>button.label===label).every(button=>button.disabled),label+' opened early');};
  await page.goto(origin+'/?locale=en');if(viewport.width<500)await select('Synthetic Alpha');
  await editor.fill('Synthetic never-submit draft');await page.waitForFunction(()=>!document.querySelector('.send-button').disabled);
  await select('Synthetic Beta');await page.waitForFunction(()=>document.querySelector('.send-button')?.getAttribute('aria-label')==='Continue task'&&!document.querySelector('.send-button').disabled);
  await select('Synthetic Alpha');
  for(const path of ['spaces','assistants']){
   const group=path+'-fresh';await config({hold:path+':'+group,disconnect:'1'});await waitHeld(group);
   // Runtime and the new conversation stream can complete before the directory.
   await page.waitForTimeout(500);await locked('held-'+path);
   await editor.fill('Editable while '+path+' pending');
   await select('Synthetic Beta');await locked('continue-held-'+path);
   await config({hold:'',releaseHeld:group});await page.waitForFunction(()=>document.querySelector('.send-button')?.getAttribute('aria-label')==='Continue task'&&!document.querySelector('.send-button').disabled);
   await capture('continue-fresh-'+path);await select('Synthetic Alpha');await page.waitForFunction(()=>!document.querySelector('.send-button').disabled);
   assert.equal(await editor.inputValue(),'Editable while '+path+' pending');await capture('send-fresh-'+path);
  }
  // Fresh directory alone cannot restore the old conversation's authority.
  await config({hold:'session:session-fresh',disconnect:'1'});await waitHeld('session-fresh');await page.waitForTimeout(500);assert(await send.isDisabled());await capture('held-session');await select('Synthetic Beta');assert(await send.isDisabled());await capture('held-session-continue');
  const sessionBlocked=await page.evaluate(async()=>{
   const {createProductionClient}=await import('/adapters/production.mjs'),{domainsFor}=await import('/state/domains/index.mjs');
   const client=await createProductionClient({startupIdentity:{runtime_id:'synthetic-control',boot_id:'synthetic-boot'}});
   client.confirmStartup({runtime_id:'synthetic-control',boot_id:'synthetic-boot'});client.setScope({space:'local',owner:'beta'});
   const domains=domainsFor(client);await domains.directory.refresh();
   const result={freshCapabilities:client.supports('chat.send')&&client.supports('chat.continue'),statuses:[]};
   for(const operation of [()=>client.send({text:'never submit'}),()=>client.continueTurn({request_id:'never-submit'})]){try{await operation();result.statuses.push('unexpected dispatch');}catch(error){result.statuses.push(error.status);}}
   domains.dispose();client.resources.clear();return result;
  });assert.deepEqual(sessionBlocked,{freshCapabilities:true,statuses:[503,503]});report.observations.push({name:'adapter-unconfirmed-session',width:viewport.width,...sessionBlocked});
  await config({hold:'',releaseHeld:'session-fresh'});await page.waitForFunction(()=>document.querySelector('.send-button')?.getAttribute('aria-label')==='Continue task'&&!document.querySelector('.send-button').disabled);await select('Synthetic Alpha');await page.waitForFunction(()=>!document.querySelector('.send-button').disabled);
  // Hold granted capabilities from an obsolete cycle; the next cycle revokes them.
  await config({hold:'assistants:old-cycle',disconnect:'1'});await waitHeld('old-cycle');
  await config({hold:'assistants:new-cycle',revoked:'true'});
  await page.evaluate(()=>{window.dispatchEvent(new Event('offline'));window.dispatchEvent(new Event('online'));});await waitHeld('new-cycle');
  await config({releaseHeld:'old-cycle'});await page.waitForTimeout(500);await locked('late-old-cycle');
  await config({hold:'',releaseHeld:'new-cycle'});await page.waitForFunction(()=>document.querySelector('button[aria-label="Tasks"]')?.disabled===false);assert(await send.isDisabled());await capture('fresh-revoked-send');
  await select('Synthetic Beta');await page.waitForTimeout(300);assert(await send.isDisabled());assert.equal(await send.getAttribute('aria-label'),'Continue task');await capture('fresh-revoked-continue');
  // A late granted directory from another Control cannot unlock a new identity.
  await config({revoked:'false',hold:'assistants:old-control'});
  await page.evaluate(()=>{window.dispatchEvent(new Event('offline'));window.dispatchEvent(new Event('online'));});await waitHeld('old-control');
  await config({hold:'assistants:new-control',control:'other-synthetic-control'});
  await page.evaluate(()=>{window.dispatchEvent(new Event('offline'));window.dispatchEvent(new Event('online'));});await waitHeld('new-control');
  await config({releaseHeld:'old-control'});await page.waitForTimeout(500);await locked('late-old-control');
  await config({hold:'',releaseHeld:'new-control'});if(viewport.width<500)await select('Synthetic Alpha');await editor.waitFor();assert.equal(await editor.inputValue(),'');await editor.fill('New Control synthetic draft');await page.waitForFunction(()=>!document.querySelector('.send-button').disabled);await capture('new-control-fresh');
  await config({offlineSpace:'true',disconnect:'1'});await page.getByText('Some Spaces are offline. Cached assistants are shown.',{exact:false}).first().waitFor();await page.waitForFunction(()=>!document.querySelector('.send-button').disabled);await capture('confirmed-local-with-offline-space');
  // Existing online forms cannot dispatch a command through pre-reconnect state.
  await page.getByRole('button',{name:'Assistant information',exact:true}).click();await page.getByRole('button',{name:'Assistant settings',exact:true}).first().click();await page.locator('.editor-form').waitFor();
  await config({hold:'assistants:management',disconnect:'1'});await waitHeld('management');
  assert(await page.locator('.page-host[inert]').count()>0);await capture('held-management-form');
  const blocked=await page.evaluate(async()=>{
   const {createProductionClient}=await import('/adapters/production.mjs');
   const client=await createProductionClient({startupIdentity:{runtime_id:'other-synthetic-control',boot_id:'synthetic-boot'}});
   client.confirmStartup({runtime_id:'other-synthetic-control',boot_id:'synthetic-boot'});
   const operations=[()=>client.send({text:'never submit'}),()=>client.continueTurn({request_id:'never-submit'}),()=>client.request('/api/v1/assistants/profile',{method:'POST',body:{name:'never submit'}})],results=[];
   for(const operation of operations){try{await operation();results.push('unexpected dispatch');}catch(error){results.push(error.status);}}
   client.resources.clear();return results;
  });assert.deepEqual(blocked,[503,503,503]);report.observations.push({name:'adapter-blocked-mutations',width:viewport.width,statuses:blocked});
  await config({revoked:'true',hold:'assistants:management-revoked'});
  await page.evaluate(()=>{window.dispatchEvent(new Event('offline'));window.dispatchEvent(new Event('online'));});await waitHeld('management-revoked');
  await config({hold:'',releaseHeld:'management'});await page.waitForTimeout(300);assert(await page.locator('.page-host[inert]').count()>0);
  await config({releaseHeld:'management-revoked'});await page.waitForFunction(()=>document.querySelector('button[aria-label="Tasks"]')?.disabled===false);assert(await page.locator('.page-host[inert]').count()>0);await capture('fresh-revoked-management-form');
  const revoked=await page.evaluate(async()=>{
   const {createProductionClient}=await import('/adapters/production.mjs'),{domainsFor}=await import('/state/domains/index.mjs');
   const client=await createProductionClient({startupIdentity:{runtime_id:'other-synthetic-control',boot_id:'synthetic-boot'}});
   client.confirmStartup({runtime_id:'other-synthetic-control',boot_id:'synthetic-boot'});client.setScope({space:'local',owner:'alpha'});
   const domains=domainsFor(client);await domains.directory.refresh();
   const chat=domains.chat.open(client.scope());await new Promise((resolve,reject)=>chat.watch({snapshot:(value,received,options)=>{if(options.live)resolve();},failed:reject}));
   const operations=[()=>client.send({text:'never submit'}),()=>client.continueTurn({request_id:'never-submit'}),()=>domains.directory.update(client.scope(),'alpha',{name:'never submit'}),()=>domains.directory.configure(client.scope(),{})],results=[];
   for(const operation of operations){try{await operation();results.push('unexpected dispatch');}catch(error){results.push(error.status);}}
   domains.dispose();client.resources.clear();return results;
  });assert.deepEqual(revoked,[403,403,403,403]);report.observations.push({name:'adapter-revoked-mutations',width:viewport.width,statuses:revoked});
  await config({revoked:'false'});await page.evaluate(()=>{window.dispatchEvent(new Event('offline'));window.dispatchEvent(new Event('online'));});await page.waitForFunction(()=>!document.querySelector('.page-host')?.inert);
  await context.close();
 }
 report.calls=(await config({})).calls;
 assert.equal(report.calls.filter(call=>call.method==='POST').length,0,'regression must never dispatch synthetic commands');
 assert.deepEqual(report.errors,[]);report.passed=true;
 console.log('PASS reconnect authority desktop/phone: held spaces/assistants/session, send/continue, fresh/revoked capabilities, retired cycle/Control, management and POST=0');
}catch(error){report.error=error.stack;process.exitCode=1;console.error(error);}
finally{
 await browser?.close();if(child.exitCode===null){const ended=new Promise(done=>child.once('exit',done));child.kill('SIGTERM');await ended;}
 await writeFile(resolve(output,'results.json'),JSON.stringify(report,null,2));
}
