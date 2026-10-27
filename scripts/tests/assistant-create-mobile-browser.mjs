import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {assistantCreateHTTPFixture} from './assistant-create-http-fixture.mjs';

const baseline=process.argv.includes('--baseline'),output=resolve(process.env.POT_CREATION_OUTPUT||'.pot-artifacts/mobile-create');
await mkdir(output,{recursive:true});
const source=await sourceServer({root:process.env.POT_CREATION_SOURCE||resolve('.')}),browser=await launchBrowser(),results=[];
const phoneContext=async origin=>{const context=await browser.newContext({viewport:{width:390,height:852},hasTouch:true,isMobile:true,serviceWorkers:'block'});await context.route('**/*',route=>new URL(route.request().url()).origin===origin?route.continue():route.abort());return context;};
try{for(const locale of ['en','zh-CN']){
  const fixture=await assistantCreateHTTPFixture(source.origin),context=await phoneContext(fixture.origin),page=await context.newPage(),errors=[];
  page.setDefaultTimeout(8000);page.on('console',m=>{if(m.type()==='error')console.error(m.text());});page.on('response',r=>{if(r.status()>=400)console.error(r.status(),r.url());});page.on('pageerror',e=>errors.push(e.message));
  await context.addInitScript(()=>{
    window.creationEvidence={touch:[],history:[],idb:[],frames:[]};
    let previous;const sample=()=>{const root=document.querySelector('#app'),history=document.querySelector('.history'),value={route:root?.dataset.route,directory:root?.classList.contains('show-directory'),owner:document.querySelector('.assistant-row.is-selected')?.dataset.viewKey,chatVisible:!!history&&!history.hidden&&getComputedStyle(history).visibility==='visible'};const key=JSON.stringify(value);if(key!==previous){creationEvidence.frames.push({at:Date.now(),...value});previous=key;}requestAnimationFrame(sample);};requestAnimationFrame(sample);
    for(const type of ['pointerdown','pointerup','pointercancel','click'])document.addEventListener(type,e=>{creationEvidence.touch.push({at:Date.now(),type,trusted:e.isTrusted,pointerType:e.pointerType,target:e.target.closest('button')?.className});},true);
    for(const method of ['pushState','replaceState']){const original=history[method];history[method]=function(state,...args){creationEvidence.history.push({at:Date.now(),method,state});return original.call(this,state,...args);};}
    addEventListener('popstate',e=>creationEvidence.history.push({at:Date.now(),method:'popstate',state:e.state}));
    const tx=IDBDatabase.prototype.transaction;IDBDatabase.prototype.transaction=function(...args){const transaction=tx.apply(this,args);transaction.addEventListener('complete',()=>creationEvidence.idb.push({at:Date.now(),mode:transaction.mode,stores:[...transaction.objectStoreNames]}));return transaction;};
  });
  const control=async data=>assert((await context.request.post(fixture.origin+'/fixture/control',{data})).ok());
  const count=path=>fixture.trace.filter(row=>row.kind==='request'&&row.path===path).length;
  const tap=selector=>page.locator(selector).tap();
  const draftSaved=(owner,text)=>page.waitForFunction(async({owner,text})=>{const {createIndexedStore,idbRequest}=await import('/state/indexed-store.mjs'),store=createIndexedStore();try{const rows=await store.transaction('composerDrafts','readonly',tx=>idbRequest(tx.objectStore('composerDrafts').getAll()));return rows.some(row=>row.key===JSON.stringify(['creation-control','local',owner])&&row.text===text);}finally{store.close();}},{owner,text});
  try{
    await page.goto(fixture.origin+'/?locale='+locale+'&theme=light');await page.locator('.show-directory .assistant-row').waitFor();
    const notNow=page.getByRole('button',{name:locale==='en'?'Not now':'暂不',exact:true});if(await notNow.count())await notNow.tap();
    await tap('.assistant-row');await page.locator('#pot-composer').fill('Original draft '+locale);await draftSaved('original','Original draft '+locale);await tap('.titlebar .narrow-navigation');
    await control({hold:['links']});const linksBefore=count('/api/v1/agent-links'),addStarted=Date.now();await tap('.directory-heading button:last-child');await page.locator('.assistant-runtime-choice').first().waitFor();const localReadyMS=Date.now()-addStarted;
    // A directory update between touch down/up must retain the selected node.
    const remote=page.locator('[data-runtime-id="connection:fixture-peer"] button');await page.waitForTimeout(150);const remoteReadyWhileLinksHeld=await remote.count()>0;await control({release:['links']});await remote.waitFor();results.push({locale,localReadyMS,remoteReadyWhileLinksHeld,addReadyMS:Date.now()-addStarted,addLinkRequests:count('/api/v1/agent-links')-linksBefore});if(!baseline)assert(remoteReadyWhileLinksHeld,'The already confirmed connection catalog needs no duplicate read');
    const session=await context.newCDPSession(page),box=await remote.boundingBox(),x=box.x+box.width/2,y=box.y+box.height/2;
    await session.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x,y}]});await control({changed:true});await page.waitForFunction(()=>document.querySelector('[data-runtime-id="connection:fixture-peer"] .assistant-runtime-status')&&document.querySelector('[data-runtime-id="connection:fixture-peer"] .hint')?.textContent.includes('1'));
    await session.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});await page.waitForTimeout(160);
    const remoteResponded=await page.locator('.editor-form').count()>0;results.push({locale,remoteResponded});if(!baseline)assert(remoteResponded,'A real touch survives a directory refresh');
    if(remoteResponded){await tap('.assistant-runtime-summary button');await page.waitForTimeout(120);const changeResponded=await page.locator('.assistant-runtime-choice').count()>0;results.push({locale,changeResponded});if(!baseline)assert(changeResponded,'Change runtime leaves the configuration editor');if(!changeResponded)await tap('.editor-actions button:not(.primary)');await page.locator('.assistant-runtime-choice').first().waitFor();}
    await control({hold:['providers']});const providersBefore=count('/api/v1/providers');
    await tap('[data-runtime-id=local] button');await page.waitForTimeout(60);const localBox=await page.locator('[data-runtime-id=local] button').boundingBox();await page.touchscreen.tap(localBox.x+20,localBox.y+20);
    await page.waitForTimeout(120);const loading=await page.locator('.assistant-catalog-state').count(),busy=await page.locator('.assistant-runtime-choice[aria-busy="true"]').count();
    await page.screenshot({path:output+`/loading-${locale}-${baseline?'before':'after'}.png`});
    if(!baseline)await page.evaluate(locale=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:locale==='en'?'zh-CN':'en'}})),locale);
    await control({release:['providers']});await page.locator('.editor-form input').first().waitFor();
    results.push({locale,loading,busy,providerRequests:count('/api/v1/providers')-providersBefore});if(!baseline){assert.equal(loading,1);assert.equal(busy,1);assert.equal(count('/api/v1/providers')-providersBefore,1);}
    await page.evaluate(locale=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:locale}})),locale);
    await page.locator('.editor-form input').first().fill('Created fixture');
    if(!baseline){await tap('.assistant-runtime-summary button');await page.locator('dialog[open]').last().getByRole('button',{name:locale==='en'?'Keep editing':'继续编辑',exact:true}).tap();assert.equal(await page.locator('.editor-form input').first().inputValue(),'Created fixture');}
    await control({failCreate:true});await tap('.editor-actions .primary');await page.locator('.editor-form .inline-error span').waitFor();
    assert.equal(await page.locator('.editor-form input').first().inputValue(),'Created fixture');
    await control({hold:['create','links']});const createStart=Date.now();await tap('.editor-actions .primary');await page.waitForTimeout(60);
    // Trusted duplicate touch on the pending submit cannot register again.
    const submitBox=await page.locator('.editor-actions .primary').boundingBox();await page.touchscreen.tap(submitBox.x+10,submitBox.y+10);
    await control({release:['create']});await page.waitForTimeout(550);
    const chatBeforeLinks=await page.locator('#app[data-route="chat"]:not(.show-directory)').count()>0;
    results.push({locale,chatBeforeLinks,elapsed:Date.now()-createStart});if(!baseline)assert(chatBeforeLinks,'Committed creation opens chat while unrelated link metadata is held');
    await page.screenshot({path:output+`/created-${locale}-${baseline?'before':'after'}.png`});
    await control({release:['links']});await page.locator('#app[data-route="chat"]:not(.show-directory)').waitFor();await page.locator('article.message').waitFor();
    assert.equal(await page.locator('#pot-composer').inputValue(),'','A new identity cannot inherit the original assistant draft');
    if(!baseline){await page.goBack();await page.locator('#app.show-directory').waitFor();assert.equal(await page.locator('.editor-form').count(),0);await page.goForward();await page.waitForFunction(()=>history.state?.potScope?.owner==='created-1'&&!document.querySelector('#app.show-directory'));}
    await page.locator('#pot-composer').fill('Created draft '+locale);await draftSaved('created-1','Created draft '+locale);
    // Product edge Back with native touch input, followed by browser Back.
    await session.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:4,y:350}]});
    for(const x of [35,80,150,235]){await session.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x,y:350}]});await page.waitForTimeout(25);}
    await session.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});await page.waitForTimeout(350);
    const gestureDirectory=await page.locator('#app.show-directory').count()>0;
    // Inspect browser Back separately from selecting the directory again.
    if(gestureDirectory)await tap('.assistant-row.is-selected');
    if(gestureDirectory)await page.waitForFunction(()=>history.state?.potScope?.owner==='created-1'&&history.state?.potDirectory===false);
    await page.goBack();await page.waitForTimeout(350);const browserDirectory=await page.locator('#app.show-directory').count()>0;
    await page.screenshot({path:output+`/back-${locale}-${baseline?'before':'after'}.png`});
    results.push({locale,gestureDirectory,browserDirectory,history:await page.evaluate(()=>history.state)});
    if(!baseline){assert(gestureDirectory,'Touch edge Back opens the assistant directory');assert(browserDirectory,'Browser Back cannot reopen the completed creation page');}
    if(!baseline){await page.locator('.assistant-row').filter({hasText:'Original fixture'}).tap();await page.waitForFunction(locale=>document.querySelector('#pot-composer')?.value==='Original draft '+locale,locale);await tap('.titlebar .narrow-navigation');await page.locator('.assistant-row').filter({hasText:'Created fixture'}).tap();await page.waitForFunction(locale=>document.querySelector('#pot-composer')?.value==='Created draft '+locale,locale);results.push({locale,independentDrafts:true});}
    const state=await (await context.request.get(fixture.origin+'/fixture/state')).json();assert.equal(state.created,1);assert.equal(state.trace.filter(row=>row.path==='/api/v1/assistants'&&row.method==='POST').length,2,'One failed attempt and one successful retry; no duplicate touch mutation');
    assert.deepEqual(errors,[]);const evidence=await page.evaluate(()=>creationEvidence);assert(evidence.idb.some(row=>row.mode==='readwrite'),'Real IndexedDB commits were observed');
    const acceptedAt=fixture.trace.find(row=>row.kind==='created').at,chatAt=evidence.frames.find(row=>row.at>=acceptedAt&&row.route==='chat'&&!row.directory&&row.chatVisible&&row.owner===JSON.stringify(['local','created-1']))?.at;assert(chatAt);results.push({locale,acceptedToVisibleChatMS:chatAt-acceptedAt});
    await writeFile(output+`/evidence-${locale}-${baseline?'before':'after'}.json`,JSON.stringify({results:results.filter(row=>row.locale===locale),trace:fixture.trace,evidence,errors},null,2));
  }catch(error){console.error(JSON.stringify({errors,trace:fixture.trace.slice(-12),body:(await page.locator('body').innerText()).slice(-1600)}));await writeFile(output+'/failure-evidence.json',JSON.stringify(await page.evaluate(()=>creationEvidence),null,2));await page.screenshot({path:output+'/failure.png'});throw error;}finally{await context.close();await fixture.close();}
}
  {
    const fixture=await assistantCreateHTTPFixture(source.origin),context=await phoneContext(fixture.origin),page=await context.newPage();page.setDefaultTimeout(8000);
    try{
      await context.request.post(fixture.origin+'/fixture/control',{data:{emptyProviders:true}});await page.goto(fixture.origin+'/?locale=en');await page.locator('.show-directory .assistant-row').waitFor();await page.locator('.directory-heading button:last-child').tap();await page.locator('[data-runtime-id=local] button').tap();await page.locator('.provider-choice').waitFor();
      const requests=fixture.trace.filter(row=>row.kind==='request'&&row.path==='/api/v1/providers').length;results.push({noProviderSetupRequests:requests});if(!baseline)assert.equal(requests,2,'First-provider setup reuses configuration and retains its authoritative executable-type refresh');await writeFile(output+`/no-provider-${baseline?'before':'after'}.json`,JSON.stringify({requests,trace:fixture.trace},null,2));
      assert.equal(fixture.trace.filter(row=>row.method==='POST'&&row.path==='/api/v1/assistants').length,0);
    }finally{await context.close();await fixture.close();}
  }
  if(!baseline)for(const fault of ['back','scope','boot','connection','control','capability']){
    const context=await phoneContext(source.origin),page=await context.newPage();page.setDefaultTimeout(8000);
    try{
      await page.goto(source.origin+'/scripts/tests/fixtures/assistant-start.html?capture=1&locale=en');await page.waitForFunction(()=>window.assistantStartResults?.passed);
      await page.evaluate(()=>{const f=window.managementFixture,request=f.client.request;window.oldChoice=document.querySelectorAll('.assistant-runtime-choice')[1];window.oldClick=oldChoice.onclick;window.heldCatalogs=0;
        f.client.request=async(path,options={})=>{if(path==='/api/v1/agent-links/assistants'&&options.body?.action==='catalog'){heldCatalogs++;await new Promise(resolve=>window.releaseCatalog=resolve);}return request(path,options);};});
      await page.locator('.assistant-runtime-choice').nth(1).tap();await page.waitForFunction(()=>!!window.releaseCatalog);
      await page.evaluate(fault=>{const f=window.managementFixture;if(fault==='scope')f.client.setScope({space:'other',owner:'other'});if(fault==='boot')f.client.runtime.boot_id='replacement';if(fault==='connection')f.client.connectionGeneration=1;if(fault==='control')f.client.spaces[0].control_id='replacement';if(fault==='capability'){f.links[0].directory.targets[0].capabilities.operations=[];f.client.resources.invalidate(['agent-links'],{space:'local'});}},fault);
      if(fault==='back')await page.locator('.titlebar button').first().tap();
      await page.evaluate(()=>releaseCatalog());await page.waitForTimeout(150);
      assert.equal(await page.locator('.assistant-management-form').count(),0,'A retired '+fault+' catalog cannot open an editor');
      await page.evaluate(()=>oldClick());await page.waitForTimeout(80);assert.equal(await page.evaluate(()=>heldCatalogs),1,'An old '+fault+' row handler cannot issue a second catalog request');
      results.push({fault,retiredCatalogRejected:true,oldHandlerRejected:true});
    }finally{await context.close();}
  }
  if(!baseline)for(const fault of ['scope','boot','connection']){
    const context=await phoneContext(source.origin),page=await context.newPage();page.setDefaultTimeout(8000);
    try{
      await page.goto(source.origin+'/scripts/tests/fixtures/assistant-start.html?capture=1&locale=en');await page.waitForFunction(()=>window.assistantStartResults?.passed);
      await page.locator('.assistant-runtime-choice').first().tap();await page.locator('.editor-form input').first().fill('Retired creation');
      await page.evaluate(()=>{const f=managementFixture,request=f.client.request;f.client.request=async(path,options={})=>{if(path==='/api/v1/assistants'&&options.method==='POST')await new Promise(resolve=>window.releaseCreation=resolve);return request(path,options);};});
      await page.locator('.editor-actions .primary').tap();await page.waitForFunction(()=>!!window.releaseCreation);
      await page.evaluate(fault=>{const f=managementFixture;if(fault==='scope')f.client.setScope({space:'other',owner:'other'});if(fault==='boot')f.client.runtime.boot_id='replacement';if(fault==='connection')f.client.connectionGeneration=1;releaseCreation();},fault);
      await page.locator('.editor-form [data-error-key="identityChanged"]').waitFor();assert(await page.locator('.editor-actions .primary').isDisabled());
      await page.locator('.editor-form').evaluate(form=>form.requestSubmit());assert.equal(await page.evaluate(()=>managementFixture.created),1);assert.equal(await page.locator('#app[data-route="chat"]').count(),0);
      results.push({fault,lateCreationCannotNavigate:true,acceptedOnce:true});
    }finally{await context.close();}
  }
}finally{await browser.close();await source.close();await writeFile(output+`/results-${baseline?'before':'after'}.json`,JSON.stringify(results,null,2));}
console.log(JSON.stringify({baseline,results}));
