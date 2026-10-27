import assert from 'node:assert/strict';
import {readFile,mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {launchBrowser} from './browser-harness.mjs';

const origin=(await readFile(process.env.POT_DIRECTORY_FREQUENCY_URL_FILE,'utf8')).trim(),output=resolve(process.env.POT_DIRECTORY_FREQUENCY_OUTPUT||'.pot-artifacts/frequency/live');await mkdir(output,{recursive:true});
const browser=await launchBrowser(),results=[];
try{
 for(const phone of [false,true]){
  const context=await browser.newContext({viewport:phone?{width:390,height:852}:{width:1280,height:800},isMobile:phone,hasTouch:phone}),page=await context.newPage(),errors=[];
  page.setDefaultTimeout(12000);page.on('pageerror',e=>errors.push(e.message));
  await context.addInitScript(()=>{window.taskOutputFrames=[];const sample=()=>{const h=document.querySelector('.task-history'),box=h?.getBoundingClientRect();taskOutputFrames.push({at:Date.now(),top:h?.scrollTop,height:h?.clientHeight,extent:h?.scrollHeight,visible:!!h&&getComputedStyle(h).visibility==='visible',text:document.querySelector('.task-transcript')?.textContent?.length,first:[...document.querySelectorAll('.task-transcript [data-message-id]')].find(row=>row.getBoundingClientRect().bottom>box?.top)?.dataset.messageId});requestAnimationFrame(sample);};requestAnimationFrame(sample);});
  await context.request.post(origin+'/fixture/login');
  let created;
  try{
   await page.goto(origin+'/?locale=en');await page.locator('.assistant-row').filter({hasText:'History fixture'}).waitFor({state:'attached'});if(phone&&await page.locator('.assistant-row').filter({hasText:'History fixture'}).isVisible())await page.locator('.assistant-row').filter({hasText:'History fixture'}).click();
   await page.waitForFunction(()=>document.querySelector('.connection-status.is-connected'));
   if(await page.getByRole('button',{name:'Not now',exact:true}).isVisible())await page.getByRole('button',{name:'Not now',exact:true}).click();
   await page.waitForFunction(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
   // Open through the catalogue: Chat virtualizes task cards with its reading
   // position, which is independent of this transcript invalidation contract.
   await page.waitForTimeout(500);
   const rail=page.locator('.rail-button[aria-label="Tasks"]');
   if(await rail.isVisible())await rail.click();else{
    const navigation=page.locator('.narrow-navigation');
    if(await navigation.getAttribute('aria-label')==='Back to Assistants')await navigation.click();
    await navigation.click();await page.locator('.menu-destination[aria-label="Tasks"]').click();
   }
   await page.locator('.task-list').waitFor();
   const response=await context.request.post(origin+'/fixture/task/start');assert(response.ok(),await response.text());created=await response.json();
   const row=page.locator('.task-row[data-phase="running"]').filter({hasText:'Synthetic frequency Task'});await row.waitFor();await row.click();
   const transcript=page.getByRole('log',{name:'Task conversation',exact:true});await transcript.waitFor();
   await page.waitForFunction(()=>document.querySelector('.task-transcript')?.textContent.includes('live-05'));
   const streamStart=Date.now(),before=await transcript.innerText();assert(before.includes('live-00'));
   await page.waitForFunction(()=>document.querySelector('.task-transcript')?.textContent.includes('live-70'));
   const streamEnd=Date.now(),after=await transcript.innerText();assert(after.length>before.length,'an opened Task transcript must continue appending output');
   assert(streamEnd-streamStart>5000,'the observed output window must exceed the resource TTL');
   await page.locator('[data-view-key="task-actions"]').click();await page.getByRole('dialog',{name:'Task actions',exact:true}).getByRole('button',{name:'Cancel task',exact:true}).click();
   const started=Date.now();await page.getByRole('dialog',{name:'Task actions',exact:true}).getByRole('button',{name:'Cancel task',exact:true}).click();
   let record;do{record=await(await context.request.get(origin+'/fixture/task/state?id='+created.task.id)).json();if(record?.info?.phase==='cancelled')break;await page.evaluate(()=>new Promise(requestAnimationFrame));}while(Date.now()-started<10000);assert.equal(record.info.phase,'cancelled');
   const server=await(await context.request.get(origin+'/fixture/state')).json(),reads=server.trace.filter(row=>row.kind==='http-start'&&streamStart<=row.at&&row.at<=streamEnd),directoryGETs=reads.filter(row=>row.path==='/api/v1/assistants').length,activeGETs=reads.filter(row=>row.path==='/api/v1/tasks/query'||row.path==='/api/v1/tasks/catalog').length;
   assert.equal(directoryGETs,0,'an opened transcript cannot refresh directory through detail resource commits');assert.equal(activeGETs,0,'an opened transcript cannot refresh task cards or list');
   results.push({phone,task:created.task.id,beforeLength:before.length,afterLength:after.length,directoryGETs,activeGETs,streamMS:streamEnd-streamStart,cancelMS:Date.now()-started,phase:record.info.phase,errors});assert.deepEqual(errors,[]);
   await writeFile(resolve(output,`task-live-${phone?'phone':'desktop'}.json`),JSON.stringify({result:results.at(-1),server,frames:await page.evaluate(()=>taskOutputFrames)},null,2));
  }catch(error){await writeFile(resolve(output,`task-${phone?'phone':'desktop'}-failure.txt`),error.stack+'\n'+await page.locator('body').innerText());await writeFile(resolve(output,`task-${phone?'phone':'desktop'}-failure.json`),JSON.stringify({created,server:await(await context.request.get(origin+'/fixture/state')).json(),record:created?await(await context.request.get(origin+'/fixture/task/state?id='+created.task.id)).json():null},null,2));throw error;}
  finally{await context.close();}
 }
 console.log(JSON.stringify(results,null,2));await writeFile(resolve(output,'task-live-summary.json'),JSON.stringify(results,null,2));
}finally{await browser.close();if(!process.argv.includes('--keep-fixture')){const response=await fetch(origin+'/fixture/stop',{method:'POST'});assert(response.ok,'fixture shutdown');}}
