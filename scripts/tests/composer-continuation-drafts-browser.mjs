import {waitForDraftSaved} from './draft-persistence-browser.mjs';
import assert from 'node:assert/strict';
import {writeFile} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import {attachmentPickerFixture} from './attachment-picker-fixture.mjs';
import {sourceServer,launchBrowser} from './browser-harness.mjs';

const file={name:'saved.png',mimeType:'image/png',buffer:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP438DwHwAGgAJ/EEwb4QAAAABJRU5ErkJggg==','base64')};
// Pause the real IDB completion boundary, after its data committed. The UI and
// production HTTP/SSE adapter stay unmodified; only synthetic local IO is held.
export function draftRaceBoundaries(){
 const complete=Object.getOwnPropertyDescriptor(IDBTransaction.prototype,'oncomplete'),get=IDBObjectStore.prototype.get,put=IDBObjectStore.prototype.put;
 window.holdDraftRead=localStorage.getItem('continuationDraftRead')==='hold';
 window.failDraftRead=localStorage.getItem('continuationDraftRead')==='fail';
 Object.defineProperty(IDBTransaction.prototype,'oncomplete',{...complete,set(handler){complete.set.call(this,function(event){
  if(this.mode==='readonly'&&this.objectStoreNames.contains('composerDrafts')&&window.holdDraftRead){window.releaseDraftRead=()=>{window.holdDraftRead=false;localStorage.removeItem('continuationDraftRead');handler.call(this,event);};return;}
  if(this.__pendingWrite&&window.holdPreparing){window.releasePreparing=()=>{window.holdPreparing=false;handler.call(this,event);};return;}
  return handler.call(this,event);
 });}});
 IDBObjectStore.prototype.get=function(...args){if(this.name==='composerDrafts'&&window.failDraftRead)throw new DOMException('Synthetic draft read failure','UnknownError');return get.apply(this,args);};
 IDBObjectStore.prototype.put=function(value,...args){if(this.name==='composerDrafts'&&value.pending)this.transaction.__pendingWrite=true;return put.call(this,value,...args);};
}

export async function composerContinuationDraftsBrowser(browser,{sourceOrigin}={}){
 const fixture=await attachmentPickerFixture(sourceOrigin),results=[];
 const continuation={user_message_id:'original-input',attempt_id:'draft-race-attempt',recovery_required:true};
 try{
  for(const phone of [false,true])for(const scenario of ['delayed-draft','failed-load','preparing-send','restored-send-fence']){
   const viewport=phone?{width:390,height:852}:{width:1280,height:800},name=scenario+'-'+(phone?'phone':'desktop');
   const context=await browser.newContext({viewport,isMobile:phone,hasTouch:phone,serviceWorkers:'block'}),page=await context.newPage(),errors=[],marks=[];
   page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message));
   await context.addCookies([{name:'picker-case',value:name,url:fixture.origin}]);await context.addInitScript(draftRaceBoundaries);
   const control=async data=>{assert((await context.request.post(fixture.origin+'/fixture/control',{data})).ok());};
   const evidence=async()=>await(await context.request.get(fixture.origin+'/fixture/state')).json();
   const input=page.locator('#pot-composer'),main=page.locator('.send-button');
   const saved=options=>waitForDraftSaved(page,options);
   const play=()=>page.waitForFunction(()=>document.querySelector('.send-button')?.getAttribute('aria-label')==='Continue task'&&!document.querySelector('.send-button').disabled);
   const noCommands=async()=>assert.equal((await evidence()).trace.filter(row=>['post','continue'].includes(row.kind)).length,0);
   const sample=()=>page.evaluate(()=>({label:document.querySelector('.send-button')?.getAttribute('aria-label'),disabled:document.querySelector('.send-button')?.disabled,text:document.querySelector('#pot-composer')?.value,status:document.querySelector('.draft-storage-status')?.textContent,files:[...document.querySelectorAll('.attachment-name')].map(node=>node.textContent),references:document.querySelectorAll('.composer-references .message-reference').length}));
   const uploaded=()=>page.waitForFunction(()=>document.querySelector('.attachment-card')&&!document.querySelector('.attachment-card progress,.attachment-card .attachment-error'));
   const choose=async()=>{await page.getByRole('button',{name:'Add attachment',exact:true}).click();const chooser=page.waitForEvent('filechooser');await page.getByRole('menuitem',{name:'Images',exact:true}).click();await(await chooser).setFiles(file);await uploaded();};
   const reference=async()=>{await page.waitForFunction(()=>{const h=document.querySelector('.history');return h&&getComputedStyle(h).visibility==='visible'&&h.querySelector('[data-reference-source]');});await page.evaluate(()=>{const node=document.querySelector('.history [data-reference-source]'),range=document.createRange();range.selectNodeContents(node);getSelection().removeAllRanges();getSelection().addRange(range);node.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}));node.dispatchEvent(new PointerEvent('pointerup',{bubbles:true}));});await page.waitForFunction(()=>document.querySelectorAll('.composer-references .message-reference').length===1);};
   const switchOwner=async owner=>{if(phone)await page.locator('.titlebar .narrow-navigation').click();await page.getByRole('button',{name:'Synthetic '+owner,exact:true}).click();await page.waitForFunction(owner=>document.querySelector('.assistant-profile-trigger')?.textContent.includes('Synthetic '+owner),owner);await saved();};
   const releaseLoad=()=>page.evaluate(()=>window.releaseDraftRead());
   try{
    await control({continuation,references:true});await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha');await saved();await play();
    if(scenario==='delayed-draft'){
     await input.fill('Saved Alpha text');await reference();await choose();await saved();
     await switchOwner('beta');await input.fill('Saved Beta draft');await saved();await switchOwner('alpha');
     await page.evaluate(()=>localStorage.setItem('continuationDraftRead','hold'));await page.reload();await page.waitForFunction(()=>!!window.releaseDraftRead);
     assert.equal(await input.inputValue(),'');assert.equal(await main.getAttribute('aria-label'),'Send');assert(await main.isDisabled());await page.evaluate(()=>document.querySelector('.send-button').click());await noCommands();marks.push({phase:'loading',ui:await sample()});
     await releaseLoad();await saved();await uploaded();assert.equal(await input.inputValue(),'Saved Alpha text');assert.equal(await main.getAttribute('aria-label'),'Send');assert.equal(await page.locator('.composer-references .message-reference').count(),1);await noCommands();
     await input.fill('');assert.equal(await main.getAttribute('aria-label'),'Send');await page.getByRole('button',{name:'Remove attachment',exact:true}).click();assert.equal(await main.getAttribute('aria-label'),'Send');assert(await main.isDisabled());await page.locator('.message-reference-remove').click();await play();await saved();marks.push({phase:'cleared-restored-draft',ui:await sample()});
     await control({holdContinue:true});await main.click();await page.waitForFunction(()=>document.querySelector('.send-button').getAttribute('aria-busy')==='true');
     await input.fill('New Alpha draft during continuation');await input.press('Enter');await page.evaluate(()=>document.querySelector('.send-button').click());assert(await main.isDisabled());
     await switchOwner('beta');assert.equal(await input.inputValue(),'Saved Beta draft');await input.fill('Fresh Beta draft');await control({releaseContinue:true});await saved();
     await page.waitForFunction(async()=> (await(await fetch('/fixture/state')).json()).trace.filter(row=>row.kind==='continue').length===1);
     assert.equal(await input.inputValue(),'Fresh Beta draft');assert.equal(await main.getAttribute('aria-label'),'Send');await switchOwner('alpha');assert.match(await input.inputValue(),/^New Alpha draft during continuation/);assert.equal(await main.getAttribute('aria-label'),'Send');assert.equal((await evidence()).rows.length,0);marks.push({phase:'late-continuation',ui:await sample()});
    }else if(scenario==='failed-load'){
     await input.fill('Saved text before failed load');await saved();await page.evaluate(()=>localStorage.setItem('continuationDraftRead','fail'));await page.reload();await page.locator('.draft-storage-status.inline-error').waitFor();
     assert.equal(await main.getAttribute('aria-label'),'Send');assert(await main.isDisabled());await page.evaluate(()=>document.querySelector('.send-button').click());await noCommands();
     await input.fill('New typing during failed load');await page.evaluate(()=>{window.failDraftRead=false;window.holdDraftRead=true;localStorage.setItem('continuationDraftRead','hold');});await page.locator('.draft-storage-status').getByRole('button',{name:'Retry',exact:true}).click();await page.waitForFunction(()=>!!window.releaseDraftRead);assert.equal(await main.getAttribute('aria-label'),'Send');assert(await main.isDisabled());await noCommands();
     await releaseLoad();await saved();assert.equal(await input.inputValue(),'New typing during failed load');assert(await main.isEnabled());await noCommands();await input.fill('');await play();marks.push({phase:'retried-load',ui:await sample()});
    }else if(scenario==='preparing-send'){
     await input.fill('Immutable Alpha submission');await reference();await choose();await saved();await control({holdSend:true});await page.evaluate(()=>window.holdPreparing=true);await main.click();await page.waitForFunction(()=>!!window.releasePreparing);await noCommands();
     await input.fill('');await page.locator('.message-reference-remove').click();await page.getByRole('button',{name:'Remove attachment',exact:true}).click();await page.evaluate(()=>document.querySelector('.send-button').click());assert(await main.isDisabled());await noCommands();marks.push({phase:'preparing-cleared',ui:await sample()});
     await switchOwner('beta');await input.fill('Beta while Alpha flush waits');await page.evaluate(()=>window.releasePreparing());
     await page.waitForFunction(async()=> (await(await fetch('/fixture/state')).json()).trace.filter(row=>row.kind==='post').length===1);let posts=(await evidence()).trace.filter(row=>row.kind==='post');assert.equal(posts[0].owner,'alpha');assert.equal(posts[0].path,'/api/v1/messages');assert.equal(posts[0].body.text,'Immutable Alpha submission');assert.equal(posts[0].body.references.length,1);assert.equal(posts[0].body.attachments.length,1);
     assert.equal((await evidence()).trace.filter(row=>row.kind==='receipt').length,0);await control({holdSend:false,releaseSends:true});await saved();assert.equal(await input.inputValue(),'Beta while Alpha flush waits');assert.equal((await evidence()).trace.filter(row=>row.kind==='continue').length,0);marks.push({phase:'captured-send-scope',ui:await sample()});
    }else{
     await input.fill('Fixed request before reload');await saved();await control({holdSend:true,acceptSend:false});await main.click();await page.waitForFunction(async()=> (await(await fetch('/fixture/state')).json()).trace.filter(row=>row.kind==='post').length===1);await saved({pending:true});const original=(await evidence()).trace.find(row=>row.kind==='post');
     await page.reload();await saved({pending:true});await input.fill('');assert(await main.isDisabled());await page.evaluate(()=>document.querySelector('.send-button').click());assert.equal((await evidence()).trace.filter(row=>row.kind==='continue').length,0);
     const retry=page.getByRole('button',{name:'Check and retry',exact:true});await retry.waitFor({timeout:30000});assert.equal((await evidence()).trace.filter(row=>row.kind==='post').length,1);await control({holdSend:false,acceptSend:true});await retry.click();await saved();await play();const posts=(await evidence()).trace.filter(row=>row.kind==='post');assert.equal(posts.length,2);assert.deepEqual(posts[1].body,original.body);assert.equal(posts[1].owner,'alpha');assert.equal((await evidence()).trace.filter(row=>row.kind==='continue').length,0);marks.push({phase:'send-fence-explicit-retry',ui:await sample()});
    }
    assert.deepEqual(errors,[]);results.push({name,viewport,ok:true,marks,server:await evidence()});
   }catch(error){results.push({name,viewport,ok:false,error:error.stack,errors,marks,ui:await sample(),server:await evidence()});throw Object.assign(Error(name+': '+error.message),{results});}
   finally{await context.close();}
  }
  return results;
 }finally{await fixture.close();}
}

if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){
 const source=await sourceServer(),browser=await launchBrowser();
 try{const results=await composerContinuationDraftsBrowser(browser,{sourceOrigin:source.origin});console.log(JSON.stringify(results,null,2));if(process.env.POT_CONTINUATION_REPORT)await writeFile(process.env.POT_CONTINUATION_REPORT,JSON.stringify(results,null,2)+'\n');}
 catch(error){console.error(error.stack);if(process.env.POT_CONTINUATION_REPORT)await writeFile(process.env.POT_CONTINUATION_REPORT,JSON.stringify(error.results||{error:error.stack},null,2)+'\n');process.exitCode=1;}
 finally{await browser.close();await source.close();}
}
