import assert from 'node:assert/strict';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {translate} from '../../web/src/locales/messages.mjs';

const server=await sourceServer(),browser=await launchBrowser();
try{
 for(const locale of ['en','zh-CN']){
  const page=await browser.newPage({viewport:{width:locale==='en'?1280:390,height:900}}),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  try{
   await page.goto(server.origin+'/empty-loading-fixture');
   await page.evaluate(async locale=>{
    document.documentElement.lang=locale;document.body.innerHTML='<main></main>';
    const sheet=document.createElement('link');sheet.rel='stylesheet';sheet.href='/styles.css';document.head.append(sheet);await new Promise(resolve=>sheet.onload=resolve);await document.fonts.ready;
    const {createPages}=await import('/components/pages.mjs');
    const f=window.loadingFeedback={checks:[],tests:[],saves:[],holdCatalog:true};
    const snapshot={profiles:[],directory:[],provider_types:[{id:'custom',name:'Fixture',available:true,protocol:'openai_chat',protocols:['openai_chat'],capabilities:{catalog:'http'}}]};
    const client={runtime:{boot_id:'fixture'},assistants:[],scope:()=>({space:'local',owner:''}),request:async(path,options={})=>{
     if(path==='/api/v1/providers')return options.method==='POST'?new Promise((resolve,reject)=>f.saves.push({resolve,reject})):f.holdCatalog?new Promise(resolve=>f.catalogReady=()=>{f.holdCatalog=false;resolve(snapshot);}):snapshot;
     if(path==='/api/v1/providers/resolve')return {endpoint:'http://fixture/v1/chat/completions'};
     if(path==='/api/v1/providers/check')return new Promise((resolve,reject)=>f.checks.push({resolve,reject}));
     if(path==='/api/v1/providers/test')return new Promise((resolve,reject)=>f.tests.push({resolve,reject}));
     return {};
    }};
    f.pages=createPages({client,chatVisible:()=>false,host:document.querySelector('main'),approvalHost:document.createElement('div'),chatApprovalHost:document.createElement('section'),locale:()=>locale,notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});
    await f.pages.show('settings');
   },locale);
   await page.getByRole('button',{name:translate(locale,'settings.providers'),exact:true}).click();
   await page.waitForFunction(()=>!!loadingFeedback.catalogReady);
   await page.waitForTimeout(280);
   assert.equal(await page.locator('.page-content').getAttribute('data-initial-loading'),'');
   assert.equal(await page.locator('.page-content').evaluate(node=>getComputedStyle(node,'::after').opacity),'1');
   await page.evaluate(()=>loadingFeedback.catalogReady());
   await page.waitForFunction(()=>!document.querySelector('.page-content').hasAttribute('data-initial-loading'));
   await page.evaluate(()=>loadingFeedback.pages.setupAI());
   await page.getByRole('button',{name:'Fixture',exact:true}).click();
   await page.getByLabel(translate(locale,'providers.endpoint'),{exact:true}).fill('http://fixture');
   await page.getByLabel(translate(locale,'providers.key'),{exact:true}).fill('key');
   await page.getByLabel(translate(locale,'providers.modelID'),{exact:true}).fill('model');
   await page.waitForFunction(()=>loadingFeedback.checks.length===1);
   await page.waitForTimeout(280);
   const model=page.getByLabel(translate(locale,'providers.model'),{exact:true});
   assert.equal(await model.getAttribute('aria-busy'),'true');
   assert.equal(await model.evaluate(node=>getComputedStyle(node.parentElement,'::after').opacity),'1');
   await page.getByLabel(translate(locale,'providers.key'),{exact:true}).fill('replacement-key');
   await page.waitForFunction(()=>loadingFeedback.checks.length===2);
   await page.evaluate(()=>loadingFeedback.checks[0].resolve({supported:true,catalog:{read_at:new Date().toISOString(),models:[{id:'stale'}]}}));
   await page.waitForTimeout(40);assert.equal(await model.getAttribute('aria-busy'),'true','Older discovery cannot stop the current indicator');
   await page.evaluate(()=>loadingFeedback.checks[1].resolve({supported:true,catalog:{read_at:new Date().toISOString(),models:[{id:'model'}]}}));
   await page.waitForFunction(()=>!document.querySelector('.model-discovery select').hasAttribute('aria-busy'));
   const test=page.getByRole('button',{name:translate(locale,'providers.test'),exact:true});
   await test.scrollIntoViewIfNeeded();const before=await test.boundingBox();await test.click();await page.waitForTimeout(280);
   assert.equal(await test.getAttribute('aria-busy'),'true');assert.equal(await test.textContent(),translate(locale,'providers.test'));assert.deepEqual(await test.boundingBox(),before);
   assert.equal(await test.evaluate(node=>getComputedStyle(node,'::after').opacity),'1');
   await page.evaluate(()=>loadingFeedback.tests[0].reject(Error('Synthetic failure')));
   await page.waitForFunction(()=>!document.querySelector('.provider-test-row button').disabled);
   assert.equal(await test.getAttribute('aria-busy'),'false');
   const save=page.locator('.editor-actions .primary');await save.scrollIntoViewIfNeeded();const saveBefore=await save.boundingBox();await save.click();await page.waitForTimeout(280);
   assert.equal(await save.getAttribute('aria-busy'),'true');assert.deepEqual(await save.boundingBox(),saveBefore);
   await page.evaluate(()=>loadingFeedback.saves[0].reject(Error('Synthetic save failure')));
   await page.waitForFunction(()=>!document.querySelector('.editor-actions .primary').disabled);
   assert.equal(await save.getAttribute('aria-busy'),null);
   // Real resource and pagination components with held asynchronous work.
   await page.evaluate(async locale=>{
    loadingFeedback.pages.dispose();document.body.replaceChildren();
    const {createResourceView}=await import('/components/resource-view.mjs');
    const {createRecordPagination}=await import('/components/record-groups.mjs');
    const resource={id:'fixture',entry:'report.md',name:'report.md',mime_type:'text/markdown',size:10,files:[{path:'report.md'}],download:{path:'report.md'}};
    const view=createResourceView({resource,mode:'preview'},{acquireResource:()=>({read:()=>new Promise(resolve=>loadingFeedback.resourceReady=resolve),release(){}}),downloadResource:()=>new Promise((resolve,reject)=>loadingFeedback.download={resolve,reject})});
    document.body.append(view);loadingFeedback.resource=view;
    const pager=createRecordPagination({locale:()=>locale,label:'Pages',move(){}});document.body.append(pager.root);pager.update({offset:20,count:20,total:80,nextOffset:40});loadingFeedback.pager=pager;
   },locale);
   await page.waitForTimeout(280);
   const resource=page.locator('pot-resource');assert.equal(await resource.getAttribute('data-state'),'loading');assert.equal(await resource.evaluate(node=>getComputedStyle(node.querySelector('.resource-body'),'::after').opacity),'1');
   const summary=page.locator('.record-page-summary'),summaryBefore=await summary.boundingBox();
   await page.evaluate(()=>loadingFeedback.pager.update({offset:20,loading:true}));await page.waitForTimeout(280);
   assert.deepEqual(await summary.boundingBox(),summaryBefore);assert.equal(await summary.evaluate(node=>getComputedStyle(node,'::after').opacity),'1');
   await page.emulateMedia({reducedMotion:'reduce'});
   assert.equal(await resource.evaluate(node=>getComputedStyle(node.querySelector('.resource-body'),'::after').animationName),'none');assert.equal(await resource.evaluate(node=>getComputedStyle(node.querySelector('.resource-body'),'::after').opacity),'1');
   await page.evaluate(()=>loadingFeedback.resourceReady(new Blob(['# Loaded preview'],{type:'text/markdown'})));
   await page.waitForFunction(()=>loadingFeedback.resource.dataset.state==='ready');
   assert.equal(await resource.evaluate(node=>getComputedStyle(node.querySelector('.resource-body'),'::after').content),'none');
   const download=resource.locator('.resource-actions button').last();await download.click();assert.equal(await download.getAttribute('aria-busy'),'true');
   await page.evaluate(()=>loadingFeedback.download.reject(Error('Synthetic download failure')));
   await page.waitForFunction(()=>!loadingFeedback.resource.querySelector('.resource-actions button:last-child').disabled);
   assert.equal(await download.getAttribute('aria-busy'),null);
   assert.deepEqual(errors,[]);console.log(JSON.stringify({locale,buttons:true,discovery:true,resources:true,pagination:true,reducedMotion:true}));
  }finally{await page.close();}
 }
}finally{await browser.close();await server.close();}
