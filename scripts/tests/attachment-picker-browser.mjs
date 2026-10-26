import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {attachmentPickerFixture} from './attachment-picker-fixture.mjs';

const file={name:'synthetic.png',mimeType:'image/png',buffer:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP438DwHwAGgAJ/EEwb4QAAAABJRU5ErkJggg==','base64')};
export async function attachmentPickerBrowser(browser,{sourceOrigin,baseline=false,captureDir}={}){
 const fixture=await attachmentPickerFixture(sourceOrigin),results=[];
 const scenario=async(name,run,{clock=false}={})=>{
  const context=await browser.newContext({viewport:{width:390,height:852}}),page=await context.newPage(),errors=[];
  await context.addCookies([{name:'picker-case',value:name,url:fixture.origin}]);if(clock)await page.clock.install();
  await context.addInitScript(()=>{window.streams=[];const Native=EventSource;window.EventSource=class extends Native{constructor(url,options){super(url,options);window.streams.push(this);}};});
  page.on('pageerror',e=>errors.push(e.message));
  const control=async body=>{const r=await context.request.post(fixture.origin+'/fixture/control',{data:body});assert(r.ok());};
  const evidence=async()=>await(await context.request.get(fixture.origin+'/fixture/state')).json();
  const sample=()=>page.evaluate(()=>({cards:document.querySelectorAll('.attachment-card').length,hint:document.querySelector('.attachment-hint')?.textContent,disabled:document.querySelector('.attachment-plus')?.disabled,status:document.querySelector('.connection-status')?.textContent,sw:!!navigator.serviceWorker.controller}));
  const ready=()=>page.waitForFunction(()=>document.querySelector('.connection-status')?.classList.contains('is-connected')&&document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled,{},{timeout:12000});
  const choose=async()=>{await page.getByRole('button',{name:'Add attachment',exact:true}).click();const chooser=page.waitForEvent('filechooser');await page.getByRole('menuitem',{name:'Images',exact:true}).click();return chooser;};
  const uploaded=()=>page.waitForFunction(()=>document.querySelector('.attachment-card')&&!document.querySelector('.attachment-card progress')&&!document.querySelector('.attachment-card .attachment-error'));
  const switchOwner=async owner=>{await page.locator('.titlebar .narrow-navigation').click();await page.getByRole('button',{name:'Synthetic '+owner,exact:true}).click();await ready();};
  const visibility=value=>page.evaluate(value=>{window.visibility=value;Object.defineProperty(document,'visibilityState',{get:()=>window.visibility,configurable:true});document.dispatchEvent(new Event('visibilitychange'));if(value==='hidden'){window.dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}));window.dispatchEvent(new Event('freeze'));}else{window.dispatchEvent(new Event('resume'));window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));window.dispatchEvent(new Event('focus'));}},value);
  const marks=[];
  try{
   await page.goto(fixture.origin+'/?locale=en&assistant_id=alpha&conversation=main');await ready();await page.evaluate(()=>navigator.serviceWorker.ready);await page.waitForFunction(()=>!!navigator.serviceWorker.controller);
   await run({page,control,evidence,sample,ready,choose,uploaded,switchOwner,visibility,marks});assert.deepEqual(errors,[]);
   results.push({name,ok:true,marks,final:await sample(),server:await evidence()});
  }catch(error){results.push({name,ok:false,error:error.stack,errors,marks,final:await sample().catch(()=>null),body:await page.locator('body').innerText(),server:await evidence()});}
  finally{await context.close();}
 };
 try{
  await scenario('connecting-picker-return',async({page,control,evidence,sample,ready,choose,uploaded,visibility,marks})=>{
   const chooser=await choose();await visibility('hidden');await control({holdInitial:true});await page.clock.setSystemTime((await page.evaluate(()=>Date.now()))+60000);await visibility('visible');
   await page.waitForFunction(()=>window.streams.filter(s=>new URL(s.url).pathname==='/api/v1/events').length>=2&&!document.querySelector('.connection-status')?.classList.contains('is-connected'));
   await chooser.setFiles(file);await page.waitForFunction(()=>document.querySelector('input[type=file]')?.files.length===1||!!document.querySelector('.attachment-card'));
   marks.push({phase:'returned-connecting',ui:await sample()});assert.equal((await evidence()).trace.filter(t=>t.kind==='upload').length,0);assert.equal(await page.locator('.attachment-card').count(),baseline?0:1);
   if(captureDir){await mkdir(captureDir,{recursive:true});await page.screenshot({path:captureDir+'/picker-phone-'+(baseline?'before':'after')+'.png'});}
   await control({holdInitial:false,release:true});await ready();if(!baseline)await uploaded();
   marks.push({phase:'recovered',ui:await sample()});const uploads=(await evidence()).trace.filter(t=>t.kind==='upload');assert.equal(uploads.length,baseline?0:3);assert(uploads.every(t=>t.owner==='alpha'&&t.space==='local'));
  },{clock:true});
  await scenario('permission-picker-return',async({page,control,evidence,sample,choose,uploaded,marks})=>{
   const chooser=await choose();await control({permission:false,directory:true});
   await page.locator('#pot-composer').fill('Permission probe');await page.waitForFunction(()=>document.querySelector('.send-button').disabled);
   await chooser.setFiles(file);marks.push({phase:'permission-unavailable',ui:await sample()});assert.equal(await page.locator('.attachment-card').count(),baseline?0:1);assert.equal((await evidence()).trace.filter(t=>t.kind==='upload').length,0);
   await control({permission:true,directory:true});if(!baseline)await uploaded();else await page.waitForFunction(()=>!document.querySelector('.attachment-plus').disabled);
   marks.push({phase:'permission-restored',ui:await sample()});assert.equal((await evidence()).trace.filter(t=>t.kind==='upload').length,baseline?0:3);assert.equal((await evidence()).trace.filter(t=>t.kind==='sse-open'&&t.path==='/api/v1/events').length,1);
  });
  await scenario('owner-picker-return',async({page,evidence,choose,uploaded,switchOwner,sample,marks})=>{
   const chooser=await choose();await switchOwner('beta');await chooser.setFiles(file);
   if(baseline)await uploaded();else assert.match(await page.locator('.attachment-hint').textContent(),/cancelled/);
   assert.equal(await page.locator('.attachment-card').count(),baseline?1:0);
   const uploads=(await evidence()).trace.filter(t=>t.kind==='upload');assert.equal(uploads.length,baseline?3:0);assert(uploads.every(t=>t.owner==='beta'));
   marks.push(await sample());
  });
  if(!baseline){
   await scenario('upload-failure-retry',async({page,control,evidence,choose,uploaded,sample,marks})=>{
    await control({uploadFailure:true});await(await choose()).setFiles(file);await page.getByRole('button',{name:'Retry',exact:true}).waitFor();
    assert.match(await page.locator('.attachment-hint').textContent(),/failed/);assert(await page.locator('.send-button').isDisabled());marks.push(await sample());
    await control({uploadFailure:false,running:true,publish:true});await page.getByRole('button',{name:'Retry',exact:true}).click();await uploaded();
    const uploads=(await evidence()).trace.filter(t=>t.kind==='upload');assert.equal(uploads.length,4);assert.equal(new Set(uploads.map(t=>t.id)).size,1);marks.push(await sample());
   });
   await scenario('disposed-chooser-and-upload',async({page,marks})=>{
    marks.push(await page.evaluate(async()=>{
     const {createAttachmentComposer}=await import('/components/attachments.mjs'),results=[];
     for(const phase of ['chooser','upload','forgotten']){
      const form=document.createElement('form'),input=document.createElement('textarea');form.append(input);
      const calls=[];let release,composer;
      const client={scope:()=>({space:'isolated',owner:'original'}),request:(_path,options)=>{calls.push(options.body.action);return new Promise(resolve=>release=()=>resolve({offset:0}));}};
      composer=createAttachmentComposer({client,form,input,getKey:()=> 'original-conversation',getCaps:()=>({image:true}),canSelect:()=>true,canUpload:()=>true,changed:()=>composer.update(),locale:()=> 'en'});
      composer.update();const click=HTMLInputElement.prototype.click;
      try{HTMLInputElement.prototype.click=()=>{};form.querySelector('.attachment-menu button').click();}finally{HTMLInputElement.prototype.click=click;}
      const picker=form.querySelector('input[type=file]'),data=new DataTransfer();data.items.add(new File(['late'],'late.png',{type:'image/png'}));picker.files=data.files;
      if(phase==='upload'){picker.dispatchEvent(new Event('change'));await Promise.resolve();}
      if(phase==='forgotten')composer.forget(client.scope());else composer.destroy();
      if(phase!=='upload')picker.dispatchEvent(new Event('change'));else release();
      await Promise.resolve();await Promise.resolve();
      if(composer.items().length||calls.join(',')!==(phase==='upload'?'begin':''))throw Error('Disposed work revived: '+JSON.stringify({phase,calls}));
      results.push({phase,calls,items:composer.items().length});
      composer.destroy();
     }
     return results;
    }));
   });
   await scenario('pending-send-snapshot',async({page,control,evidence,choose,uploaded,marks,sample})=>{
    await(await choose()).setFiles(file);await uploaded();await control({holdSend:true,running:true,publish:true});await page.locator('.send-button').click();
    await page.waitForFunction(async()=> (await(await fetch('/fixture/state')).json()).rows.length===1);
    await page.getByRole('button',{name:'Remove attachment',exact:true}).click();await(await choose()).setFiles({...file,name:'new.png'});await uploaded();
    await control({receipt:true});await page.waitForFunction(()=>document.querySelector('.connection-status').classList.contains('is-connected'));
    assert.equal(await page.locator('.attachment-name').textContent(),'new.png');const e=await evidence();assert.equal(e.rows.length,1);assert.equal(e.trace.filter(t=>t.kind==='post').length,1);assert.equal(e.trace.filter(t=>t.kind==='upload'&&t.action==='remove').length,0);assert.notEqual(e.rows[0].body.attachments[0].name,'new.png');marks.push(await sample());
   });
   await scenario('owner-cancel-and-late-uploads',async({page,control,evidence,choose,uploaded,switchOwner,marks,sample})=>{
    const chooser=await choose();await switchOwner('beta');await chooser.setFiles(file);assert.equal(await page.locator('.attachment-card').count(),0);assert.match(await page.locator('.attachment-hint').textContent(),/cancelled/);assert.equal((await evidence()).trace.filter(t=>t.kind==='upload').length,0);
    await switchOwner('alpha');assert.equal(await page.locator('.attachment-card').count(),0);
    await choose();await page.evaluate(()=>{window.oldPicker=document.querySelector('input[type=file]');oldPicker.dispatchEvent(new Event('cancel'));});
    const next=await choose();await page.evaluate(()=>{const d=new DataTransfer();d.items.add(new File(['late'],'late.png',{type:'image/png'}));oldPicker.files=d.files;oldPicker.dispatchEvent(new Event('change'));});assert.equal(await page.locator('.attachment-card').count(),0);
    await control({holdUploads:true});await next.setFiles(file);await page.waitForFunction(async()=> (await(await fetch('/fixture/state')).json()).trace.some(t=>t.kind==='upload'&&t.action==='begin'));
    await page.getByRole('button',{name:'Remove attachment',exact:true}).click();await control({holdUploads:false,releaseUploads:true});await(await choose()).setFiles({...file,name:'new.png'});await uploaded();
    const resetPicker=await choose();await control({conversation:'replacement',publish:true});await page.waitForFunction(()=>!document.querySelector('input[type=file]'));await resetPicker.setFiles(file);assert.equal(await page.locator('.attachment-card').count(),1);
    const e=await evidence();assert(e.trace.filter(t=>t.kind==='upload').every(t=>t.owner==='alpha'));assert.equal(e.trace.filter(t=>t.kind==='upload'&&t.action==='finish').length,1);assert.equal(await page.locator('.attachment-name').textContent(),'new.png');marks.push(await sample());
   });
   await scenario('limits-unsupported-and-locale',async({page,control,evidence,choose,uploaded,marks,sample})=>{
    await(await choose()).setFiles([file,{name:'large.png',mimeType:'image/png',buffer:Buffer.alloc(8*1024*1024)}]);await uploaded();assert.match(await page.locator('.attachment-hint').textContent(),/8 MiB/);
    await control({image:false,publish:true});await page.waitForFunction(()=>document.querySelector('.attachment-card').classList.contains('is-incompatible'));assert.match(await page.locator('.attachment-hint').textContent(),/does not support images/);assert(await page.locator('.send-button').isDisabled());
    await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}})));assert.match(await page.locator('.attachment-hint').textContent(),/不支持图片/);assert.match(await page.locator('.attachment-hint').textContent(),/8 MiB/);assert.equal(await page.getByRole('button',{name:'移除附件',exact:true}).count(),1);
    await page.getByRole('button',{name:'移除附件',exact:true}).click();
    await control({image:true,publish:true});await page.waitForFunction(()=>!document.querySelector('.attachment-menu button').disabled);
    await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'en'}})));
    const chooser=await choose();await control({image:false,publish:true});await page.waitForFunction(()=>document.querySelector('.attachment-menu button').disabled);await chooser.setFiles(file);
    assert.equal(await page.locator('.attachment-card').count(),1);assert.match(await page.locator('.attachment-hint').textContent(),/does not support images/);
    assert.equal((await evidence()).trace.filter(t=>t.kind==='upload'&&t.action==='begin').length,1);
    marks.push({phase:'unsupported-return',ui:await sample()});await control({image:true,publish:true});await uploaded();
    assert.equal((await evidence()).trace.filter(t=>t.kind==='upload'&&t.action==='begin').length,2);marks.push(await sample());
   });
  }
 }finally{await fixture.close();}
 for(const result of results)if(!result.ok)throw Error(JSON.stringify(results,null,2));
 return results;
}
