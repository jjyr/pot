import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {Readable} from 'node:stream';
import {writeFile} from 'node:fs/promises';
import {installFixtureWebSocket} from './helpers/ws-server.mjs';

// Real WebSocket/HTTP, production UI/client/cache and a controlling SW. Only
// the server faults and business data are synthetic. Product deadlines run at
// their actual durations; cases use separate browser contexts and cookies.
export async function chatStreamRecovery(browser,{sourceOrigin,cases}={}){
 const states=new Map(),results=[];
 const json=(res,value,status=200)=>{res.writeHead(status,{'content-type':'application/json'});res.end(JSON.stringify(value));};
 const frame=()=>({kind:'reset',base_cursor:0,view:{state:{cursor:1,boot_id:'stream-boot',conversation_id:'main',messages:[],turn:null,agent:{phase:'ready',attachments:{image:true,pdf:true}}},messages:[],replies:[],history:{has_more:false,has_newer:false}}});
 const capabilities={version:1,scope:'owner',operations:['chat.read','chat.send','chat.subscribe','chat.completions','chat.attachments','task.list','task.read','task.events','task.outcomes']};
 const server=createServer(async(req,res)=>{try{
  const url=new URL(req.url,'http://fixture'),path=url.pathname,name=/stream-case=([^;]+)/.exec(req.headers.cookie||'')?.[1],state=states.get(name);
  if(!state){const response=await fetch(sourceOrigin+req.url);res.writeHead(response.status,Object.fromEntries(response.headers));Readable.fromWeb(response.body).pipe(res);return;}
  const send=stream=>stream.res.write(`event: conversation\ndata: ${JSON.stringify(frame())}\n\n`);
  if(path==='/fixture/state')return json(res,{statusChecks:state.statusChecks,statusAborted:state.statusAborted});
  if(path==='/fixture/control'){
   let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);
   if(body.fault){state.fault=body.fault;if(body.fault==='headers'||body.fault==='reset')state.faultUntil=Date.now()+21000;}
   if('authDenied'in body)state.authDenied=body.authDenied;
   if(body.changesFault)state.changesFault=body.changesFault;if('statusMode'in body)state.statusMode=body.statusMode;
   if(body.rename){state.alphaName=body.rename;state.directoryVersion=(state.directoryVersion||1)+1;}
   if(body.publishChanges)for(const stream of state.streams)if(stream.path.endsWith('/changes'))stream.res.write(`id: stream-boot:${state.directoryVersion||1}\nevent: changed\ndata: ${JSON.stringify({assistants:state.directoryVersion||1})}\n\n`);
   if(body.silence){for(const stream of state.streams)stream.silent=true;}
   if(body.end)for(const stream of [...state.streams])if(stream.path.endsWith('/events'))stream.res.end();
   if(body.endChanges)for(const stream of [...state.streams])if(stream.path.endsWith('/changes'))stream.res.end();
   return json(res,{});
  }
  if(path==='/api/v1/events'||path==='/api/v1/changes'){
   const event=path.endsWith('/events');
   let mode;
   if(event){
    const sticky=state.fault==='headers'||state.fault==='reset';
    if(sticky&&state.faultUntil&&Date.now()<state.faultUntil){mode=state.fault;}
    else{if(sticky){state.fault=null;state.faultUntil=null;}mode=state.fault;state.fault=null;}
    state.subscriptions++;
   }else{
    mode=state.changesFault;state.changesFault=null;state.changeSubscriptions++;
   }
   const stream={res,path,owner:url.searchParams.get('assistant_id')};state.streams.add(stream);req.on('close',()=>state.streams.delete(stream));
   if(mode==='auth'||mode==='closed')return json(res,{},401);if(mode==='headers')return;
   res.writeHead(200,{'content-type':'text/event-stream','cache-control':'no-cache'});res.write(': opened\n\n');
   if(event&&mode!=='reset')send(stream);
   if(!event)res.write('id: stream-boot:1\nevent: changed\ndata: {"assistants":1}\n\n');
   const timer=setInterval(()=>{if(!stream.silent)res.write('event: heartbeat\ndata: {}\n\n');},15000);req.on('close',()=>clearInterval(timer));return;
  }
  if(path==='/access/status'){
   state.statusChecks++;
   if(state.statusMode==='timeout'){req.on('close',()=>{if(!res.writableEnded)state.statusAborted++;});return;}
   if(state.statusMode==='unknown')return json(res,{});
   if(state.statusMode==='network'){res.destroy();return;}
   return json(res,{authenticated:!state.authDenied},state.statusMode==='denied'?401:state.statusMode==='forbidden'?403:200);
  }
  if(path==='/api/v1/assistants'){
   state.directoryReads=(state.directoryReads||0)+1;
   return json(res,['alpha','beta'].map(id=>({id,name:id==='alpha'&&state.alphaName?state.alphaName:'Synthetic '+id,revision:state.directoryVersion||1,capabilities})));
  }
  if(path==='/api/v1/attachments'&&req.method==='POST'){
   let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);let item=state.uploads.get(body.id);
   if(body.action==='begin'){item={attachment:{id:body.id,name:body.name,mime_type:body.mime_type,size:body.size},offset:0};state.uploads.set(body.id,item);}
   if(body.action==='chunk')item.offset+=Buffer.from(body.data,'base64').length;if(body.action==='finish')item.ready=true;
   return json(res,item);
  }
  if(path==='/api/v1/messages'&&req.method==='POST'){state.messageWrites++;for await(const _ of req){}return json(res,{ok:true});}
  if(path.startsWith('/api/'))return json(res,path==='/api/v1/runtime'?{runtime_id:'stream-fixture',boot_id:'stream-boot'}:path==='/api/v1/spaces'?{active_space_id:'local',control_id:'stream-fixture',items:[{id:'local',kind:'local',name:'Synthetic',connected:true,control_id:'stream-fixture'}]}:path==='/api/v1/assistants/bootstrap'?{assistant:null}:path==='/api/v1/snapshot'?{...frame(),boot_id:'stream-boot'}:path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:{items:[],total:0,cursor:0,has_more:false});
  const response=await fetch(sourceOrigin+req.url);res.writeHead(response.status,Object.fromEntries(response.headers));Readable.fromWeb(response.body).pipe(res);
 }catch(error){json(res,{message:error.message},500);}});
 const transport=await installFixtureWebSocket(server,{heartbeatMs:0});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const origin='http://127.0.0.1:'+server.address().port;
 let tail=Promise.resolve();
 const runScenario=async(name,run)=>{
  if(cases&&!cases.includes(name))return;
 const state={streams:new Set(),uploads:new Map(),subscriptions:0,changeSubscriptions:0,statusChecks:0,statusAborted:0,messageWrites:0};states.set(name,state);
  const context=await browser.newContext({viewport:{width:390,height:852}}),page=await context.newPage(),errors=[];
  page.setDefaultTimeout(30000);
  if(name==='paused-clock-owner-and-list-reentry')await page.clock.install();
  await context.addCookies([{name:'stream-case',value:name,url:origin}]);page.on('pageerror',e=>errors.push(e.message));
  await context.addInitScript(()=>{
window.__sockets=[];window.__streams=[];window.__statusAborts=0;window.__probeChecks=0;window.__duplicates=0;
   // Real native WebSocket subclass. Instrumentation only. Stores every
   // subscription record on window.__streams as {id,path,state,socket} where
   // state is 0=pending, 1=live, 2=closed; predicates filter on `state`.
   const Native=window.WebSocket;
   class Instrumented extends Native{
    constructor(url,protocols){super(url,protocols);window.__sockets.push(this);
     this.addEventListener('close',()=>{for(const rec of window.__streams)if(rec.socket===this)rec.state=2;});
     const send=this.send.bind(this);
 this.send=data=>{try{const msg=JSON.parse(data);if(msg&&msg.type==='subscribe'){const path=new URL(msg.path,location.origin).pathname;if(window.__streams.some(r=>r.socket===this&&r.path===path&&r.state!==2))window.__duplicates++;window.__streams.push({id:msg.id,path,state:0,socket:this});}if(msg&&msg.type==='unsubscribe'){for(const rec of window.__streams)if(rec.id===msg.id&&rec.socket===this)rec.state=2;}}catch{}return send(data);};
     this.addEventListener('message',event=>{let msg;try{msg=JSON.parse(event.data);}catch{return;}if(!msg||typeof msg!=='object')return;
const recs=window.__streams.filter(r=>r.socket===this);
if(msg.type==='response'&&msg.id!==undefined){
  const status=msg.status;
  for(const rec of recs)if(rec.id===msg.id){
    if(typeof status==='number'&&status>=400)rec.state=2;
    else if(status===200&&rec.state===0)rec.state=1;
  }
}else if(msg.type==='end'&&msg.id!==undefined){
  for(const rec of recs)if(rec.id===msg.id)rec.state=2;
}else if(msg.type==='event'&&msg.id!==undefined){
  for(const rec of recs)if(rec.id===msg.id&&rec.state===0)rec.state=1;
}
     });
    }
   }
   Object.defineProperty(Instrumented,'CONNECTING',{value:0});Object.defineProperty(Instrumented,'OPEN',{value:1});Object.defineProperty(Instrumented,'CLOSING',{value:2});Object.defineProperty(Instrumented,'CLOSED',{value:3});
   window.WebSocket=Instrumented;
   const nativeFetch=fetch;window.fetch=(url,options)=>{if(String(url).endsWith('/access/status')&&options?.signal){window.__probeChecks++;options.signal.addEventListener('abort',()=>window.__statusAborts++,{once:true});}return nativeFetch(url,options);};
  });
  const control=body=>context.request.post(origin+'/fixture/control',{data:body});
  const ready=()=>page.waitForFunction(()=>document.querySelector('.connection-status')?.classList.contains('is-connected'),{},{timeout:55000});
const sample=()=>page.evaluate(()=>({states:window.__streams.map(s=>({path:s.path,state:s.state})),sockets:window.__sockets.map(s=>({state:s.readyState})),duplicates:window.__duplicates,draft:document.querySelector('#pot-composer')?.value,status:document.querySelector('.connection-status')?.textContent,addTitle:document.querySelector('.attachment-plus')?.title,sw:!!navigator.serviceWorker.controller}));
  const liveCount=async path=>page.evaluate(p=>window.__streams.filter(s=>s.path===p&&s.state!==2).length,path);
  const started=Date.now();
  console.log('WS recovery: '+name);
  try{
   await page.goto(origin+'/?locale=en&assistant_id=alpha&conversation=main');await ready();let timeoutId;
try {
  await Promise.race([
    page.evaluate(async () => {
      await navigator.serviceWorker.ready;
      return true;
    }),
    new Promise((_, reject) => {
      timeoutId = setTimeout(
        () => reject(new Error('Service Worker readiness timed out')),
        10000
      );
    }),
  ]);
} finally {
  clearTimeout(timeoutId);
}await page.reload();await ready();await page.waitForFunction(()=>!!navigator.serviceWorker.controller);
   // Shell installation and its first controlled reopen are setup, outside the
   // single-live-subscription baseline used by the fault recipes below.
   state.subscriptions=state.changeSubscriptions=1;state.statusChecks=state.statusAborted=0;
await page.evaluate(()=>{window.__streams=window.__streams.filter(r=>r.state!==2);window.__sockets=window.__sockets.filter(s=>s.readyState!==3);window.__probeChecks=window.__statusAborts=0;window.__duplicates=0;});
   state.statusChecks=state.statusAborted=0;
   await page.locator('#pot-composer').fill('retained synthetic draft');await run({page,context,state,control,ready,sample,liveCount,origin});assert.deepEqual(errors,[]);
results.push({name,ok:true,ms:Date.now()-started,subscriptions:state.subscriptions,changeSubscriptions:state.changeSubscriptions,statusChecks:state.statusChecks,statusAborted:state.statusAborted,messageWrites:state.messageWrites,sample:await sample()});
}catch(error){results.push({name,ok:false,error:error.stack,errors,ms:Date.now()-started,subscriptions:state.subscriptions,changeSubscriptions:state.changeSubscriptions,messageWrites:state.messageWrites,sample:await sample().catch(()=>null)});}
  finally{console.log('WS recovery done: '+name);await context.close();for(const s of state.streams)s.res.end();}
 };
 const scenario=(name,run)=>{const result=tail.then(()=>runScenario(name,run));tail=result.catch(()=>{});return result;};
 try{await Promise.all([
  scenario('terminal-probe-offline-online-race',async({page,state,control,ready,origin})=>{
   await control({fault:'closed',statusMode:'timeout',end:true});
   await page.waitForFunction(()=>window.__streams.some(r=>r.path==='/api/v1/events'&&r.state===2)&&!document.querySelector('.connection-status')?.classList.contains('is-connected'));
   await page.waitForFunction(async()=>{const s=(await(await fetch('/fixture/state')).json()).statusChecks;return s>=1&&s<=2;});
   assert(state.statusChecks>=1&&state.statusChecks<=2,'The real pending status request must have started');
   await control({statusMode:null});
   await page.evaluate(()=>{let online=false;Object.defineProperty(navigator,'onLine',{get:()=>online,configurable:true});window.dispatchEvent(new Event('offline'));online=true;window.dispatchEvent(new Event('online'));});
   await ready();assert(await page.evaluate(()=>window.__statusAborts)>=1,'Offline cancels the actual fetch AbortSignal');assert.equal(await page.evaluate(()=>window.__streams.filter(r=>r.path==='/api/v1/events'&&r.state!==2).length),1);
  }),
  scenario('changes-terminal-probe-timeout-recovers',async({page,state,control,ready,liveCount})=>{
await control({changesFault:'closed',statusMode:'timeout',endChanges:true});
   await page.waitForFunction(async()=>((await(await fetch('/fixture/state')).json()).statusChecks)>0);
   await page.waitForTimeout(10500);assert(state.statusAborted>0,'The actual bounded access probe must abort');
   assert.equal(await page.locator('.connection-status').evaluate(el=>el.classList.contains('is-connected')),false,'Pending access must not mark the UI connected');
   assert.equal(state.messageWrites,0,'No message may be written while access is pending');
   await control({statusMode:null});const retry=page.locator('.startup-status button');if(await retry.count())await retry.click();await ready();
   const reads=state.directoryReads;await control({rename:'Recovered alpha',publishChanges:true});
   await page.waitForFunction(()=>document.querySelector('.assistant-profile-trigger')?.textContent.includes('Recovered alpha'));
   assert(state.directoryReads>reads,'Reconfirmed changes must invalidate, commit and render the directory');
   assert.equal(await liveCount('/api/v1/changes'),1);
  }),
  scenario('changes-unknown-and-network-status-recovers',async({page,state,control,ready,liveCount})=>{
   for(const statusMode of ['unknown','network']){
    const checks=state.statusChecks;await control({changesFault:'closed',statusMode,endChanges:true});
    await page.waitForFunction(async count=>(await(await fetch('/fixture/state')).json()).statusChecks>count,checks);
    await page.waitForTimeout(500);assert.equal(await page.locator('#pot-composer').inputValue(),'retained synthetic draft','An unavailable status probe must retain the local editor');
    await control({statusMode:null});const retry=page.locator('.startup-status button');if(await retry.count())await retry.click();await ready();
    assert.equal(await page.locator('#pot-composer').inputValue(),'retained synthetic draft');
   }
   assert.equal(await liveCount('/api/v1/changes'),1);
  }),
  ...['denied','forbidden'].map(statusMode=>scenario('changes-confirmed-'+statusMode+'-pauses-across-lifecycle',async({page,state,control})=>{
await control({changesFault:'closed',statusMode,endChanges:true});
   if(statusMode==='denied')await page.locator('.startup-status').getByText('Your browser session has expired.',{exact:false}).waitFor();
   else await page.locator('.startup-status').getByText('Access is not permitted.',{exact:false}).waitFor();
   assert.equal(await page.evaluate(()=>window.__sockets.filter(s=>s.readyState<=1).length),0,'No socket may be live/open while denied');
   assert.equal(state.messageWrites,0,'No message may be written while denied');
   await page.waitForTimeout(150);const count=state.changeSubscriptions;
   await page.evaluate(()=>{window.dispatchEvent(new Event('focus'));window.dispatchEvent(new Event('pageshow'));});
   await page.waitForTimeout(900);assert.equal(state.changeSubscriptions,count,'Confirmed denial must not revive the subscription on foreground events');
  })),
  scenario('first-reset-deadline-despite-heartbeats',async({page,state,control,ready})=>{
   await page.getByRole('button',{name:'Add attachment',exact:true}).click();const chooser=page.waitForEvent('filechooser');await page.getByRole('menuitem',{name:'Images',exact:true}).click();await(await chooser).setFiles({name:'synthetic.png',mimeType:'image/png',buffer:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP438DwHwAGgAJ/EEwb4QAAAABJRU5ErkJggg==','base64')});await page.waitForFunction(()=>document.querySelector('.attachment-card')&&!document.querySelector('.attachment-card progress'));
const started=Date.now();await control({fault:'reset',end:true});await page.waitForFunction(()=>!document.querySelector('.connection-status')?.classList.contains('is-connected'));await ready();assert(Date.now()-started>=20000,'Reset deadline must be exercised at its real duration');assert(state.subscriptions>=2,'Bounded re-subscription on shared socket across identity revalidation');assert((await page.evaluate(()=>window.__sockets.length))<=4,'At most four browser sockets across recovery');assert.equal(await page.evaluate(()=>window.__streams.filter(r=>r.path==='/api/v1/events'&&r.state!==2).length),1,'Exactly one live /events subscription');assert.equal(await page.evaluate(()=>window.__streams.filter(r=>r.path==='/api/v1/changes'&&r.state!==2).length),1,'Exactly one live /changes subscription');assert.equal(state.messageWrites,0,'No message may be written during recovery');assert.equal(await page.evaluate(()=>window.__duplicates),0,'No duplicate concurrent subscription per socket/path');
   assert.equal(await page.locator('.attachment-card').count(),1);assert.equal(await page.locator('#pot-composer').inputValue(),'retained synthetic draft');
  }),
  // Identifier kept for plan compatibility; this now asserts the WebSocket
  // subscribe-ACK deadline (socket open but no ACK) rather than 200-before-body.
scenario('headers-deadline',async({page,state,control,ready})=>{const started=Date.now();await control({fault:'headers',end:true});await page.waitForFunction(()=>!document.querySelector('.connection-status')?.classList.contains('is-connected'));await ready();assert(Date.now()-started>=20000,'Subscribe ACK deadline must be exercised at its real duration');assert(state.subscriptions>=2,'Bounded re-subscription after subscribe ACK deadline');assert((await page.evaluate(()=>window.__sockets.length))<=4,'At most four browser sockets across recovery');assert.equal(await page.evaluate(()=>window.__streams.filter(r=>r.path==='/api/v1/events'&&r.state!==2).length),1,'Exactly one live /events subscription');assert.equal(await page.evaluate(()=>window.__streams.filter(r=>r.path==='/api/v1/changes'&&r.state!==2).length),1,'Exactly one live /changes subscription');assert.equal(state.messageWrites,0,'No message may be written during recovery');assert.equal(await page.evaluate(()=>window.__duplicates),0,'No duplicate concurrent subscription per socket/path');assert.equal(await page.locator('#pot-composer').inputValue(),'retained synthetic draft');}),
  scenario('idle-heartbeat-expiry',async({state,control,ready,page})=>{
   // Silence must apply to every shared stream, so heartbeat absence affects the
   // whole socket and both /events and /changes subscriptions expire together.
   await control({silence:true});
   await page.waitForFunction(()=>window.__streams.filter(r=>r.path==='/api/v1/events').length>=2,{},{timeout:50000});
   await ready();
   assert.equal(await page.evaluate(()=>window.__streams.filter(r=>r.path==='/api/v1/events'&&r.state!==2).length),1);
  }),
  scenario('normal-quiet-and-foreground-burst',async({page,state,ready})=>{await page.evaluate(()=>{window.dispatchEvent(new Event('focus'));window.dispatchEvent(new Event('pageshow'));window.dispatchEvent(new Event('online'));});await page.waitForTimeout(47000);await ready();assert.equal(state.subscriptions,1);}),
  scenario('paused-clock-owner-and-list-reentry',async({page,state,control,ready})=>{
   await page.evaluate(()=>{window.__visibility='hidden';Object.defineProperty(document,'visibilityState',{get:()=>window.__visibility});document.dispatchEvent(new Event('visibilitychange'));});await page.clock.setSystemTime((await page.evaluate(()=>Date.now()))+60000);await page.evaluate(()=>{window.__visibility='visible';document.dispatchEvent(new Event('visibilitychange'));window.dispatchEvent(new Event('focus'));window.dispatchEvent(new Event('pageshow'));});await page.waitForFunction(()=>window.__streams.filter(r=>r.path==='/api/v1/events').length>=2);await ready();assert.equal(await page.evaluate(()=>window.__streams.filter(r=>r.path==='/api/v1/events'&&r.state!==2).length),1);
   // Ending /events closes the shared socket, taking /changes with it; reentry
   // through list->owner must restore exactly one shared socket.
   await control({fault:'reset',end:true});await page.waitForFunction(()=>!document.querySelector('.connection-status')?.classList.contains('is-connected'));await page.locator('.titlebar .narrow-navigation').click();await page.getByRole('button',{name:'Synthetic beta',exact:true}).click();await ready();await page.locator('.titlebar .narrow-navigation').click();await page.getByRole('button',{name:'Synthetic alpha',exact:true}).click();await ready();const count=state.subscriptions;await page.waitForTimeout(21000);assert.equal(state.subscriptions,count);assert.equal(await page.locator('#pot-composer').inputValue(),'retained synthetic draft');
  }),
  scenario('authentication-terminal-and-live-locale',async({page,state,control,origin})=>{
   await control({fault:'auth',authDenied:true,end:true});await page.getByRole('heading',{name:'Connect to Pot',exact:true}).waitFor();const count=state.subscriptions;await page.waitForTimeout(700);assert.equal(state.subscriptions,count);
   if(process.env.POT_STREAM_CAPTURE_DIR)await page.screenshot({path:process.env.POT_STREAM_CAPTURE_DIR+'/auth-after.png'});
   await page.goto(origin+'/?locale=zh-CN');await page.getByRole('heading',{name:'连接到 Pot',exact:true}).waitFor();
  }),
  scenario('commit-held-and-abort-retain-readiness-gate',async({page,state,control,ready})=>{
   await page.evaluate(async()=>{const db=await new Promise(resolve=>{const req=indexedDB.open('pot-chat-cache');req.onsuccess=()=>resolve(req.result);});const tx=db.transaction(['conversations','messages','views','cacheStats'],'readwrite');let held=true;window.__release=()=>held=false;const pump=()=>{const request=tx.objectStore('cacheStats').get('chat');request.onsuccess=()=>{if(held)pump();};};pump();tx.oncomplete=()=>db.close();});await control({end:true});await page.waitForFunction(()=>!document.querySelector('.connection-status')?.classList.contains('is-connected'));await page.waitForTimeout(20500);assert(state.subscriptions>=2&&state.subscriptions<=3,'Identity revalidation adds bounded re-subscription; a received frame must not retry while its commit waits');assert(await page.locator('.attachment-plus').isEnabled(),'Local selection remains available while upload/send readiness is blocked');await page.evaluate(()=>window.__release());await ready();
   await page.evaluate(()=>{const original=IDBDatabase.prototype.transaction;IDBDatabase.prototype.transaction=function(...args){const tx=original.apply(this,args);if(window.__abort&&this.name==='pot-chat-cache'&&args[1]==='readwrite')queueMicrotask(()=>tx.abort());return tx;};window.__abort=true;});await control({end:true});await page.waitForFunction(()=>document.querySelector('.connection-status').textContent.includes('storage'));assert(await page.locator('.attachment-plus').isEnabled(),'Local selection remains available while upload/send readiness is blocked');await page.evaluate(()=>window.__abort=false);await Promise.race([ready(),page.locator('.connection-status').getByRole('button',{name:'Retry',exact:true}).click()]);await ready();assert(!/storage|cache/i.test(await page.locator('.connection-status').innerText()));
  }),
 ]);}finally{for(const state of states.values())for(const stream of state.streams)stream.res.end();await transport.close();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}
 if(process.env.POT_STREAM_RESULTS_FILE)await writeFile(process.env.POT_STREAM_RESULTS_FILE,JSON.stringify(results,null,2));
 if(cases)assert.equal(results.length,cases.length,'Every selected browser scenario must run');
 assert(results.every(r=>r.ok),JSON.stringify(results.filter(r=>!r.ok)));return results;
}
