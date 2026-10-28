import {waitForDraftSaved} from './draft-persistence-browser.mjs';
// Isolated production bootstrap + real SW/IndexedDB/SSE. No Control, account,
// model, product root, Push registration or browser profile is used.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile,mkdir,writeFile,mkdtemp,rm} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import {tmpdir} from 'node:os';
import {execFileSync} from 'node:child_process';
import {shellAssets} from './pwa-shell.mjs';
const root=resolve(process.env.POT_COLD_SOURCE||resolve(import.meta.dirname,'../..'));
const output=resolve(process.env.POT_COLD_OUTPUT||'.pot-artifacts/cold-start');await mkdir(output,{recursive:true});
let release='one',hold='',control='synthetic-control',spaceControl='',auth=200,authenticated=true,failAsset='',revoked=false,offlineSpace=false;
const held=[];
const calls=[],streams=new Set();
const original=await shellAssets(root),updated=await shellAssets(root,(path,body)=>path==='/styles.css'?Buffer.concat([body,Buffer.from('\n/* synthetic next release */\n')]):body);
if(!process.argv.includes('--serve')){
 const temporary=await mkdtemp(resolve(tmpdir(),'pot-cold-shell-'));
 try{
  const source=resolve(temporary,'worker.go');await writeFile(source,'package main\nimport("io/fs";"os"; web "github.com/jjyr/pot/web")\nfunc main(){b,e:=fs.ReadFile(web.FS(),"service-worker.js");if e!=nil{panic(e)};os.Stdout.Write(b)}\n');
  const embedded=execFileSync('go',['run',source],{cwd:root,maxBuffer:4*1024*1024});
  assert.equal(original.files.get('/service-worker.js').toString(),embedded.toString(),'browser fixture must serve the exact Go-generated worker');
  original.files.set('/service-worker.js',embedded);
 }finally{await rm(temporary,{recursive:true,force:true});}
}
const capabilities={version:1,operations:['chat.read','chat.send','chat.continue','chat.events','chat.history','assistant.profile','assistant.profile.read','assistant.profile.write','assistant.management','task.read','task.list','task.events']};
const definitions=['alpha','beta'].map(id=>({id,name:id==='alpha'?'Synthetic Alpha':'Synthetic Beta',revision:1,phase:'ready',capabilities}));
const frame=owner=>({kind:'reset',base_cursor:0,view:{state:{api_version:3,cursor:1,boot_id:'synthetic-boot',conversation_id:'conversation-'+owner,turn:null,...(owner==='beta'?{continuation:{user_message_id:'beta-original',attempt_id:'synthetic-attempt',summary:false}}:{}),agent:{phase:'ready',model:'synthetic-model'}},replies:[],messages:Array.from({length:90},(_,index)=>({sequence:String(index+1),version:1,message:{id:owner+'-'+index,role:index%2?'assistant':'user',status:'complete',text:`${owner.toUpperCase()} cached message ${index+1}. Synthetic reading position sample.\n`+'Local conversation content. '.repeat(5)}})),history:{before:'1',after:'90',has_more:false,has_newer:false}}});
function closeStreams(){for(const res of streams)res.end();streams.clear();}
const server=createServer(async(req,res)=>{try{
 const url=new URL(req.url,'http://fixture'),path=url.pathname;
 if(path==='/__phone'){res.setHeader('Content-Type','text/html');res.end('<!doctype html><title>Pot phone viewport fixture</title><iframe title="Pot phone 390px" style="width:390px;height:740px;border:1px solid #ccc" src="http://127.0.0.1:'+String(Number(server.address().port)-1)+'/?locale=en"></iframe>');return;}
 if(path==='/__fixture'){
  if(url.searchParams.has('hold'))hold=url.searchParams.get('hold');
  if(url.searchParams.has('revoked'))revoked=url.searchParams.get('revoked')==='true';
  if(url.searchParams.has('offlineSpace'))offlineSpace=url.searchParams.get('offlineSpace')==='true';
  if(url.searchParams.has('releaseHeld')){const group=url.searchParams.get('releaseHeld');for(const entry of held.splice(0)){if(!group||entry.group===group)entry.release();else held.push(entry);}}
  if(url.searchParams.has('auth'))auth=Number(url.searchParams.get('auth'));
  if(url.searchParams.has('authenticated'))authenticated=url.searchParams.get('authenticated')==='true';
  if(url.searchParams.has('control')){control=url.searchParams.get('control');spaceControl='';}
  if(url.searchParams.has('spaceControl'))spaceControl=url.searchParams.get('spaceControl');
  if(url.searchParams.has('directory'))for(const stream of streams)stream.write('event: changed\ndata: '+JSON.stringify({spaces:2})+'\n\n');
  if(url.searchParams.has('release'))release=url.searchParams.get('release');
  if(url.searchParams.has('failAsset'))failAsset=url.searchParams.get('failAsset');
  if(url.searchParams.has('disconnect'))closeStreams();
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({hold,auth,control,release,pending:held.map(entry=>entry.group),calls}));return;
 }
 const call={at:Date.now(),method:req.method,path:req.url,release};calls.push(call);res.on('finish',()=>{call.status=res.statusCode;call.finishedAt=Date.now()});
 if(path==='/access/status'||path.startsWith('/api/')){
  if(hold==='auth'&&path==='/access/status'||hold==='runtime'&&path==='/api/v1/runtime')return;
  res.setHeader('Content-Type','application/json');
  if(path==='/access/status'){res.writeHead(auth);res.end(JSON.stringify({authenticated,scope:'control'}));return;}
  const owner=url.searchParams.get('assistant_id')||'alpha';let value={};
  if(path==='/api/v1/runtime')value={runtime_id:control,boot_id:'synthetic-boot',host:{hostname:'Synthetic Host'}};
  if(path==='/api/v1/spaces')value={active_space_id:'local',items:[{id:'local',control_id:spaceControl||control,name:'Synthetic Space',kind:'local',connected:true}]};
  if(offlineSpace&&path==='/api/v1/spaces')value.items.push({id:'unavailable',control_id:'unavailable-control',name:'Synthetic unavailable Space',kind:'remote',connected:false});
  if(path==='/api/v1/assistants'&&url.searchParams.get('space_id')==='unavailable'){res.writeHead(503);res.end(JSON.stringify({code:'synthetic_offline'}));return;}
  if(path==='/api/v1/assistants')value=definitions.map(item=>({...item,capabilities:revoked?{version:1,operations:['chat.read','chat.events','chat.history']}:capabilities}));
  if(path==='/api/v1/agent-links')value={items:[]};
  if(path==='/api/v1/snapshot')value=frame(owner);
  if(path==='/api/v1/assistants/profile')value={id:owner,name:owner==='alpha'?'Synthetic Alpha':'Synthetic Beta',revision:1,avatar:''};
  if(path==='/api/v1/permissions')value={pending:[],policy:{machines:{},rules:[],task_grants:[]}};
  if(path==='/api/v1/tasks/catalog'||path==='/api/v1/tasks/query')value={items:[],total:0,counts:{},next_offset:null};
  if(path==='/api/v1/assistants/bootstrap')value={assistant:null};
  if(hold.startsWith('spaces:')&&path==='/api/v1/spaces'||hold.startsWith('assistants:')&&path==='/api/v1/assistants'){const body=JSON.stringify(value);held.push({group:hold.split(':')[1],release:()=>res.end(body)});return;}
  if(path==='/api/v1/events'||path==='/api/v1/changes'){
   res.setHeader('Content-Type','text/event-stream');res.setHeader('Cache-Control','no-store');res.flushHeaders();streams.add(res);res.on('close',()=>streams.delete(res));res.write(': synthetic stream\n\n');
   if(path.endsWith('/events')){const body='event: conversation\ndata: '+JSON.stringify(frame(owner))+'\n\n';if(hold.startsWith('session:'))held.push({group:hold.split(':')[1],release:()=>res.write(body)});else res.write(body);}return;
  }
  res.end(JSON.stringify(value));return;
 }
 let file=(release==='one'?original:updated).files.get(path==='/'?'/index.html':path);
 if(path===failAsset){res.writeHead(503).end('synthetic interrupted update');return;}
 if(!file){res.writeHead(404).end();return;}
 res.setHeader('Cache-Control','no-store');res.setHeader('Content-Type',({'.js':'text/javascript','.mjs':'text/javascript','.css':'text/css','.html':'text/html','.json':'application/json','.webmanifest':'application/manifest+json','.ttf':'font/ttf','.png':'image/png','.svg':'image/svg+xml'})[extname(path==='/'?'/index.html':path)]||'application/octet-stream');res.end(file);
}catch(error){res.writeHead(500).end(error.message)}});
await new Promise(done=>server.listen(Number(process.env.POT_COLD_PORT||0),'127.0.0.1',done));
const origin=`http://127.0.0.1:${server.address().port}`;
if(process.argv.includes('--serve')){console.log(origin);process.on('SIGTERM',()=>{closeStreams();server.closeAllConnections();server.close()});}
else{
 const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE||'playwright');
 const browser=await chromium.launch({headless:true,executablePath:process.env.POT_CHROMIUM_EXECUTABLE,args:['--no-sandbox']});
 const results=[],errors=[];
 async function record(page,name){const observation=await page.evaluate(async()=>({url:location.href,text:document.body.innerText,editor:document.querySelector('.composer-input')?.innerText||document.querySelector('textarea.composer-input')?.value,sendDisabled:document.querySelector('.send-button')?.disabled,controller:!!navigator.serviceWorker.controller,resources:performance.getEntriesByType('resource').map(entry=>({name:entry.name,workerStart:entry.workerStart,transferSize:entry.transferSize})),caches:await Promise.all((await caches.keys()).map(async name=>({name,assets:(await(await caches.open(name)).keys()).map(request=>request.url)}))),storage:await new Promise((resolve,reject)=>{const op=indexedDB.open('pot-chat-cache');op.onerror=()=>reject(op.error);op.onsuccess=()=>{const db=op.result,tx=db.transaction(['startup','views','composerDrafts'],'readonly'),data={};for(const name of ['startup','views','composerDrafts']){const get=tx.objectStore(name).getAll();get.onsuccess=()=>data[name]=get.result;}tx.oncomplete=()=>{db.close();resolve(data)}}})}));results.push({name,at:Date.now(),observation});await writeFile(resolve(output,'observations.json'),JSON.stringify(results,null,2));return observation;}
 const editor=page=>page.locator('textarea.composer-input').first();
 const waitChat=page=>page.getByText('ALPHA cached message 90.',{exact:false}).first().waitFor();
 // Draft hydration precedes the hidden layout/position commits. Check the
 // readable viewport itself while the network gate is still held or offline.
 const waitCachedChat=(page,anchor)=>page.waitForFunction(anchor=>{
  const history=document.querySelector('.history:not([data-preparation-surface])');
  if(!history||history.getAttribute('aria-busy')==='true'||getComputedStyle(history).visibility!=='visible')return false;
  const box=history.getBoundingClientRect();if(!box.width||!box.height)return false;
  const visible=[...history.querySelectorAll('article.message')].filter(row=>{const rect=row.getBoundingClientRect();return rect.bottom>box.top&&rect.top<box.bottom;});
  return visible.some(row=>row.textContent.includes('ALPHA cached message'))&&(!anchor||visible.some(row=>row.dataset.messageId===anchor.id&&Math.abs(row.getBoundingClientRect().top-box.top-anchor.offset)<2));
 },anchor);
 const config=async values=>{const query=new URLSearchParams(values);await fetch(origin+'/__fixture?'+query);};
 try{
 for(const viewport of [{width:1280,height:800},{width:390,height:844}]){
  hold='';auth=200;authenticated=true;control='synthetic-control';spaceControl='';release='one';failAsset='';
  const context=await browser.newContext({viewport});let page=await context.newPage();page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message));
  await page.goto(origin+'/?locale=en');if(viewport.width<500)await page.getByText('Synthetic Alpha',{exact:true}).first().click();await waitChat(page);await editor(page).fill('Persistent synthetic draft');await waitForDraftSaved(page);
  // Cache the second assistant, then return to Alpha without losing its draft.
  if(viewport.width<500)await page.getByRole('button',{name:'Back to Assistants',exact:true}).click();
  await page.getByText('Synthetic Beta',{exact:true}).first().click();await page.getByText('BETA cached message 90.',{exact:false}).first().waitFor();await page.waitForFunction(()=>!document.querySelector('.send-button').disabled);
  if(viewport.width<500)await page.getByRole('button',{name:'Back to Assistants',exact:true}).click();
  await page.getByText('Synthetic Alpha',{exact:true}).first().click();await waitChat(page);assert.equal(await editor(page).inputValue(),'Persistent synthetic draft');
  // A trusted wheel establishes a non-bottom reading anchor.
  await page.locator('.history:not([data-preparation-surface])').hover();await page.mouse.wheel(0,-2200);await page.waitForTimeout(800);
  await page.evaluate(async()=>{await navigator.serviceWorker.ready});
  const prepared=await record(page,'prepared-'+viewport.width);assert(prepared.storage.startup[0]?.owner==='alpha');
  const reading=state=>state.storage.views.find(view=>view.key.includes('alpha')&&view.surface===state.storage.startup[0]?.surface)?.position;
  assert.equal(reading(prepared)?.mode,'reading');
  const anchorPosition=reading(prepared).position.anchor,anchor=anchorPosition.id;
  for(const block of ['auth','runtime']){
   await config({hold:block});await page.reload({waitUntil:'domcontentloaded'});
   await editor(page).waitFor();await page.waitForFunction(()=>document.querySelector('textarea.composer-input')?.value==='Persistent synthetic draft');
   await waitCachedChat(page,anchorPosition);
   assert((await page.locator('.history:not([data-preparation-surface])').innerText()).includes('ALPHA cached message'));
   const state=await record(page,'held-'+block+'-'+viewport.width);assert.equal(reading(state)?.position?.anchor?.id,anchor);assert(state.controller);assert.equal(state.caches.find(cache=>cache.name==='pot-ui-shell-'+original.version)?.assets.length,original.assets.length);assert(!state.text.includes('Could not start'));assert.equal(await page.getByRole('button',{name:'Allow notifications',exact:true}).count(),0);assert(!state.text.split('\n').includes('Connected'),'cached directory must not claim a live connection');if(viewport.width>500)assert(await page.getByRole('button',{name:'Tasks',exact:true}).isDisabled());
   assert(await page.locator('.send-button').isDisabled());
   await editor(page).fill('Typed while '+block+' pending');await page.waitForTimeout(500);
   await page.screenshot({path:resolve(output,'held-'+block+'-'+viewport.width+'.png')});
   await config({hold:''});await page.getByRole('button',{name:'Retry',exact:true}).last().click();await page.waitForFunction(()=>!document.querySelector('.startup-status'));
   await page.waitForTimeout(400);assert.equal(await editor(page).inputValue(),'Typed while '+block+' pending');
   await editor(page).fill('Persistent synthetic draft');await page.waitForTimeout(450);
  }
  await context.setOffline(true);await page.close();page=await context.newPage();page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message));await page.goto(origin+'/?locale=en',{waitUntil:'domcontentloaded'});await editor(page).waitFor();await page.waitForFunction(()=>document.querySelector('textarea.composer-input')?.value==='Persistent synthetic draft');
  await waitCachedChat(page,anchorPosition);
  assert((await page.locator('.history:not([data-preparation-surface])').innerText()).includes('ALPHA cached message'));assert(await page.locator('.send-button').isDisabled());
  await editor(page).fill('Offline new draft');await page.waitForTimeout(500);assert.equal(reading(await record(page,'offline-'+viewport.width))?.position?.anchor?.id,anchor);await page.screenshot({path:resolve(output,'offline-'+viewport.width+'.png')});
  if(viewport.width<500)await page.getByRole('button',{name:'Back to Assistants',exact:true}).click();await page.getByText('Synthetic Beta',{exact:true}).first().click();await page.getByText('BETA cached message 90.',{exact:false}).first().waitFor();
  assert(await page.locator('.send-button').isDisabled());assert.equal(await page.locator('.send-button').getAttribute('aria-label'),'Send');
  if(viewport.width<500)await page.getByRole('button',{name:'Back to Assistants',exact:true}).click();await page.getByText('Synthetic Alpha',{exact:true}).first().click();await editor(page).waitFor();await page.waitForFunction(()=>document.querySelector('textarea.composer-input')?.value==='Offline new draft');
  await waitCachedChat(page,anchorPosition);
  const beforePosts=calls.filter(call=>call.method==='POST'&&/messages|turn|assistants/.test(call.path)).length;
  await config({hold:'runtime'});await context.setOffline(false);await editor(page).fill('New input during reconnect');await page.waitForTimeout(500);assert(await page.locator('.send-button').isDisabled());await record(page,'reconnect-held-'+viewport.width);
  await config({hold:''});await page.getByRole('button',{name:'Retry',exact:true}).last().click();await page.waitForFunction(()=>!document.querySelector('.startup-status'));await page.waitForTimeout(500);
  assert.equal(await editor(page).inputValue(),'New input during reconnect');assert.equal(calls.filter(call=>call.method==='POST'&&/messages|turn|assistants/.test(call.path)).length,beforePosts);
  await page.waitForFunction(()=>!document.querySelector('.send-button').disabled);await record(page,'recovered-'+viewport.width);
  // Interrupted release install keeps the old shell active and readable.
  await config({release:'two',failAsset:'/styles.css'});await page.evaluate(async()=>{await(await navigator.serviceWorker.ready).update()});await page.waitForTimeout(700);
  const oldCache=await page.evaluate(()=>caches.keys());assert(oldCache.includes('pot-ui-shell-'+original.version));assert(!oldCache.includes('pot-ui-shell-'+updated.version));
  await config({failAsset:''});await page.evaluate(async()=>{await(await navigator.serviceWorker.ready).update()});await page.waitForFunction(async()=>(await navigator.serviceWorker.getRegistration()).waiting?.state==='installed');
  assert(await page.evaluate(async expected=>{const text=await(await fetch('/styles.css')).text();return !text.includes('synthetic next release')},original.version));
  await record(page,'update-waiting-'+viewport.width);await page.close();page=await context.newPage();await page.waitForTimeout(350);await page.goto(origin+'/?locale=en',{waitUntil:'domcontentloaded'});await editor(page).waitFor();
  await page.waitForFunction(async()=>!(await navigator.serviceWorker.getRegistration()).waiting);assert((await page.evaluate(async()=>(await(await fetch('/styles.css')).text()))).includes('synthetic next release'));
  await record(page,'update-activated-'+viewport.width);
  // Confirmed Control change creates a new namespace and never imports Alpha's old draft.
  await config({control:'other-synthetic-control'});await page.reload({waitUntil:'domcontentloaded'});await page.waitForFunction(()=>!document.querySelector('.startup-status'));if(viewport.width<500)await page.getByText('Synthetic Alpha',{exact:true}).first().click();await editor(page).waitFor();assert.equal(await editor(page).inputValue(),'');await record(page,'control-change-'+viewport.width);
  await editor(page).fill('Old Control input');await page.waitForTimeout(450);await config({control:'stream-changed-control',disconnect:'1'});await page.waitForFunction(()=>document.querySelector('textarea.composer-input')?.value==='');await record(page,'stream-control-change-'+viewport.width);
  if(viewport.width<500)await page.getByText('Synthetic Alpha',{exact:true}).first().click();await editor(page).fill('Old Space input');await page.waitForTimeout(450);await config({spaceControl:'new-space-control',directory:'1'});await page.waitForFunction(()=>document.querySelector('textarea.composer-input')?.value==='');await record(page,'space-control-change-'+viewport.width);
  await context.close();results.push({name:'PASS viewport '+viewport.width});
 }
 // Fresh visit, explicit access failures, and retry use independent empty storage.
 hold='auth';auth=200;control='synthetic-control';spaceControl='';release='one';
 const context=await browser.newContext(),page=await context.newPage();page.setDefaultTimeout(12000);await page.goto(origin,{waitUntil:'domcontentloaded'});await page.getByText('First use requires a connection.',{exact:false}).waitFor();assert.equal(await editor(page).count(),0);await page.screenshot({path:resolve(output,'first-use.png')});
 await config({hold:'',auth:'403'});await page.getByRole('button',{name:'Retry',exact:true}).click();await page.getByText('Access is not permitted.',{exact:false}).waitFor();
 await config({auth:'401'});await page.getByRole('button',{name:'Retry',exact:true}).click();await page.getByText('Your browser session has expired.',{exact:false}).waitFor();
 await config({auth:'200',authenticated:'false'});await page.getByRole('button',{name:'Retry',exact:true}).click();await page.getByRole('heading',{name:'Connect to Pot'}).waitFor();await context.close();
 // Corrupt locator and transient IndexedDB read failures recover by explicit retry.
 for(const fault of ['corrupt','read']){
  hold='';auth=200;authenticated=true;control='synthetic-control';spaceControl='';release='one';
  const isolated=await browser.newContext(),view=await isolated.newPage();view.setDefaultTimeout(12000);await view.goto(origin);await waitChat(view);await editor(view).fill('Storage recovery draft');await view.waitForTimeout(500);await view.evaluate(async()=>{await navigator.serviceWorker.ready});
  const locator=await view.evaluate(async()=>new Promise((resolve,reject)=>{const op=indexedDB.open('pot-chat-cache');op.onerror=()=>reject(op.error);op.onsuccess=()=>{const db=op.result,tx=db.transaction('startup','readonly'),get=tx.objectStore('startup').get('last');get.onsuccess=()=>resolve(get.result);tx.oncomplete=()=>db.close()}}));
  if(fault==='corrupt')await view.evaluate(async()=>new Promise(resolve=>{const op=indexedDB.open('pot-chat-cache');op.onsuccess=()=>{const db=op.result,tx=db.transaction('startup','readwrite');tx.objectStore('startup').put({key:'last',schema_version:99,runtime_id:'foreign'});tx.oncomplete=()=>{db.close();resolve()}}}));
  else await view.addInitScript(()=>{const get=IDBObjectStore.prototype.get;window.restoreStartupRead=()=>IDBObjectStore.prototype.get=get;IDBObjectStore.prototype.get=function(...args){if(this.name==='startup')throw Error('Synthetic local read failure');return get.apply(this,args)}});
  await isolated.setOffline(true);await view.reload({waitUntil:'domcontentloaded'});await view.getByText('Could not connect or open saved conversations.',{exact:false}).waitFor();assert.equal(await editor(view).count(),0);
  if(fault==='corrupt')await view.evaluate(async locator=>new Promise(resolve=>{const op=indexedDB.open('pot-chat-cache');op.onsuccess=()=>{const db=op.result,tx=db.transaction('startup','readwrite');tx.objectStore('startup').put(locator);tx.oncomplete=()=>{db.close();resolve()}}}),locator);
  else await view.evaluate(()=>restoreStartupRead());
  await view.getByRole('button',{name:'Retry',exact:true}).click();await editor(view).waitFor();await view.waitForFunction(()=>document.querySelector('textarea.composer-input')?.value==='Storage recovery draft');await waitCachedChat(view);assert(await view.locator('.send-button').isDisabled());await record(view,'storage-retry-'+fault);await isolated.close();
 }

 // A retained locator cannot fabricate a missing directory; connectivity repairs it.
 hold='';auth=200;authenticated=true;control='synthetic-control';spaceControl='';release='one';
 const missingContext=await browser.newContext(),missing=await missingContext.newPage();missing.setDefaultTimeout(12000);await missing.goto(origin);await waitChat(missing);await editor(missing).fill('Missing directory draft');await missing.waitForTimeout(500);await missing.evaluate(async()=>{await navigator.serviceWorker.ready;await new Promise(resolve=>{const op=indexedDB.open('pot-chat-cache');op.onsuccess=()=>{const db=op.result,tx=db.transaction('projections','readwrite');tx.objectStore('projections').clear();tx.oncomplete=()=>{db.close();resolve()}}})});await missingContext.setOffline(true);await missing.reload({waitUntil:'domcontentloaded'});await missing.getByText('Showing saved conversations.',{exact:false}).first().waitFor();await missing.waitForTimeout(200);assert(await missing.locator('.send-button').isDisabled());await record(missing,'missing-directory-offline');await missingContext.setOffline(false);await editor(missing).waitFor();await missing.waitForFunction(()=>document.querySelector('textarea.composer-input')?.value==='Missing directory draft');await record(missing,'missing-directory-recovered');await missingContext.close();
 assert.equal(calls.filter(call=>call.method==='POST'&&/messages|turn|assistants/.test(call.path)).length,0,'cache restoration must not submit commands');
 await writeFile(resolve(output,'results.json'),JSON.stringify({passed:true,results,errors,calls,versions:{one:original.version,two:updated.version}},null,2));assert.deepEqual(errors,[]);console.log('PASS local-first cold start desktop/mobile, held auth/runtime, offline reopen/edit/switch, reconnect, no replay, atomic SW update, Control isolation and first-use/access failures');
 }catch(error){
  const pages=await Promise.all(browser.contexts().flatMap(context=>context.pages()).map(async page=>({url:page.url(),dom:await page.evaluate(()=>({text:document.body.innerText,errors:window.potErrors?.logs(),online:navigator.onLine,editor:document.querySelector('textarea.composer-input')?.value,root:document.querySelector('#app')?.className,history:[...document.querySelectorAll('.history:not([data-preparation-surface])')].map(h=>({hidden:h.hidden,busy:h.getAttribute('aria-busy'),visibility:getComputedStyle(h).visibility,top:h.scrollTop,extent:h.scrollHeight,rows:[...h.querySelectorAll('article.message')].map(row=>row.dataset.messageId)}))})).catch(reason=>({error:reason.message}))})));
  await writeFile(resolve(output,'failure.json'),JSON.stringify({error:error.stack,pages,results,errors,calls},null,2));console.error(error);process.exitCode=1;
 }
 finally{await browser.close();closeStreams();server.closeAllConnections();await new Promise(done=>server.close(done));}
}
