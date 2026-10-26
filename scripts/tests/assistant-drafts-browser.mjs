import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {attachmentPickerFixture} from './attachment-picker-fixture.mjs';

const file={name:'synthetic.png',mimeType:'image/png',buffer:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP438DwHwAGgAJ/EEwb4QAAAABJRU5ErkJggg==','base64')};
export async function assistantDraftsBrowser(browser,{sourceOrigin,captureDir,baseline=false}={}){
 const fixture=await attachmentPickerFixture(sourceOrigin),results=[];
 const scenario=async(name,run)=>{
  const context=await browser.newContext({viewport:{width:390,height:852},serviceWorkers:'block'}),page=await context.newPage(),errors=[];
  page.setDefaultTimeout(10000);page.on('pageerror',error=>errors.push(error.message));
  await context.addCookies([{name:'picker-case',value:name,url:fixture.origin}]);
  const control=async data=>{const response=await context.request.post(fixture.origin+'/fixture/control',{data});assert(response.ok());};
  const server=async()=>await(await context.request.get(fixture.origin+'/fixture/state')).json();
  const ready=()=>page.waitForFunction(()=>document.querySelector('.connection-status')?.classList.contains('is-connected')&&!document.querySelector('#pot-composer')?.disabled);
  const saved=()=>page.waitForFunction(()=>document.querySelector('.draft-storage-status')?.textContent==='Draft saved on this device');
  const choose=async(name='synthetic.png')=>{await page.getByRole('button',{name:'Add attachment',exact:true}).click();const chooser=page.waitForEvent('filechooser');await page.getByRole('menuitem',{name:'Images',exact:true}).click();await(await chooser).setFiles({...file,name});};
  const uploaded=()=>page.waitForFunction(()=>document.querySelector('.attachment-card')&&!document.querySelector('.attachment-card progress,.attachment-card .attachment-error'));
  const switchOwner=async owner=>{await page.locator('.titlebar .narrow-navigation').click();await page.getByRole('button',{name:'Synthetic '+owner,exact:true}).click();await ready();};
  const disk=()=>page.evaluate(async()=>{const {createIndexedStore,idbRequest}=await import('/state/indexed-store.mjs');const db=createIndexedStore();try{return await db.transaction(['composerDrafts','composerFiles'],'readonly',async tx=>({drafts:await idbRequest(tx.objectStore('composerDrafts').getAll()),files:(await idbRequest(tx.objectStore('composerFiles').getAll())).map(({blob,...meta})=>({...meta,size:blob.size,type:blob.type}))}));}finally{db.close();}});
  const reference=()=>page.evaluate(()=>{const node=document.querySelector('.message[data-message-id="alpha-source"] .markdown')||document.querySelector('.message[data-message-id="alpha-source"] p');const range=document.createRange();range.selectNodeContents(node);getSelection().removeAllRanges();getSelection().addRange(range);document.dispatchEvent(new KeyboardEvent('keyup',{key:'Shift',bubbles:true}));});
  try{
   await control({references:true});await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha&conversation=main');await ready();if(!baseline)await saved();
   await run({page,context,control,server,ready,saved,choose,uploaded,switchOwner,disk,reference});
   assert.deepEqual(errors,[]);results.push({name,ok:true,server:await server(),disk:baseline?null:await disk()});
  }catch(error){results.push({name,ok:false,error:error.stack,errors,body:await page.locator('body').innerText().catch(()=>''),server:await server(),disk:await disk().catch(()=>null)});throw Object.assign(Error(name+': '+error.message),{results});}
  finally{await context.close();}
 };
 try{
  await scenario('owners-reload',async({page,control,ready,saved,choose,uploaded,switchOwner,disk,reference})=>{
   await page.locator('#pot-composer').fill('Alpha persistent draft');await reference();await page.waitForFunction(()=>document.querySelector('.composer-references')?.textContent.includes('Synthetic reference source'));
   await choose();await uploaded();if(!baseline)await saved();
   if(baseline){await page.reload();await ready();assert.equal(await page.locator('#pot-composer').inputValue(),'');}
   else{
    const a=await disk();assert.equal(a.files.length,1);assert.equal(a.drafts[0].references[0].message_id,'alpha-source');
    await switchOwner('beta');await saved();await page.locator('#pot-composer').fill('Beta independent draft');await choose('beta.png');await uploaded();await saved();
    await page.reload();await ready();await saved();await switchOwner('beta');await saved();assert.equal(await page.locator('#pot-composer').inputValue(),'Beta independent draft');assert.equal(await page.locator('.attachment-name').innerText(),'beta.png');
    await switchOwner('alpha');await saved();await uploaded();assert.equal(await page.locator('#pot-composer').inputValue(),'Alpha persistent draft');assert.equal(await page.locator('.attachment-name').innerText(),'synthetic.png');assert.match(await page.locator('.composer-references').innerText(),/Synthetic reference source/);
   }
   if(captureDir){await mkdir(captureDir,{recursive:true});await page.screenshot({path:captureDir+'/draft-reload-'+(baseline?'before':'after')+'.png'});}
   if(!baseline){
    await page.getByRole('button',{name:'Remove attachment',exact:true}).click();await saved();assert.equal((await disk()).files.length,1,'Only alpha original removed; beta survives');
    await control({conversation:'replacement'});await page.reload();await ready();await saved();assert.equal(await page.locator('#pot-composer').inputValue(),'');assert.equal(await page.locator('.composer-references').innerText(),'');assert.equal((await disk()).drafts.find(row=>row.key.includes('alpha')).conversation_id,'replacement');
   }
  });
  if(baseline)return results;
  await scenario('failed-upload',async({page,control,saved,choose,ready,disk,uploaded})=>{
   await control({uploadFailure:true});await choose();await page.getByRole('button',{name:'Retry',exact:true}).waitFor();await saved();const before=(await disk()).drafts[0].attachments[0].attachment.id;
   await page.reload();await ready();await saved();assert.equal(await page.locator('.attachment-card').count(),1);await page.getByRole('button',{name:'Retry',exact:true}).waitFor();
   await control({uploadFailure:false});await page.getByRole('button',{name:'Retry',exact:true}).click();await uploaded();await saved();assert.equal((await disk()).drafts[0].attachments[0].attachment.id,before);
  });
  await scenario('waiting-upload',async({page,control,saved,choose,ready,disk,uploaded})=>{
   await control({permission:false,directory:true});await page.waitForFunction(()=>document.querySelector('.connection-status')?.classList.contains('is-connected'));await page.waitForTimeout(150);await choose();await saved();assert.match(await page.locator('.attachment-hint').innerText(),/Waiting/);const before=(await disk()).files[0].id;
   await page.reload();await ready();await saved();assert.equal(await page.locator('.attachment-card img').evaluate(node=>node.naturalWidth),1);await control({permission:true,directory:true});await uploaded();await saved();assert.equal((await disk()).files[0].id,before);
  });
  await scenario('accepted-new-edits',async({page,control,saved,choose,uploaded,server,disk,ready,reference})=>{
   await page.locator('#pot-composer').fill('Submitted text');await reference();await choose();await uploaded();await saved();await control({holdSend:true});await page.getByRole('button',{name:'Send',exact:true}).click();
   await page.waitForFunction(()=>document.querySelector('.connection-status')?.textContent.includes('Confirming'));await page.locator('#pot-composer').fill('Text written during submit');await page.locator('.message-reference-remove').click();await reference();await page.waitForFunction(()=>document.querySelector('.composer-references .message-reference'));await choose('new.png');await uploaded();await saved();
   const first=(await server()).trace.find(row=>row.kind==='post');assert(first);assert.equal((await server()).trace.filter(row=>row.kind==='receipt').length,0,'Normal pending POST has no receipt reads');
   await control({holdSend:false,releaseSends:true});await ready();await saved();assert.equal(await page.locator('#pot-composer').inputValue(),'Text written during submit');assert.equal(await page.locator('.composer-references .message-reference').count(),1);assert.equal(await page.locator('.attachment-card').count(),1);assert.equal((await disk()).files.length,1);assert.equal((await disk()).drafts[0].pending,null);
   await page.reload();await ready();await saved();await uploaded();assert.equal(await page.locator('#pot-composer').inputValue(),'Text written during submit');assert.equal(await page.locator('.attachment-name').innerText(),'new.png');assert.equal((await server()).trace.filter(row=>row.kind==='post').length,1);
  });
  await scenario('reload-pending-accepted',async({page,control,saved,choose,uploaded,server,disk,ready})=>{
   await page.locator('#pot-composer').fill('Accepted before reload');await choose();await uploaded();await control({holdSend:true});await page.getByRole('button',{name:'Send',exact:true}).click();await page.waitForTimeout(100);await saved();const prior=await disk();assert(prior.drafts[0].pending);assert.equal((await server()).trace.filter(row=>row.kind==='post').length,1);
   await control({receipt:true});await page.reload();await ready();await saved();assert.equal(await page.locator('#pot-composer').inputValue(),'');assert.equal(await page.locator('.attachment-card').count(),0);assert.equal((await disk()).files.length,0);const evidence=await server();assert.equal(evidence.trace.filter(row=>row.kind==='post').length,1);assert(evidence.trace.filter(row=>row.kind==='receipt').every(row=>row.query.request_id===prior.drafts[0].pending.request.request_id));
  });
  await scenario('rejected-and-unknown',async({page,control,saved,server,disk,ready})=>{
   await page.locator('#pot-composer').fill('Keep on rejection');await control({rejectSend:true});await page.getByRole('button',{name:'Send',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.connection-status')?.textContent.includes('not accepted'));await saved();assert.equal(await page.locator('#pot-composer').inputValue(),'Keep on rejection');assert.equal((await disk()).drafts[0].pending,null);
   await control({rejectSend:false,holdSend:true,acceptSend:false});await page.getByRole('button',{name:'Send',exact:true}).click();await page.waitForTimeout(100);await saved();const id=(await disk()).drafts[0].pending.request.request_id;
   await page.reload();await saved();await page.waitForFunction(()=>document.querySelector('.connection-status')?.textContent.includes('Confirming')||document.querySelector('.connection-status')?.textContent.includes('may have been accepted'));assert.equal(await page.locator('#pot-composer').inputValue(),'Keep on rejection');assert((await disk()).drafts[0].pending);const evidence=await server();assert.equal(evidence.trace.filter(row=>row.kind==='post').length,2);assert(evidence.trace.filter(row=>row.kind==='receipt').every(row=>row.query.request_id===id));
  });
  await scenario('quota-retry-and-accept-cleanup',async({page,control,saved,server,disk,ready})=>{
   await page.evaluate(()=>{const put=IDBObjectStore.prototype.put;window.draftPut=put;IDBObjectStore.prototype.put=function(...args){if(this.name==='composerDrafts'&&window.failDraftWrites)throw new DOMException('Synthetic draft quota','QuotaExceededError');return put.apply(this,args);};window.failDraftWrites=true;});
   await page.locator('#pot-composer').fill('Never pretend saved');await page.locator('.draft-storage-status.inline-error').waitFor();assert.match(await page.locator('.draft-storage-status').innerText(),/not saved/);await page.getByRole('button',{name:'Send',exact:true}).click();await page.waitForTimeout(100);assert.equal((await server()).trace.filter(row=>row.kind==='post').length,0);
   await page.evaluate(()=>dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}})));assert.match(await page.locator('.draft-storage-status').innerText(),/草稿未保存/);assert.equal(await page.locator('.draft-storage-status').getByRole('button',{name:'重试',exact:true}).count(),1);await page.evaluate(()=>dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'en'}})));
   await page.locator('#pot-composer').fill('Latest after failed save');await page.evaluate(()=>window.failDraftWrites=false);await page.locator('.draft-storage-status').getByRole('button',{name:'Retry',exact:true}).click();await saved();assert.equal((await disk()).drafts[0].text,'Latest after failed save');
   await control({holdSend:true});await page.getByRole('button',{name:'Send',exact:true}).click();await page.waitForTimeout(100);await saved();await page.evaluate(()=>window.failDraftWrites=true);await control({releaseSends:true});await page.locator('.draft-storage-status.inline-error').waitFor();assert((await disk()).drafts[0].pending,'Disk retains a safe request fence when cleanup fails');
   await page.locator('#pot-composer').fill('New input after accepted cleanup failure');await page.evaluate(()=>window.failDraftWrites=false);await page.locator('.draft-storage-status').getByRole('button',{name:'Retry',exact:true}).click();await saved();assert.equal((await disk()).drafts[0].text,'New input after accepted cleanup failure');assert.equal((await disk()).drafts[0].pending,null);
   await page.reload();await ready();await saved();assert.equal(await page.locator('#pot-composer').inputValue(),'New input after accepted cleanup failure');assert.equal((await server()).trace.filter(row=>row.kind==='post').length,1);
  });
  await scenario('accepted-projection-failure',async({page,saved,server,disk,ready})=>{
   await page.evaluate(()=>{const put=IDBObjectStore.prototype.put;IDBObjectStore.prototype.put=function(...args){if(this.name==='projections'&&window.failProjectionWrites)throw new DOMException('Synthetic projection quota','QuotaExceededError');return put.apply(this,args);};window.failProjectionWrites=true;});
   await page.locator('#pot-composer').fill('Business acceptance precedes projection save');await page.getByRole('button',{name:'Send',exact:true}).click();await page.locator('.connection-status').getByRole('button',{name:'Retry',exact:true}).waitFor();await saved();assert.equal(await page.locator('#pot-composer').inputValue(),'');assert.equal((await disk()).drafts[0].pending,null);assert.equal((await server()).trace.filter(row=>row.kind==='receipt').length,0);
   await page.locator('#pot-composer').fill('New draft during projection failure');await saved();await page.evaluate(()=>window.failProjectionWrites=false);await page.locator('.connection-status').getByRole('button',{name:'Retry',exact:true}).click();await ready();assert.equal((await server()).trace.filter(row=>row.kind==='post').length,1);assert.equal((await server()).trace.filter(row=>row.kind==='receipt').length,0);
   await page.reload();await ready();await saved();assert.equal(await page.locator('#pot-composer').inputValue(),'New draft during projection failure');
  });
  await scenario('late-load-files-overflow',async({page,context,saved,uploaded,disk})=>{
   await page.getByRole('button',{name:'Add attachment',exact:true}).click();const picker=page.waitForEvent('filechooser');await page.getByRole('menuitem',{name:'Images',exact:true}).click();await(await picker).setFiles(Array.from({length:8},(_,i)=>({...file,name:'saved-'+i+'.png'})));await uploaded();await saved();
   await context.addInitScript(()=>{
    if(localStorage.getItem('holdDraftRead')!=='yes')return;
    const complete=Object.getOwnPropertyDescriptor(IDBTransaction.prototype,'oncomplete');
    Object.defineProperty(IDBTransaction.prototype,'oncomplete',{...complete,set(handler){complete.set.call(this,function(event){if(localStorage.getItem('holdDraftRead')==='yes'&&this.mode==='readonly'&&this.objectStoreNames.contains('composerDrafts')){window.releaseDraftRead=()=>handler.call(this,event);return;}return handler.call(this,event);});}});
   });
   await page.evaluate(()=>localStorage.setItem('holdDraftRead','yes'));await page.reload();await page.waitForFunction(()=>!!window.releaseDraftRead);
   await page.evaluate(()=>{
    for(const type of ['paste','drop']){const event=new Event(type,{bubbles:true,cancelable:true}),data=new DataTransfer();data.items.add(new File(['new'],type+'.png',{type:'image/png'}));Object.defineProperty(event,type==='paste'?'clipboardData':'dataTransfer',{value:data});document.querySelector(type==='paste'?'#pot-composer':'.composer').dispatchEvent(event);}
   });
   assert.equal(await page.locator('.attachment-card').count(),2);
   await page.evaluate(()=>{localStorage.removeItem('holdDraftRead');window.releaseDraftRead();});await saved();await uploaded();await saved();
   assert.equal(await page.locator('.attachment-card').count(),10);assert.equal((await disk()).files.length,10);assert.match(await page.locator('.attachment-hint').innerText(),/All files are kept/);assert(await page.getByRole('button',{name:'Send',exact:true}).isDisabled());
   if(captureDir){await mkdir(captureDir,{recursive:true});await page.screenshot({path:captureDir+'/restored-attachment-limit-after.png'});}
   await page.reload();await saved();await uploaded();assert.equal(await page.locator('.attachment-card').count(),10);assert.equal((await disk()).files.length,10);
   await page.getByRole('button',{name:'Remove attachment',exact:true}).first().click();await page.getByRole('button',{name:'Remove attachment',exact:true}).first().click();await saved();
   assert.equal((await disk()).files.length,8);assert(await page.getByRole('button',{name:'Send',exact:true}).isEnabled());
  });
  for(const committedPost of [false,true])await scenario(committedPost?'explicit-original-duplicate':'preflight-before-post',async({page,control,saved,choose,uploaded,server,disk,reference})=>{
   await page.locator('#pot-composer').fill('Original fixed snapshot');await reference();await choose('original.png');await uploaded();await saved();
   if(committedPost)await control({holdSend:true});
   else await page.evaluate(()=>{
    const complete=Object.getOwnPropertyDescriptor(IDBTransaction.prototype,'oncomplete'),put=IDBObjectStore.prototype.put;
    Object.defineProperty(IDBTransaction.prototype,'oncomplete',{...complete,set(handler){complete.set.call(this,function(event){if(this.__holdPending){window.pendingCommitted=true;return;}return handler.call(this,event);});}});
    IDBObjectStore.prototype.put=function(value,...args){if(this.name==='composerDrafts'&&value.pending)this.transaction.__holdPending=true;return put.call(this,value,...args);};
   });
   await page.getByRole('button',{name:'Send',exact:true}).click();
   if(committedPost){await page.waitForFunction(()=>document.querySelector('.connection-status')?.textContent.includes('Confirming'));await saved();}
   else await page.waitForFunction(()=>window.pendingCommitted);
   const original=(await disk()).drafts[0].pending.request;
   assert.equal((await server()).trace.filter(row=>row.kind==='post').length,committedPost?1:0,'Snapshot committed before transport starts');
   await page.reload();await saved();await uploaded();
   await page.locator('#pot-composer').fill('New editor contents after reload');await page.locator('.message-reference-remove').click();await reference();await choose('new-after-reload.png');await uploaded();await saved();
   const retry=page.getByRole('button',{name:'Check and retry',exact:true});await retry.waitFor({timeout:30000});
   const bounds=await retry.boundingBox();assert(bounds.x>=0&&bounds.x+bounds.width<=390,'Recovery action remains fully visible on a phone');
   assert.equal((await server()).trace.filter(row=>row.kind==='post').length,committedPost?1:0,'Reload and automatic receipt checks never POST');
   await page.evaluate(()=>dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}})));
   const zhRetry=page.getByRole('button',{name:'查证并重试',exact:true});assert.match(await zhRetry.getAttribute('title'),/相同请求 ID/);
   await page.evaluate(()=>dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'en'}})));
   if(captureDir&&!committedPost){await mkdir(captureDir,{recursive:true});await page.screenshot({path:captureDir+'/preflight-recovery-after.png'});}
   await control({holdSend:false});await retry.click();await page.waitForFunction(()=>document.querySelector('.connection-status')?.classList.contains('is-connected'));await saved();
   const evidence=await server(),posts=evidence.trace.filter(row=>row.kind==='post'),current=await disk();
   assert.equal(posts.length,committedPost?2:1);assert(posts.every(row=>JSON.stringify(row.body)===JSON.stringify(posts[0].body)&&row.body.request_id===original.request_id&&row.owner==='alpha'));
   assert.equal(evidence.rows.length,1,'Same-ID replay creates one accepted row');assert.equal(evidence.trace.findLast(row=>row.kind==='post-response').duplicate,committedPost);
   assert.equal(current.drafts[0].pending,null);assert.equal(current.drafts[0].text,'New editor contents after reload');assert.equal(current.drafts[0].references.length,1);assert.equal(current.files.length,1);assert.equal(current.drafts[0].attachments[0].attachment.name,'new-after-reload.png');
   assert.equal(posts[0].body.text,'Original fixed snapshot');assert.equal(posts[0].body.attachments[0].name,'original.png');
  });
  return results;
 }finally{await fixture.close();}
}

if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){
 const source=await sourceServer({root:process.env.POT_DRAFT_SOURCE||process.cwd()}),browser=await launchBrowser();
 try{const results=await assistantDraftsBrowser(browser,{sourceOrigin:source.origin,captureDir:process.env.POT_DRAFT_CAPTURES,baseline:process.env.POT_DRAFT_BASELINE==='1'});console.log(JSON.stringify(results,null,2));if(process.env.POT_DRAFT_REPORT)await writeFile(process.env.POT_DRAFT_REPORT,JSON.stringify(results,null,2)+'\n');}
 catch(error){console.error(error.stack);if(process.env.POT_DRAFT_REPORT)await writeFile(process.env.POT_DRAFT_REPORT,JSON.stringify(error.results||{error:error.stack},null,2)+'\n');process.exitCode=1;}
 finally{await browser.close();await source.close();}
}
