import {waitForOutbox as disk} from './outbox-persistence-browser.mjs';
import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import {sourceServer} from './browser-harness.mjs';
import {attachmentPickerFixture} from './attachment-picker-fixture.mjs';

const waitVisible=page=>page.waitForFunction(()=>{const history=document.querySelector('.history:not([data-preparation-surface])');return history&&getComputedStyle(history).visibility==='visible'&&!history.hasAttribute('aria-busy');});
export async function chatOutboxBrowser(browser,{sourceOrigin,captureDir,baseline=false}={}){
 const fixture=await attachmentPickerFixture(sourceOrigin,{durableReceipts:true}),results=[];console.log('OWN_FIXTURE '+fixture.origin);
 const context=await browser.newContext({viewport:{width:1280,height:800},serviceWorkers:'block'}),page=await context.newPage(),errors=[];
 const ready=()=>page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&!document.querySelector('#pot-composer')?.disabled);
 const control=async data=>{const response=await context.request.post(fixture.origin+'/fixture/control',{data});assert(response.ok());};
 const server=async()=>await(await context.request.get(fixture.origin+'/fixture/state')).json();
 const send=async text=>{await page.locator('#pot-composer').fill(text);await page.getByRole('button',{name:'Send',exact:true}).click();};
 page.setDefaultTimeout(15000);page.on('pageerror',error=>errors.push(error.message));
 await context.addCookies([{name:'picker-case',value:baseline?'outbox-before':'outbox-after',url:fixture.origin}]);
 try{
  await control({references:true});await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha&conversation=main');await ready();await page.waitForSelector('.attachment-plus:not(:disabled)');await waitVisible(page);
  await context.setOffline(true);await page.locator('#pot-composer').fill('Send this when the connection returns.');
  if(baseline){assert.equal(await page.getByRole('button',{name:'Send',exact:true}).isDisabled(),true);results.push({name:'baseline-offline-disabled',ok:true});}
  else{
   assert.equal(await page.getByRole('button',{name:'Send',exact:true}).isEnabled(),true);await page.getByRole('button',{name:'Send',exact:true}).click();
   let saved=await disk(page,([rows,drafts])=>rows.length===1&&rows[0].target.control==='picker-control'&&rows[0].target.space==='local'&&rows[0].target.owner==='alpha'&&rows[0].target.conversation==='main'&&rows[0].state==='queued'&&drafts[0].text==='');assert.equal(await page.locator('#pot-composer').inputValue(),'');assert.equal(saved.rows[0].started,false);
   await page.locator('.history:not([data-preparation-surface]) .message-pending:not([hidden])').waitFor();assert.equal(await page.locator('.history:not([data-preparation-surface]) .outbox-controls').count(),0);await waitVisible(page);results.push({name:'offline-explicit-submission',ok:true,request:saved.rows[0].request.request_id});
  }
  if(captureDir){await mkdir(captureDir,{recursive:true});await page.screenshot({path:captureDir+'/outbox-desktop-'+(baseline?'before':'after')+'.png'});}
  if(baseline)return results;
  await page.getByRole('button',{name:'Saved messages',exact:true}).click();await page.locator('.outbox-dialog article.message').waitFor();
  if(captureDir)await page.screenshot({path:captureDir+'/outbox-saved-messages-after.png'});
  await page.locator('.outbox-dialog').getByRole('button',{name:'Close',exact:true}).click();
  await send('Second FIFO message');await disk(page,([rows])=>rows.length===2&&rows.every(row=>row.state==='queued'));
  await page.locator('#pot-composer').fill('Ordinary unsent draft');
  // Cancellation and editing operate on the actual prepared row controls.
  await page.getByRole('button',{name:'Saved messages',exact:true}).click();
  const queued=page.locator('.outbox-dialog article.message').filter({hasText:'Second FIFO message'});
  await queued.getByRole('button',{name:'Edit',exact:true}).click();await queued.getByRole('textbox',{name:'Edit queued message'}).fill('Second FIFO edited');await queued.getByRole('button',{name:'Save',exact:true}).click();await disk(page,([rows])=>rows[1]?.request.text==='Second FIFO edited');
  await page.locator('.outbox-dialog').getByRole('button',{name:'Close',exact:true}).click();
  await send('Cancel this queued message');let saved=await disk(page,([rows])=>rows.length===3);const cancelled=saved.rows[2].request.request_id;
  await page.getByRole('button',{name:'Saved messages',exact:true}).click();
  await page.locator('.outbox-dialog article.message').filter({hasText:'Cancel this queued message'}).getByRole('button',{name:'Cancel queued message',exact:true}).click();await disk(page,([rows])=>rows[2].state==='cancelled');await page.locator('.outbox-dialog').getByRole('button',{name:'Close',exact:true}).click();
  await page.locator('#pot-composer').fill('Ordinary unsent draft');await disk(page,([rows,drafts])=>drafts.some(row=>row.text==='Ordinary unsent draft'));
  const queuedIDs=saved.rows.slice(0,2).map(row=>row.request.request_id);
  // Close and reopen the page while offline: local content remains committed.
  await page.close();const reopened=await context.newPage();reopened.on('pageerror',error=>errors.push(error.message));
  await context.setOffline(false);await reopened.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=beta&conversation=main');
  await reopened.waitForFunction(()=>document.querySelector('.connection-status.is-connected'));
  saved=await disk(reopened,([rows])=>rows.filter(row=>row.state==='accepted').length===2,{timeout:45000});
  const evidence=await server(),posts=evidence.trace.filter(row=>row.kind==='post');assert.deepEqual(posts.map(row=>row.body.request_id),queuedIDs);assert(posts.every(row=>row.owner==='alpha'));assert(!posts.some(row=>row.body.request_id===cancelled||row.body.text==='Ordinary unsent draft'));assert.equal(evidence.rows.length,2);results.push({name:'reopen-fifo-fixed-target-draft-not-sent',ok:true,trace:posts});
  // A second actual page shares the database. Transaction claims and durable
  // request identities constrain both independent dispatchers.
  const second=await context.newPage();second.on('pageerror',error=>errors.push(error.message));await second.goto(fixture.origin+'/?locale=en&assistant_id=alpha&conversation=main');await second.waitForFunction(()=>document.querySelector('.connection-status.is-connected'));
  await second.locator('#pot-composer').fill('Concurrent tabs single acceptance');await second.getByRole('button',{name:'Send',exact:true}).click();await disk(second,([rows])=>rows.length===4&&rows[3].state==='accepted');
  assert.equal((await server()).trace.filter(row=>row.kind==='post'&&row.body.text==='Concurrent tabs single acceptance').length,1);results.push({name:'two-real-tabs-single-post',ok:true});
  // Accepted-but-lost and not-yet-accepted HTTP responses use actual sockets,
  // durable SQLite fixture receipts and the production adapter's fetch path.
  for(const mode of ['dropAfterAccept','dropBeforeAccept']){
    await control({[mode]:true});const text='Synthetic '+mode;
    await second.locator('#pot-composer').fill(text);await second.getByRole('button',{name:'Send',exact:true}).click();
    const latest=await disk(second,mode==='dropAfterAccept'?([rows])=>rows.length===5&&rows[4].target.control==='picker-control'&&rows[4].target.space==='local'&&rows[4].target.owner==='alpha'&&rows[4].target.conversation==='main'&&rows[4].request.text==='Synthetic dropAfterAccept'&&rows[4].state==='accepted':([rows])=>rows.length===6&&rows[5].target.control==='picker-control'&&rows[5].target.space==='local'&&rows[5].target.owner==='alpha'&&rows[5].target.conversation==='main'&&rows[5].request.text==='Synthetic dropBeforeAccept'&&rows[5].state==='accepted',{timeout:45000});
    const row=latest.rows.at(-1),state=await server(),attempts=state.trace.filter(entry=>entry.kind==='post'&&entry.body.request_id===row.request.request_id);
    assert.equal(attempts.length,mode==='dropAfterAccept'?1:2);assert.equal(state.rows.filter(entry=>entry.body.request_id===row.request.request_id).length,1);assert(state.trace.some(entry=>entry.kind==='receipt'&&entry.query.request_id===row.request.request_id));results.push({name:mode,ok:true,id:row.request.request_id,posts:attempts.length});
  }
  await second.evaluate(()=>dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}})));
  await second.getByRole('button',{name:'已保存的消息',exact:true}).click();await second.locator('.outbox-dialog .outbox-status').filter({hasText:'已接收'}).first().waitFor();assert.equal(await second.locator('.outbox-dialog').getAttribute('aria-label'),'已保存的消息');
  if(captureDir)await second.screenshot({path:captureDir+'/outbox-saved-messages-chinese-after.png'});
  await second.locator('.outbox-dialog').getByRole('button',{name:'关闭',exact:true}).click();results.push({name:'saved-messages-bilingual',ok:true});
  await second.close();await reopened.close();assert.deepEqual(errors,[]);return results;
 }catch(error){await writeFile('.pot-artifacts/outbox-browser-failure.json',JSON.stringify({error:error.stack,errors,server:await server(),body:await page.locator('body').innerText().catch(()=>''),results},null,2));throw error;}
 finally{await context.setOffline(false);await context.close();await fixture.close();}
}

export async function outboxBrowserRestart({chromium,sourceOrigin,profile,captureDir}){
 const fixture=await attachmentPickerFixture(sourceOrigin,{durableReceipts:true});console.log('OWN_FIXTURE '+fixture.origin);let context;
 const open=()=>chromium.launchPersistentContext(profile,{executablePath:process.env.POT_CHROMIUM_EXECUTABLE,headless:true,viewport:{width:390,height:852},serviceWorkers:'allow',args:['--no-sandbox']});
 try{
  context=await open();await context.addCookies([{name:'picker-case',value:'persistent-restart',url:fixture.origin}]);let page=context.pages()[0]||await context.newPage();page.setDefaultTimeout(20000);
  await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha&conversation=main');await page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&document.querySelector('.attachment-plus:not(:disabled)'));await page.evaluate(()=>navigator.serviceWorker.ready);await page.reload();await page.waitForFunction(()=>!!navigator.serviceWorker.controller&&document.querySelector('.connection-status.is-connected')&&document.querySelector('.attachment-plus:not(:disabled)'));
  await context.setOffline(true);await page.locator('#pot-composer').fill('Explicit submission survives browser process exit');await page.getByRole('button',{name:'Send',exact:true}).click();const before=await disk(page,([rows,drafts])=>rows.length===1&&rows[0].state==='queued'&&drafts[0].text==='');
  await page.locator('#pot-composer').fill('Ordinary draft must remain unsent after browser restart');await disk(page,([rows,drafts])=>drafts[0].text==='Ordinary draft must remain unsent after browser restart');await context.close();context=null;
  context=await open();await context.setOffline(true);page=context.pages()[0]||await context.newPage();page.setDefaultTimeout(20000);await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha&conversation=main');await page.waitForFunction(()=>!!document.querySelector('.message-pending:not([hidden])')&&document.querySelector('#pot-composer')?.value==='Ordinary draft must remain unsent after browser restart');
  const offline=await disk(page,([rows])=>rows.length===1&&rows[0].state==='queued'&&!rows[0].started);assert.equal(offline.rows[0].request.request_id,before.rows[0].request.request_id);
  if(captureDir){await mkdir(captureDir,{recursive:true});await page.screenshot({path:captureDir+'/outbox-phone-offline-reopen-after.png'});}
  await context.setOffline(false);await disk(page,([rows])=>rows[0].state==='accepted'&&!rows[0].projection_pending,{timeout:45000});const state=await(await context.request.get(fixture.origin+'/fixture/state')).json(),posts=state.trace.filter(row=>row.kind==='post');assert.equal(posts.length,1);assert.equal(posts[0].body.request_id,before.rows[0].request.request_id);assert.equal(posts[0].body.text,'Explicit submission survives browser process exit');assert.equal(await page.locator('#pot-composer').inputValue(),'Ordinary draft must remain unsent after browser restart');return {name:'real-browser-process-offline-restart',ok:true,id:before.rows[0].request.request_id,posts};
 }finally{await context?.close();await fixture.close();}
}

if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){
 const source=await sourceServer({root:process.env.POT_SOURCE_ROOT||undefined});
 console.log('OWN_SOURCE '+source.origin);
 const {chromium}=await import(pathToFileURL(process.env.POT_PLAYWRIGHT_MODULE).href);
 let browser=await chromium.launch({executablePath:process.env.POT_CHROMIUM_EXECUTABLE,headless:true,args:['--no-sandbox'],env:{...process.env,TMPDIR:process.env.POT_BROWSER_TMPDIR||process.env.TMPDIR}});
 try{
  if(!process.argv.includes('--baseline')){const context=await browser.newContext(),page=await context.newPage();try{await page.goto(source.origin+'/scripts/tests/fixtures/chat-outbox-storage.html');await page.waitForFunction(()=>/DONE|FAIL/.test(document.querySelector('#results').textContent),null,{timeout:45000});const output=await page.locator('#results').textContent();await writeFile('.pot-artifacts/outbox-storage.log',output);assert(!output.includes('FAIL'),output);}finally{await context.close();}}
  const result=await chatOutboxBrowser(browser,{sourceOrigin:source.origin,captureDir:process.env.POT_CAPTURE_DIR,baseline:process.argv.includes('--baseline')});
  await writeFile('.pot-artifacts/outbox-main-browser.json',JSON.stringify(result,null,2));await browser.close();browser=null;if(!process.argv.includes('--baseline')&&process.env.POT_OUTBOX_PROFILE)result.push(await outboxBrowserRestart({chromium,sourceOrigin:source.origin,profile:process.env.POT_OUTBOX_PROFILE,captureDir:process.env.POT_CAPTURE_DIR}));await writeFile('.pot-artifacts/outbox-browser-'+(process.argv.includes('--baseline')?'before':'after')+'.json',JSON.stringify(result,null,2));console.log(JSON.stringify(result));
 }finally{await browser?.close();await source.close();}
}
