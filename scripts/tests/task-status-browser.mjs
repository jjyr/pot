import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';

// Actual task rows/cache plus the production Chat capability guards, all using
// synthetic records. No product API, model or persistent user data is accessed.
export async function taskStatusBrowser(browser,{sourceOrigin,output}={}){
 const results=[];
 for(const [width,locale,theme]of [[1280,'en','light'],[390,'zh-CN','dark'],[320,'en','dark'],[1280,'zh-CN','light']]){
  const context=await browser.newContext({viewport:{width,height:800},reducedMotion:'reduce'}),page=await context.newPage(),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await context.route('**/*',route=>new URL(route.request().url()).origin===sourceOrigin?route.continue():route.abort());
  try{
   await page.goto(`${sourceOrigin}/scripts/tests/fixtures/task-status.html?locale=${locale}&theme=${theme}`);
   await page.waitForFunction(()=>window.taskStatus||document.querySelector('#results')?.textContent.startsWith('FAIL'));
   assert.match(await page.locator('#results').textContent(),/^PASS 13 checks/);assert.deepEqual(errors,[]);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'task rows fit viewport');
   results.push({width,locale,theme,checks:await page.evaluate(()=>taskStatus.checks)});
  }finally{await context.close();}
 }
 const context=await browser.newContext({viewport:{width:1280,height:800},serviceWorkers:'block'}),page=await context.newPage(),errors=[];let currentGate;
 page.setDefaultTimeout(8000);
 page.on('pageerror',error=>errors.push(error.message));
 await context.route('**/*',route=>new URL(route.request().url()).origin===sourceOrigin?route.continue():route.abort());
 try{
  await page.goto(sourceOrigin+'/scripts/tests/fixtures/task-communication.html?identity=1&single=1&task-status=1&locale=en');
  await page.waitForFunction(()=>window.taskCommunication);await page.evaluate(()=>taskCommunication.prepare());
  const row=page.locator('.background-task').first(),send=page.locator('.send-button');
  await page.waitForFunction(()=>document.querySelector('.background-task .task-activity-dot')?.dataset.phase==='running');
  await page.locator('#pot-composer').fill('Synthetic guarded draft');await page.waitForFunction(()=>!document.querySelector('.send-button')?.disabled);
  await page.evaluate(()=>taskCommunication.holdTasks());
  await page.evaluate(()=>taskCommunication.tasks(tasks=>tasks[0].phase='waiting'));
  await page.waitForFunction(()=>document.querySelector('.background-task-elapsed')?.hasAttribute('data-finished-at'));
  assert.equal(await row.locator('.task-activity-dot').getAttribute('data-phase'),'running','invalidated query retains confirmed phase while IO waits');
  await page.evaluate(()=>taskCommunication.releaseTasks());
  await page.waitForFunction(()=>document.querySelector('.background-task .task-activity-dot')?.dataset.phase==='waiting'&&!document.querySelector('.background-task-elapsed')?.hasAttribute('data-finished-at'));
  await page.locator('#pot-composer').fill('Synthetic guarded draft');
  await page.evaluate(()=>{const f=taskCommunication,request=f.client.request.bind(f.client);f.attemptedWrites=[];f.client.request=(path,options={})=>{if(options.method&&options.method!=='GET')f.attemptedWrites.push(path);return request(path,options);};});
  for(const gate of [{operations:false},{online:false},{capability:false},{scopeReady:false}]){
   currentGate=gate;
   await page.evaluate(gate=>taskCommunication.gate(gate),gate);
   await page.waitForFunction(()=>document.querySelector('.send-button')?.disabled&&document.querySelector('.background-task-elapsed')?.hasAttribute('data-finished-at'));
   assert.equal(await row.locator('.task-activity-dot').getAttribute('data-phase'),'waiting','temporary gate does not replace last task phase');
   assert.equal(await row.locator('.background-task-status').textContent(),'Waiting');
   const frozen=await row.locator('.background-task-elapsed').textContent();
   await page.evaluate(async()=>{const send=document.querySelector('.send-button');send.disabled=false;send.click();send.disabled=true;for(let i=0;i<6;i++)await new Promise(requestAnimationFrame);});
   // Real handler checks authority even if a caller tampers with disabled.
   assert.deepEqual(await page.evaluate(()=>taskCommunication.attemptedWrites),[]);
   assert.equal(await row.locator('.background-task-elapsed').textContent(),frozen);
   await page.evaluate(()=>taskCommunication.gate());
   await page.waitForFunction(()=>!document.querySelector('.send-button')?.disabled&&!document.querySelector('.background-task-elapsed')?.hasAttribute('data-finished-at'));
   assert.equal(await send.isEnabled(),true);
   results.push({productionChatGate:gate,phase:'waiting',writes:0});
  }
  assert.deepEqual(errors,[]);assert.deepEqual(await page.evaluate(()=>taskCommunication.errors),[]);
 }catch(error){
  const state=await page.evaluate(()=>({sendDisabled:document.querySelector('.send-button')?.disabled,rows:[...document.querySelectorAll('.background-task')].map(row=>({phase:row.querySelector('.task-activity-dot')?.dataset.phase,time:row.querySelector('.background-task-elapsed')?.outerHTML})),operations:taskCommunication.client.operationsReady,online:taskCommunication.client.onlineReady,assistants:taskCommunication.client.assistants}));
  throw Error(error.message+' '+JSON.stringify({currentGate,errors,state}),{cause:error});
 }finally{await context.close();}
 if(output){
  await mkdir(output,{recursive:true});
  for(const [name,width,locale,theme]of [['desktop-light',1280,'en','light'],['phone-dark',390,'zh-CN','dark']])for(const version of ['before','after']){
   const context=await browser.newContext({viewport:{width,height:800},colorScheme:theme,reducedMotion:'reduce'}),page=await context.newPage();
   try{
    await page.goto(`${sourceOrigin}/scripts/tests/fixtures/task-status.html?locale=${locale}&theme=${theme}${version==='before'?'&before=1':''}`);
    await page.waitForFunction(()=>window.taskStatus);await page.evaluate(()=>document.fonts.ready);
    await page.locator('#tasks').screenshot({path:`${output}/task-status-${name}-${version}.png`});
   }finally{await context.close();}
  }
 }
 return results;
}
