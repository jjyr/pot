// Isolated production bootstrap, HTTP/SSE and committed IndexedDB. No model,
// operator account, real assistant or existing browser profile is involved.
import {tmpdir} from 'node:os';
import {installFixtureWebSocket} from './helpers/ws-server.mjs';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {mkdir,writeFile,mkdtemp,rm} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import {shellAssets} from './pwa-shell.mjs';

const root=resolve(process.env.POT_CACHE_ENTRY_SOURCE||resolve(import.meta.dirname,'../..'));
const output=resolve(process.env.POT_CACHE_ENTRY_OUTPUT||'.pot-artifacts/cache-entry');
await mkdir(output,{recursive:true});
const {files}=await shellAssets(root),calls=[],held=new Set(),streams=new Set(),gates=new Set(),callWatchers=new Set();
const capabilities={version:1,operations:['chat.read','chat.events','chat.history','chat.send','assistant.profile','task.read','task.list']};
let failHistory=false;
const runtime={runtime_id:'cache-entry-control',boot_id:'cache-entry-boot',host:{hostname:'Synthetic Host'}};
const definitions=['alpha','beta'].map(id=>({id,name:'Synthetic '+id,revision:1,phase:'ready',capabilities}));
const message=(owner,n)=>({sequence:String(n),version:1,message:{id:owner+'-'+n,role:n%2?'assistant':'user',...(n%2?n>1?{reply_to:owner+'-'+(n-1)}:{}:{request_id:owner+'-turn-'+n}),status:'complete',text:`${owner.toUpperCase()} committed ${n}. `+'Local readable content. '.repeat(8),...([20,100].includes(n)?{references:[{message_id:owner+'-'+(n===20?100:20),quote:'Cached local reference'}]}:{})}});
// Real bounded reset frames include the owning input as a context dependency.
// Omitting it makes the first assistant acquire a different reply view key when
// an earlier page arrives, which is not a same-node cache-paging fixture.
const frame=(owner,first=81,last=160)=>({kind:'reset',base_cursor:0,view:{state:{api_version:3,cursor:10,boot_id:runtime.boot_id,conversation_id:'chat-'+owner,turn:null,agent:{phase:'ready',model:'synthetic'}},replies:[],messages:[...(first>1&&first%2?[{...message(owner,first-1),context_only:true}]:[]),...Array.from({length:last-first+1},(_,i)=>message(owner,first+i))],history:{before:String(first),after:String(last),has_more:first>1,has_newer:false}}});
const gateFor=path=>path==='/access/status'?'auth':path==='/api/v1/runtime'?'runtime':['/api/v1/spaces','/api/v1/assistants','/api/v1/agent-links'].includes(path)?'directory':path==='/api/v1/events'?'sse':path==='/api/v1/history'?'history':'';
function release(group){gates.delete(group);for(const entry of [...held])if(entry.group===group){held.delete(entry);entry.run();}}
const server=createServer(async(req,res)=>{try{
 const url=new URL(req.url,'http://fixture'),path=url.pathname;
 if(path==='/seed'){res.setHeader('Content-Type','text/html');res.end('<!doctype html><title>Synthetic cache seed</title>');return;}
 const call={path:req.url,method:req.method,at:Date.now()};calls.push(call);for(const watcher of callWatchers)watcher();
 const run=()=>{
  if(res.destroyed)return;
  res.setHeader('Content-Type','application/json');let value={};const owner=url.searchParams.get('assistant_id')||'alpha';
  if(path==='/access/status')value={authenticated:true,scope:'control'};
  if(path==='/api/v1/runtime')value=runtime;
  if(path==='/api/v1/spaces')value={active_space_id:'local',items:[{id:'local',control_id:runtime.runtime_id,name:'Synthetic Space',kind:'local',connected:true}]};
  if(path==='/api/v1/assistants')value=definitions;
  if(path==='/api/v1/agent-links')value={items:[]};
  if(path==='/api/v1/assistants/bootstrap')value={assistant:null};
  if(path==='/api/v1/permissions')value={pending:[],policy:{machines:{},rules:[],task_grants:[]}};
  if(path==='/api/v1/tasks/catalog'||path==='/api/v1/tasks/query')value={items:[],total:0,counts:{},next_offset:null};
  if(path==='/api/v1/snapshot')value=frame(owner);
  if(path==='/api/v1/history'){
   if(failHistory){res.writeHead(503);res.end(JSON.stringify({code:'synthetic_history_unavailable'}));return;}
   const pivot=Number(url.searchParams.get('before')||0),after=Number(url.searchParams.get('after')||0),around=Number((url.searchParams.get('around')||'').split('-').at(-1));
   const first=pivot?Math.max(1,pivot-80):after?after+1:around?Math.max(1,around-40):81,last=Math.min(160,first+79);
   value={boot_id:runtime.boot_id,conversation_id:'chat-'+owner,cursor:'10',messages:Array.from({length:last-first+1},(_,i)=>message(owner,first+i)),history:{before:String(first),after:String(last),has_more:first>1,has_newer:last<160}};
  }
  if(path==='/api/v1/events'||path==='/api/v1/changes'){
   res.setHeader('Content-Type','text/event-stream');res.setHeader('Cache-Control','no-store');res.flushHeaders();streams.add(res);res.on('close',()=>streams.delete(res));
   if(path.endsWith('/events'))res.write('event: conversation\ndata: '+JSON.stringify(frame(owner))+'\n\n');
   return;
  }
  if(path.startsWith('/api/')||path==='/access/status'){call.released=Date.now();res.end(JSON.stringify(value));return;}
  const file=files.get(path==='/'?'/index.html':path);if(!file){res.writeHead(404).end();return;}
  res.setHeader('Cache-Control','no-store');res.setHeader('Content-Type',({'.js':'text/javascript','.mjs':'text/javascript','.css':'text/css','.html':'text/html','.json':'application/json','.webmanifest':'application/manifest+json','.ttf':'font/ttf','.png':'image/png','.svg':'image/svg+xml'})[extname(path==='/'?'/index.html':path)]||'application/octet-stream');res.end(file);
 };
 const group=gateFor(path);if(gates.has(group)){const entry={group,run};held.add(entry);res.on('close',()=>held.delete(entry));}else run();
}catch(error){res.writeHead(500).end(error.stack)}});
const fixtureWS=await installFixtureWebSocket(server);
await new Promise(done=>server.listen(0,'127.0.0.1',done));
const origin=`http://127.0.0.1:${server.address().port}`;
function historyCalls(count){const countNow=()=>calls.filter(call=>call.path.startsWith('/api/v1/history')).length;return new Promise((resolve,reject)=>{let timer;const check=()=>{if(countNow()<count)return;clearTimeout(timer);callWatchers.delete(check);resolve();};timer=setTimeout(()=>{callWatchers.delete(check);reject(Error(`Expected ${count} history requests; observed ${countNow()}`));},5000);callWatchers.add(check);check();});}
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE||'playwright');
const profile=await mkdtemp(resolve(tmpdir(),'pot-cache-entry-'));
let browser;const evidence=[],errors=[];
async function observe(page,label,{storage=true}={}){const value=await page.evaluate(async storage=>{
 const history=document.querySelector('.history[role="log"]'),box=history?.getBoundingClientRect();
 const stored=storage?await new Promise((resolve,reject)=>{const op=indexedDB.open('pot-chat-cache');op.onerror=()=>reject(op.error);op.onsuccess=()=>{const db=op.result,tx=db.transaction(['conversations','views'],'readonly'),data={};for(const store of ['conversations','views']){const read=tx.objectStore(store).getAll();read.onsuccess=()=>data[store]=read.result;}tx.oncomplete=()=>{db.close();resolve(data);};tx.onabort=()=>reject(tx.error);};}):null;
 return {at:Date.now(),selected:document.querySelector('.assistant-row.is-selected')?.dataset.viewKey,stored,visible:!!history&&getComputedStyle(history).visibility==='visible'&&!history.hidden,busy:history?.getAttribute('aria-busy'),top:history?.scrollTop,rows:[...document.querySelectorAll('.message-window article.message')].filter(row=>!row.closest('[data-conversation-hold]')).map(row=>{const r=row.getBoundingClientRect();return {id:row.dataset.messageId,top:r.top-(box?.top||0),height:r.height,visible:r.bottom>box?.top&&r.top<box?.bottom};}),sendDisabled:document.querySelector('.send-button')?.disabled,text:document.body.innerText,trace:window.cacheEntryTrace};
 },storage);evidence.push({label,gates:[...gates],calls:structuredClone(calls),...value});await writeFile(resolve(output,'evidence.json'),JSON.stringify({evidence,errors},null,2));return value;}
async function readable(page,owner='alpha'){
 await page.waitForFunction(owner=>{const h=document.querySelector('.history[role="log"]'),b=h?.getBoundingClientRect();return h&&getComputedStyle(h).visibility==='visible'&&!h.hidden&&[...h.querySelectorAll('.message-window article.message')].some(r=>!r.closest('[data-conversation-hold]')&&r.dataset.messageId?.startsWith(owner+'-')&&r.getBoundingClientRect().bottom>b.top&&r.getBoundingClientRect().top<b.bottom);},owner,{timeout:7000});
}
async function wheel(page,delta){const h=page.locator('.history[role="log"]:not([data-preparation-surface])');await h.hover();const before=await h.evaluate(h=>h.scrollTop);await page.mouse.wheel(0,delta);await page.waitForFunction(before=>Math.abs(document.querySelector('.history[role="log"]').scrollTop-before)>24,before,{timeout:3000});}
async function select(page,owner,width){if(width<500&&!await page.locator('.assistant-row').first().isVisible())await page.getByRole('button',{name:'Back to Assistants',exact:true}).click();await page.getByText('Synthetic '+owner,{exact:true}).first().click();await readable(page,owner);}
async function localReferences(page){await page.locator('.history[role="log"]:not([data-preparation-surface]) [data-message-id="alpha-100"] .message-reference-open').click();await page.waitForFunction(()=>!!document.querySelector('.history[role="log"]:not([data-preparation-surface]) .message-window [data-message-id="alpha-20"]'));await page.locator('.history[role="log"]:not([data-preparation-surface]) [data-message-id="alpha-20"] .message-reference-open').click();await page.waitForFunction(()=>!!document.querySelector('.history[role="log"]:not([data-preparation-surface]) .message-window [data-message-id="alpha-100"]'));await readable(page);}
try{
 browser=await chromium.launchPersistentContext(profile,{headless:true,executablePath:process.env.POT_CHROMIUM_EXECUTABLE,args:['--no-sandbox'],viewport:{width:1280,height:900}});
 browser.on('page',page=>page.on('pageerror',error=>errors.push(error.stack)));
 for(const width of (process.env.POT_CACHE_ENTRY_WIDTHS||'1280,390').split(',').map(Number)){
  failHistory=false;
  const context=await browser.browser().newContext({viewport:{width,height:900}}),page=await context.newPage();page.on('pageerror',error=>errors.push(error.stack));
  await page.goto(origin+'/seed');
  const seed=await page.evaluate(async({runtime,frames})=>{
   const {createProductionClient,normalizeSnapshot}=await import('/adapters/production.mjs');
   const {createChatCache}=await import('/state/chat-cache.mjs');
   const {createStartupIdentity}=await import('/state/startup-identity.mjs');
   const client=await createProductionClient(),cache=createChatCache(),startup=createStartupIdentity();
   // Populate the real projection stores through their production commit path.
   await client.resources.read('/api/v1/spaces',{space:'',owner:''});client.spaces=[{id:'local',control_id:runtime.runtime_id}];
   await Promise.all(['/api/v1/assistants','/api/v1/agent-links','/api/v1/runtime'].map(path=>client.resources.read(path,{space:'local',owner:''})));
   const result=[];
   for(const owner of ['alpha','beta']){
    const key=JSON.stringify([runtime.runtime_id,'local',owner]),snapshot=normalizeSnapshot(frames[owner],runtime,owner,'local');
    await cache.writeFrames(key,[snapshot],{surface:'seed'});
    const older=normalizeSnapshot(frames[owner+'Older'],runtime,owner,'local');
    await cache.writeRecords(key,older.messages,{before:'1',after:'40'});
    const position={mode:'reading',position:{top:0,anchors:[{key:'message:input:'+owner+'-turn-100',offset:0}],anchor:{id:owner+'-100',sequence:'100',offset:0}}};
    await cache.saveView(key,'seed',{position,conversation_id:snapshot.conversation_id,boot_id:snapshot.boot_id,cursor:snapshot.cursor,window:snapshot.messages.map(m=>({id:m.id,context_only:!!m.context_only})),tools:[],heights:[]});
    const saved=await cache.read(key,'seed');if(saved.snapshot.runtime_id!==runtime.runtime_id||saved.snapshot.owner_id!==JSON.stringify(['local',owner])||saved.snapshot.boot_id!==runtime.boot_id||saved.snapshot.conversation_id!=='chat-'+owner||!saved.snapshot.messages.some(m=>m.id===owner+'-100'))throw Error('Seed identity/commit mismatch');
    result.push({key,owner,conversation:saved.snapshot.conversation_id,boot:saved.snapshot.boot_id,committed:Date.now(),count:saved.snapshot.messages.length});
   }
   await startup.save(runtime,{space:'local',owner:'alpha'},'seed');client.resources.clear();cache.close();startup.close();return result;
  },{runtime,frames:{alpha:frame('alpha'),beta:frame('beta'),alphaOlder:frame('alpha',1,40),betaOlder:frame('beta',1,40)}});
  evidence.push({label:'committed-seed-'+width,seed});
  await page.evaluate(async()=>{await navigator.serviceWorker.register('/service-worker.js',{scope:'/',type:'module'});await navigator.serviceWorker.ready;});
  await page.addInitScript(()=>{
   window.cacheEntryTrace=[];
   const original=IDBDatabase.prototype.transaction,put=IDBObjectStore.prototype.put,transactions=new WeakMap();let serial=0;
   IDBDatabase.prototype.transaction=function(stores,mode,...args){const tx=original.call(this,stores,mode,...args),id=++serial;transactions.set(tx,id);if(mode==='readwrite')tx.addEventListener('complete',()=>window.cacheEntryTrace.push({kind:'commit',id,stores:[...tx.objectStoreNames],at:performance.now()}));return tx;};
   IDBObjectStore.prototype.put=function(value,...args){if(this.name==='messages')window.cacheEntryTrace.push({kind:'message-put',id:transactions.get(this.transaction),message:value.id,at:performance.now()});return put.call(this,value,...args);};
   const scrollTop=Object.getOwnPropertyDescriptor(Element.prototype,'scrollTop');
   Object.defineProperty(Element.prototype,'scrollTop',{...scrollTop,set(value){scrollTop.set.call(this,value);if(this.matches('.history[role="log"]:not([data-preparation-surface])')){const actual=scrollTop.get.call(this);cacheEntryTrace.push({kind:'scroll-write',requested:value,actual,residual:value-actual,at:performance.now()});}}});
   const track=()=>{const h=document.querySelector('.history[role="log"]:not([data-preparation-surface])'),visible=h&&getComputedStyle(h).visibility==='visible'&&!h.hidden,rows=h?[...h.querySelectorAll('article.message')].filter(r=>!r.closest('[data-conversation-hold]')).map(r=>r.dataset.messageId):[],preparedRows=[...document.querySelectorAll('[data-preparation-surface] article.message')].map(r=>r.dataset.messageId);const key=JSON.stringify([visible,rows,preparedRows]);if(key!==window.cacheEntryLast){window.cacheEntryLast=key;window.cacheEntryTrace.push({kind:'frame',at:performance.now(),visible,rows,preparedRows,prepared:!!document.querySelector('[data-prepared-pending]')});}requestAnimationFrame(track);};requestAnimationFrame(track);
  });
  for(const gate of ['auth','runtime','directory','sse','history'])gates.add(gate);
  await page.goto(origin+'/?locale=en');await readable(page);let seen=await observe(page,'held-bootstrap-'+width);assert(seen.sendDisabled,'empty cached composer stays disabled');assert(seen.rows.some(r=>r.id==='alpha-100'&&r.visible),'committed reading anchor visible before network release');
  await wheel(page,140);await wheel(page,-100);await observe(page,'held-local-wheel-'+width);
  await localReferences(page);await observe(page,'held-local-references-'+width);
  await select(page,'beta',width);await select(page,'alpha',width);await observe(page,'held-A-B-A-'+width);
  assert.equal(calls.filter(c=>c.path.startsWith('/api/v1/history')).length,0,'cached entry and local wheel must not fetch history');
  // With auth, runtime and directory all held, the confirmed startup locator must
  // remain intact: the product never overwrites a confirmed startup until access
  // is verified, so the cached navigation path must keep working off the seed
  // row rather than committing a new surface. Assert the row is unchanged.
  await page.evaluate(async()=>{const {createStartupIdentity}=await import('/state/startup-identity.mjs');const db=createStartupIdentity();try{const row=await db.read();if(!(row?.runtime_id==='cache-entry-control'&&row.space==='local'&&row.owner==='alpha'&&row.surface==='seed'))throw Error('Confirmed startup identity was overwritten while access held: '+JSON.stringify(row));}finally{db.close();}});
  await page.evaluate(()=>navigator.serviceWorker.ready);await page.waitForFunction(()=>!!navigator.serviceWorker.controller);
  await context.setOffline(true);await page.reload();try{await readable(page);}catch(error){await observe(page,'offline-reopen-blocked-'+width);throw error;}await wheel(page,100);await localReferences(page);await observe(page,'offline-reopen-'+width);await context.setOffline(false);
  // Missing primary anchor with a valid local fallback models an evicted row.
  // Opening it must expose the committed local window rather than await around.
  release('auth');release('runtime');release('directory');
  await page.reload();await readable(page);await page.waitForFunction(()=>!document.querySelector('#app').classList.contains('is-local-startup'));
  await select(page,'beta',width);
  await page.evaluate(async()=>{const {createIndexedStore,idbRequest}=await import('/state/indexed-store.mjs');const db=createIndexedStore();await db.transaction(['views'],'readwrite',async tx=>{const s=tx.objectStore('views'),views=await idbRequest(s.getAll());for(const row of views)if(row.key===JSON.stringify(['cache-entry-control','local','alpha'])){row.position={mode:'reading',position:{top:0,anchors:[{key:'message:input:alpha-turn-100',offset:0}],anchor:{id:'alpha-60',sequence:'60',offset:0}}};s.put(row);}});db.close();});
  try{await select(page,'alpha',width);}catch(error){await observe(page,'missing-anchor-blocked-'+width);await page.screenshot({path:resolve(output,'cache-entry-'+width+'-before.png')});throw error;}
  seen=await observe(page,'missing-anchor-local-fallback-'+width);
  assert(seen.rows.some(row=>row.id==='alpha-100'&&row.visible),'the available captured anchor positions the cached viewport');
  await page.screenshot({path:resolve(output,'cache-entry-'+width+'-after.png')});
  assert.equal(calls.filter(c=>c.path.startsWith('/api/v1/history')).length,1,'only the requested missing restore anchor begins historical IO');
  // Keep the same HTTP gap held beyond the original seven-second negative
  // window, checking every paint while exercising native wheel input.
  const heldAt=Date.now();
  for(let i=0;i<8;i++){await wheel(page,i%2?-100:100);await page.evaluate(()=>new Promise((resolve,reject)=>{const until=performance.now()+1000;function frame(){const h=document.querySelector('.history[role="log"]:not([data-preparation-surface])');if(!h||getComputedStyle(h).visibility!=='visible'||h.getAttribute('aria-busy')==='true'){reject(Error('Held gap blocked the local viewport'));return;}if(performance.now()>=until)resolve();else requestAnimationFrame(frame);}requestAnimationFrame(frame);}));}
  assert(Date.now()-heldAt>7000);await observe(page,'restore-gap-over-seven-seconds-readable-'+width);
  // First genuine boundary demand begins asynchronous history IO.
  const entryTop=await page.locator('.history[role="log"]:not([data-preparation-surface])').evaluate(h=>h.scrollTop);
  await page.locator('.history[role="log"]:not([data-preparation-surface])').hover();await page.mouse.wheel(0,-100000);
  await page.waitForFunction(()=>document.querySelector('.history[role="log"]').scrollTop<1);
  await page.mouse.wheel(0,-120);await historyCalls(2);
  assert([...held].some(entry=>entry.group==='history'),'history request remains held');
  assert.strictEqual((await page.locator('.history-page-status').first().textContent()).trim(),'Load earlier messages');
  await readable(page);await wheel(page,140);await observe(page,'pending-history-readable-'+width);
  await wheel(page,entryTop-await page.locator('.history[role="log"]:not([data-preparation-surface])').evaluate(h=>h.scrollTop));
  await page.locator('.history[role="log"]:not([data-preparation-surface]) [data-message-id="alpha-100"] .message-reference-open').click();
  await page.waitForFunction(()=>{const row=document.querySelector('.message-window [data-message-id="alpha-20"]'),h=document.querySelector('.history[role="log"]');return row&&row.getBoundingClientRect().top<h.getBoundingClientRect().bottom&&row.getBoundingClientRect().bottom>h.getBoundingClientRect().top;},null,{timeout:5000});
  await observe(page,'pending-gap-local-reference-'+width);
  assert.equal(calls.filter(c=>c.path.startsWith('/api/v1/history')).length,2,'a cached reference must bypass the pending restore and boundary gaps');
  await page.locator('.history[role="log"]:not([data-preparation-surface]) [data-message-id="alpha-20"] .message-reference-open').click();await readable(page);
  await page.waitForFunction(()=>!!document.querySelector('.message-window [data-message-id="alpha-100"]'));
  await select(page,'beta',width);await select(page,'alpha',width);await observe(page,'pending-gap-switch-return-'+width);
  await page.locator('.history[role="log"]:not([data-preparation-surface])').hover();await page.mouse.wheel(0,-100000);await page.mouse.wheel(0,-120);
  assert([...held].some(entry=>entry.group==='history'),'history request remains held');
  assert.strictEqual((await page.locator('.history-page-status').first().textContent()).trim(),'Load earlier messages');await wheel(page,140);
  // Keep an actual IDB write transaction pending while the HTTP page arrives.
  // Existing measured content must remain scrollable until its commit.
  await page.evaluate(async()=>{const db=await new Promise((resolve,reject)=>{const op=indexedDB.open('pot-chat-cache');op.onsuccess=()=>resolve(op.result);op.onerror=()=>reject(op.error);});const tx=db.transaction(['conversations','messages','views','cacheStats'],'readwrite');let released=false;const pump=()=>{const get=tx.objectStore('conversations').get(JSON.stringify(['cache-entry-control','local','alpha']));get.onsuccess=()=>{if(!released)pump();};};pump();window.cacheEntryCommitGate={release:()=>{released=true;},done:new Promise((resolve,reject)=>{tx.oncomplete=()=>{db.close();resolve();};tx.onabort=()=>reject(tx.error);})};});
  release('history');
  assert(calls.some(call=>call.path.startsWith('/api/v1/history')&&call.released),'history response released');
  await wheel(page,140);await wheel(page,-80);await observe(page,'pending-IDB-commit-readable-'+width,{storage:false});
  const anchorBefore=await page.evaluate(()=>{const h=document.querySelector('.history[role="log"]'),b=h.getBoundingClientRect(),row=[...h.querySelectorAll('.message-window article.message')].find(r=>!r.closest('[data-conversation-hold]')&&r.getBoundingClientRect().bottom>b.top);window.cacheEntryReading={node:row,id:row.dataset.messageId,offset:row.getBoundingClientRect().top-b.top};return {id:window.cacheEntryReading.id,offset:window.cacheEntryReading.offset};});
  await page.evaluate(async()=>{cacheEntryTrace.push({kind:'release-IDB',at:performance.now()});window.cacheEntryCommitGate.release();await window.cacheEntryCommitGate.done;});
  await page.waitForFunction(() => {
  const put = cacheEntryTrace.find(entry =>
    entry.kind === 'message-put' &&
    /^alpha-(?:4[1-9]|[5-7][0-9])$/.test(entry.message)
  );
  return !!put && cacheEntryTrace.some(entry =>
    entry.kind === 'commit' && entry.id === put.id
  );
});

await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await readable(page);seen=await observe(page,'history-committed-'+width);assert(seen.rows.some(r=>r.id===anchorBefore.id&&r.visible),'late page preserves current reader anchor');
  const retained=await page.evaluate(async()=>{const h=document.querySelector('.history[role="log"]'),row=h.querySelector(`[data-message-id="${cacheEntryReading.id}"]`),samples=[];for(let i=0;i<8;i++){h.dispatchEvent(new Event('scroll'));await new Promise(requestAnimationFrame);samples.push({top:h.scrollTop,offset:row.getBoundingClientRect().top-h.getBoundingClientRect().top});}const drift=samples.at(-1).offset-cacheEntryReading.offset,release=cacheEntryTrace.findLast(entry=>entry.kind==='release-IDB'),writes=cacheEntryTrace.filter(entry=>entry.kind==='scroll-write'&&entry.at>=release.at);return {same:row===cacheEntryReading.node,drift,samples,writes,quantization:writes.findLast(write=>Math.abs(write.residual-drift)<1e-7)};});
  evidence.push({label:'anchor-retention-'+width,retained});
  assert(retained.same&&Math.abs(retained.drift)<=3,'late page retains the same anchor DOM node within the existing 3px bound: '+JSON.stringify(retained));
  assert((Math.abs(retained.drift)<1e-7||retained.quantization)&&Math.max(...retained.samples.map(sample=>sample.offset))-Math.min(...retained.samples.map(sample=>sample.offset))<1e-7,'the observed residual matches a native requested/actual write and does not accumulate across repeated publications');
  await wheel(page,-300);await observe(page,'committed-gap-browsing-'+width);
  const ordering=await page.evaluate(()=>{const gap=id=>/^alpha-(?:4[1-9]|[5-7][0-9])$/.test(id),trace=cacheEntryTrace,put=trace.find(entry=>entry.kind==='message-put'&&gap(entry.message)),commit=trace.find(entry=>entry.kind==='commit'&&entry.id===put?.id),prepare=trace.find(entry=>entry.kind==='frame'&&entry.preparedRows?.some(gap)),publish=trace.find(entry=>entry.kind==='frame'&&entry.visible&&entry.rows?.some(gap));return {put,commit,prepare,publish};});
  evidence.push({label:'commit-prepare-publish-'+width,ordering,retained});
  assert(ordering.commit&&ordering.prepare&&ordering.publish&&ordering.commit.at<=ordering.prepare.at&&ordering.prepare.at<=ordering.publish.at,'uncached rows commit before hidden preparation and live publication');
  await page.screenshot({path:resolve(output,'cache-entry-'+width+'.png')});
  failHistory=true;await select(page,'beta',width);await page.locator('.history[role="log"]:not([data-preparation-surface])').hover();await page.mouse.wheel(0,-100000);await page.mouse.wheel(0,-120);
  await historyCalls(3);await page.waitForFunction(()=>document.querySelector('.history-page-status')?.textContent.includes('Could not load messages'));
  await readable(page,'beta');await wheel(page,140);seen=await observe(page,'history-failure-local-readable-'+width);assert(seen.sendDisabled,'empty composer stays disabled after historical IO failure');
  await context.close();for(const gate of [...gates])release(gate);calls.length=0;
 }
 assert.deepEqual(errors,[]);await writeFile(resolve(output,'evidence.json'),JSON.stringify({evidence,errors},null,2));console.log('PASS production cache entry, offline A/B/A, local wheel and asynchronous history');
}catch(error){evidence.push({label:'FAIL',error:error.stack});await writeFile(resolve(output,'evidence.json'),JSON.stringify({evidence,errors,calls},null,2));throw error;}
finally{await browser?.close();for(const stream of streams)stream.end();await fixtureWS.close();server.closeAllConnections();await new Promise(done=>server.close(done));await rm(profile,{recursive:true,force:true});console.log('Released browser, synthetic server and profile at '+origin);}
