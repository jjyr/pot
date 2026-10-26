import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {attachmentPickerFixture} from './attachment-picker-fixture.mjs';

// Shared production UI, HTTP adapter, SSE and IDB; every identity and file is
// synthetic. No model is invoked. A reference alone retains existing send rules.
export async function composerContinuationBrowser(browser,{sourceOrigin,baseline=false,captureDir}={}){
 const fixture=await attachmentPickerFixture(sourceOrigin),results=[];
 try{
  for(const phone of [false,true]){
   const viewport=phone?{width:390,height:852}:{width:1280,height:800};
   const context=await browser.newContext({viewport,isMobile:phone,hasTouch:phone}),page=await context.newPage(),errors=[];let finalEvidence;
   const name='continuation-'+(phone?'phone':'desktop');
   await context.addCookies([{name:'picker-case',value:name,url:fixture.origin}]);
   page.on('pageerror',error=>errors.push(error.message));
   const control=async data=>{const response=await context.request.post(fixture.origin+'/fixture/control',{data});assert(response.ok());};
   const evidence=async()=>await(await context.request.get(fixture.origin+'/fixture/state')).json();
   const continuation={user_message_id:'original-input',attempt_id:'original-attempt',recovery_required:true};
   const source={id:'synthetic-source',role:'assistant',status:'complete',text:'Saved synthetic progress. Select this text to reference it.'};
   const input=page.locator('#pot-composer'),main=page.locator('.send-button');
   const ready=()=>page.waitForFunction(()=>document.querySelector('.connection-status')?.classList.contains('is-connected'));
   const play=async()=>{await page.waitForFunction(()=>document.querySelector('.send-button')?.getAttribute('aria-label')==='Continue task'&&!document.querySelector('.send-button').disabled);assert(await main.isEnabled());};
   const select=()=>page.evaluate(()=>{const source=document.querySelector('[data-reference-source]'),range=document.createRange();range.selectNodeContents(source);getSelection().removeAllRanges();getSelection().addRange(range);source.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}));source.dispatchEvent(new PointerEvent('pointerup',{bubbles:true}));});
   try{
    await control({continuation,messages:[source]});
    await page.goto(fixture.origin+'/?locale=en&assistant_id=alpha');await ready();
    if(baseline)await page.locator('.turn-continuation').waitFor();else await play();
    if(captureDir){await page.waitForFunction(()=>!document.querySelector('.draft-storage-status')||document.querySelector('.draft-storage-status').textContent==='Draft saved on this device');await page.locator('[data-reference-source]').waitFor({state:'visible'});await page.evaluate(()=>document.fonts.ready);await mkdir(captureDir,{recursive:true});await page.screenshot({path:captureDir+'/'+name+'-'+(baseline?'before':'after')+'.png'});}
    if(baseline){results.push({viewport,baseline:true});continue;}
    assert.equal(await page.locator('.turn-continuation').count(),0);
    assert.equal(await page.locator('.turn-continuation-error').isVisible(),false);
    assert.equal(await main.locator('path').getAttribute('d'),'m8 5 11 7-11 7V5z');
    await control({running:true,publish:true});await page.waitForFunction(()=>document.querySelector('.send-button').getAttribute('aria-label')==='Stop');
    await control({running:false,publish:true});await play();
    await control({permission:false,directory:true});await page.waitForFunction(()=>document.querySelector('.send-button').getAttribute('aria-label')==='Continue task'&&document.querySelector('.send-button').disabled);
    await control({permission:true,directory:true});await play();
    await input.fill('New guidance');assert.equal(await main.getAttribute('aria-label'),'Send');assert(await main.isEnabled());
    await input.fill('   \n');await play();
    // Restored drafts use these same state selectors; no separate resume draft.
    await input.fill('Restored synthetic draft');assert.equal(await main.getAttribute('aria-label'),'Send');
    await input.fill('');await play();
    await select();assert.equal(await page.locator('.composer-references .message-reference').count(),1);
    assert.equal(await main.getAttribute('aria-label'),'Send');assert(await main.isDisabled());
    await page.locator('.composer-references .message-reference-remove').click();await play();
    for(const [menu,file] of [['Images',{name:'synthetic.png',mimeType:'image/png',buffer:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP438DwHwAGgAJ/EEwb4QAAAABJRU5ErkJggg==','base64')}],['PDF',{name:'synthetic.pdf',mimeType:'application/pdf',buffer:Buffer.from('%PDF-1.4 synthetic')}]] ){
     await page.getByRole('button',{name:'Add attachment',exact:true}).click();
     const chooser=page.waitForEvent('filechooser');await page.getByRole('menuitem',{name:menu,exact:true}).click();await(await chooser).setFiles(file);
     await page.waitForFunction(()=>document.querySelector('.attachment-card')&&!document.querySelector('.attachment-card progress'));
     assert.equal(await main.getAttribute('aria-label'),'Send');assert(await main.isEnabled());
     assert.equal((await evidence()).trace.filter(t=>t.kind==='continue').length,0);
     await page.getByRole('button',{name:'Remove attachment',exact:true}).click();await play();
    }
    // Pending activation captures the original attempt and never sends a message.
    await control({holdContinue:true});await main.click();
    await page.waitForFunction(()=>document.querySelector('.send-button').getAttribute('aria-busy')==='true');
    await page.evaluate(()=>{document.querySelector('.send-button').click();document.querySelector('.send-button').click();});
    assert(await main.isDisabled());assert.equal(await main.getAttribute('aria-label'),'Restoring progress…');
    await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}})));
    assert.equal(await main.getAttribute('aria-label'),'正在恢复进度…');
    await input.fill('Draft entered while restoring');assert(await main.isDisabled());
    await control({continueError:{status:409,code:'turn_recovery_required',message:'Synthetic tool result is unconfirmed'},releaseContinue:true});
    await page.getByText('部分工具结果或进程停止状态尚未确认。Pot 已保留进度，请查看详情后重试。',{exact:true}).waitFor();
    assert.equal(await input.inputValue(),'Draft entered while restoring');assert.equal(await main.getAttribute('aria-label'),'发送');
    await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'en'}})));
    await input.fill('');await play();
    // A retry keeps the same command identity across unresolved/403/stale errors.
    for(const error of [{status:403,code:'forbidden',message:'Synthetic permission denied'},{status:409,code:'turn_continuation_unavailable',message:'Synthetic stale attempt'}]){
     await control({holdContinue:false,continueError:error});await main.click();
     await page.locator('.turn-continuation-error').waitFor();
     await play();assert.equal(await page.locator('.turn-continuation-error').getAttribute('role'),'alert');
     assert.match(await page.locator('.turn-continuation-error').innerText(),error.code==='forbidden'?/Could not continue/:/task has changed/);
    }
    let trace=(await evidence()).trace.filter(t=>t.kind==='continue');assert.equal(trace.length,3);assert.equal(new Set(trace.map(t=>t.body.request_id)).size,1);
    assert(trace.every(t=>t.owner==='alpha'&&t.space==='local'&&t.path==='/api/v1/turn/continue'&&t.body.user_message_id==='original-input'&&t.body.attempt_id==='original-attempt'));
    // Accepted receipts hide play even before a newer stream frame arrives.
    await control({continueError:null});await main.click();await page.waitForFunction(()=>document.querySelector('.send-button').getAttribute('aria-label')==='Send');assert(await main.isDisabled());
    await control({continuation:null,publish:true});await ready();assert.equal(await main.getAttribute('aria-label'),'Send');assert(await main.isDisabled());
    // Summary continuation gets a distinct attempt, then late acceptance is fenced.
    await control({continuation:{...continuation,attempt_id:'summary-attempt',summary:true},publish:true,holdContinue:true});
    await page.waitForFunction(()=>document.querySelector('.send-button').getAttribute('aria-label')==='Restore conversation');await main.click();
    await page.waitForFunction(async()=> (await(await fetch('/fixture/state')).json()).trace.filter(t=>t.kind==='continue').length===5);
    if(phone)await page.locator('.titlebar .narrow-navigation').click();
    await page.getByRole('button',{name:'Synthetic beta',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.assistant-profile-trigger')?.textContent.includes('Synthetic beta'));await ready();
    await input.fill('Beta draft survives late restoration');await control({releaseContinue:true});
    await page.waitForFunction(()=>document.querySelector('.send-button').getAttribute('aria-label')==='Send');
    assert.equal(await input.inputValue(),'Beta draft survives late restoration');await page.waitForFunction(()=>!document.querySelector('.send-button').disabled);assert.equal(await page.locator('.turn-continuation-error').isVisible(),false);
    await input.fill('');assert(await main.isDisabled());
    await input.fill('New message instead of restoration');await main.click();
    await page.waitForFunction(async()=> (await(await fetch('/fixture/state')).json()).rows.length===1);
    await page.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
    assert.equal((await evidence()).trace.filter(t=>t.kind==='continue').length,5);
    // A running turn retains Stop and normal guidance, even with continuation data.
    await control({running:true,publish:true});await page.waitForFunction(()=>document.querySelector('.send-button').getAttribute('aria-label')==='Stop');await main.click();
    await page.waitForFunction(async()=> (await(await fetch('/fixture/state')).json()).trace.some(t=>t.kind==='cancel'));
    await input.fill('continue');assert.equal(await main.getAttribute('aria-label'),'Send guidance');await main.click();
    await page.waitForFunction(async()=> (await(await fetch('/fixture/state')).json()).rows.some(row=>row.body.user_message_id==='active-turn'&&row.body.text==='continue'));
    await page.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
    const final=finalEvidence=await evidence();assert.equal(final.trace.filter(t=>t.kind==='continue').length,5);assert.equal(final.rows.find(row=>row.body.user_message_id==='active-turn').body.text,'continue');assert.equal(final.trace.find(t=>t.path==='/api/v1/turn/steer').body.user_message_id,'active-turn');
    assert.equal(final.trace.find(t=>t.kind==='post').path,'/api/v1/messages');assert.equal(final.rows.find(row=>!row.body.user_message_id).body.text,'New message instead of restoration');
    assert.deepEqual(errors,[]);results.push({viewport,ok:true,server:final});
   }catch(error){throw Error(error.message+'\n'+JSON.stringify({viewport,errors,finalEvidence,ui:await page.evaluate(()=>({input:document.querySelector('#pot-composer')?.value,main:document.querySelector('.send-button')?.outerHTML,status:document.querySelector('.connection-status')?.outerHTML})),body:await page.locator('body').innerText(),server:await evidence()}),{cause:error});}finally{await context.close();}
  }
 }finally{await fixture.close();}
 return results;
}

if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){
 const source=await sourceServer({root:process.env.POT_CONTINUATION_SOURCE||process.cwd()}),browser=await launchBrowser();
 try{const results=await composerContinuationBrowser(browser,{sourceOrigin:source.origin,baseline:process.env.POT_CONTINUATION_BASELINE==='1',captureDir:process.env.POT_CONTINUATION_CAPTURES});console.log(JSON.stringify(results,null,2));if(process.env.POT_CONTINUATION_REPORT)await writeFile(process.env.POT_CONTINUATION_REPORT,JSON.stringify(results,null,2)+'\n');}
 catch(error){console.error(error.stack);process.exitCode=1;}
 finally{await browser.close();await source.close();}
}
