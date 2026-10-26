import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {Readable} from 'node:stream';
import {writeFile} from 'node:fs/promises';

// Real EventSource/HTTP, production UI/client/cache and a controlling SW. Only
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
   if(body.fault)state.fault=body.fault;if('authDenied'in body)state.authDenied=body.authDenied;
   if(body.changesFault)state.changesFault=body.changesFault;if('statusMode'in body)state.statusMode=body.statusMode;
   if(body.rename){state.alphaName=body.rename;state.directoryVersion=(state.directoryVersion||1)+1;}
   if(body.publishChanges)for(const stream of state.streams)if(stream.path.endsWith('/changes'))stream.res.write(`event: changed\ndata: ${JSON.stringify({assistants:state.directoryVersion||1})}\n\n`);
   if(body.silence)for(const stream of state.streams)if(stream.path.endsWith('/events'))stream.silent=true;
   if(body.end)for(const stream of [...state.streams])if(stream.path.endsWith('/events'))stream.res.end();
   if(body.endChanges)for(const stream of [...state.streams])if(stream.path.endsWith('/changes'))stream.res.end();
   return json(res,{});
  }
  if(path==='/api/v1/events'||path==='/api/v1/changes'){
   const event=path.endsWith('/events'),mode=event?state.fault:state.changesFault;if(event){state.fault=null;state.subscriptions++;}else{state.changesFault=null;state.changeSubscriptions++;}
   const stream={res,path,owner:url.searchParams.get('assistant_id')};state.streams.add(stream);req.on('close',()=>state.streams.delete(stream));
   if(mode==='auth'||mode==='closed')return json(res,{},401);if(mode==='headers')return;
   res.writeHead(200,{'content-type':'text/event-stream','cache-control':'no-cache'});res.write(': opened\n\n');
   if(event&&mode!=='reset')send(stream);
   if(!event)res.write('event: changed\ndata: {"assistants":1}\n\n');
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
  if(path.startsWith('/api/'))return json(res,path==='/api/v1/runtime'?{runtime_id:'stream-fixture',boot_id:'stream-boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',kind:'local',name:'Synthetic',connected:true}]}:path==='/api/v1/assistants/bootstrap'?{assistant:null}:path==='/api/v1/snapshot'?frame():path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:{items:[],total:0,cursor:0,has_more:false});
  const response=await fetch(sourceOrigin+req.url);res.writeHead(response.status,Object.fromEntries(response.headers));Readable.fromWeb(response.body).pipe(res);
 }catch(error){json(res,{message:error.message},500);}});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const origin='http://127.0.0.1:'+server.address().port;
 const scenario=async(name,run)=>{
  if(cases&&!cases.includes(name))return;
  const state={streams:new Set(),uploads:new Map(),subscriptions:0,changeSubscriptions:0,statusChecks:0,statusAborted:0};states.set(name,state);
  const context=await browser.newContext({viewport:{width:390,height:852}}),page=await context.newPage(),errors=[];
  if(name==='paused-clock-owner-and-list-reentry')await page.clock.install();
  await context.addCookies([{name:'stream-case',value:name,url:origin}]);page.on('pageerror',e=>errors.push(e.message));
  await context.addInitScript(()=>{
   window.__streams=[];window.__statusAborts=0;window.__probeChecks=0;const Native=EventSource;window.EventSource=class extends Native{constructor(url,options){super(url,options);window.__streams.push(this);}close(){super.close();}};
   const nativeFetch=fetch;window.fetch=(url,options)=>{if(String(url).endsWith('/access/status')&&options?.signal){window.__probeChecks++;options.signal.addEventListener('abort',()=>window.__statusAborts++,{once:true});}return nativeFetch(url,options);};
  });
  const control=body=>context.request.post(origin+'/fixture/control',{data:body});
  const ready=()=>page.waitForFunction(()=>document.querySelector('.attachment-plus')&&!document.querySelector('.attachment-plus').disabled,{},{timeout:55000});
  const sample=()=>page.evaluate(()=>({states:window.__streams.map(s=>({path:new URL(s.url).pathname,state:s.readyState})),draft:document.querySelector('#pot-composer').value,status:document.querySelector('.connection-status').textContent,addTitle:document.querySelector('.attachment-plus').title,sw:!!navigator.serviceWorker.controller}));
  const started=Date.now();
  try{
   await page.goto(origin+'/?locale=en&assistant_id=alpha&conversation=main');await ready();await page.evaluate(()=>navigator.serviceWorker.ready);await page.waitForFunction(()=>!!navigator.serviceWorker.controller);
   state.statusChecks=state.statusAborted=0;
   await page.locator('#pot-composer').fill('retained synthetic draft');await run({page,context,state,control,ready,sample});assert.deepEqual(errors,[]);
   results.push({name,ok:true,ms:Date.now()-started,subscriptions:state.subscriptions,changeSubscriptions:state.changeSubscriptions,statusChecks:state.statusChecks,statusAborted:state.statusAborted,sample:await sample()});
  }catch(error){results.push({name,ok:false,error:error.stack,errors,sample:await sample().catch(()=>null)});}
  finally{await context.close();for(const s of state.streams)s.res.end();}
 };
 try{await Promise.all([
  scenario('terminal-probe-offline-online-race',async({page,state,control,ready})=>{
   await control({fault:'closed',statusMode:'timeout',end:true});
   await page.waitForFunction(()=>window.__streams.some(s=>new URL(s.url).pathname==='/api/v1/events'&&s.readyState===2)&&document.querySelector('.attachment-plus').disabled);
   await page.waitForFunction(async()=>(await(await fetch('/fixture/state')).json()).statusChecks===1);
   assert.equal(state.statusChecks,1,'The real pending status request must have started');
   await control({statusMode:null});
   await page.evaluate(()=>{let online=false;Object.defineProperty(navigator,'onLine',{get:()=>online,configurable:true});window.dispatchEvent(new Event('offline'));online=true;window.dispatchEvent(new Event('online'));});
   await ready();assert.equal(await page.evaluate(()=>window.__statusAborts),1,'Offline cancels the actual fetch AbortSignal');assert.equal(state.subscriptions,3);assert.equal(await page.evaluate(()=>window.__streams.filter(s=>new URL(s.url).pathname==='/api/v1/events'&&s.readyState!==2).length),1);
  }),
  scenario('changes-terminal-probe-timeout-recovers',async({page,state,control})=>{
   await control({changesFault:'closed',statusMode:'timeout',endChanges:true});
   await page.waitForFunction(()=>{const streams=window.__streams.filter(s=>new URL(s.url).pathname==='/api/v1/changes');return streams.length===3&&streams.at(-1).readyState===1;},{},{timeout:9000});
   assert.equal(state.statusChecks,1);assert.equal(state.changeSubscriptions,3);assert.equal(state.statusAborted,1);assert.equal(state.subscriptions,1,'Changes recovery does not rebuild healthy Chat');
   assert.equal(await page.evaluate(()=>window.__streams.filter(s=>new URL(s.url).pathname==='/api/v1/changes'&&s.readyState!==2).length),1);
   await page.evaluate(()=>{window.dispatchEvent(new Event('focus'));window.dispatchEvent(new Event('online'));window.dispatchEvent(new Event('pageshow'));});await page.waitForTimeout(100);assert.equal(state.changeSubscriptions,3);
   const reads=state.directoryReads;await control({rename:'Recovered alpha',publishChanges:true});
   await page.waitForFunction(()=>document.querySelector('.assistant-profile-trigger')?.textContent.includes('Recovered alpha'));
   assert(state.directoryReads>reads,'The recovered changes stream must invalidate, persist and render the directory');assert.equal(state.subscriptions,1);
  }),
  scenario('changes-unknown-and-network-status-recovers',async({page,state,control})=>{
   for(const statusMode of ['unknown','network']){
    const count=state.changeSubscriptions;await control({changesFault:'closed',statusMode,endChanges:true});
    await page.waitForFunction(expected=>{const streams=window.__streams.filter(s=>new URL(s.url).pathname==='/api/v1/changes');return streams.length===expected&&streams.at(-1).readyState===1;},count+2,{timeout:4000});
    assert.equal(state.changeSubscriptions,count+2);
   }
   assert.equal(state.subscriptions,1);assert.equal(await page.evaluate(()=>window.__probeChecks),2,'One application probe per CLOSED source; Chromium may retry a socket-level GET');assert(state.statusChecks>=2);
  }),
  ...['denied','forbidden'].map(statusMode=>scenario('changes-confirmed-'+statusMode+'-pauses-across-lifecycle',async({page,state,control})=>{
   await control({changesFault:'closed',statusMode,endChanges:true});
   await page.waitForFunction(()=>window.__streams.filter(s=>new URL(s.url).pathname==='/api/v1/changes').length===2);await page.waitForTimeout(150);
   await page.evaluate(()=>{window.dispatchEvent(new Event('focus'));window.dispatchEvent(new Event('pageshow'));window.dispatchEvent(new Event('offline'));window.dispatchEvent(new Event('online'));});
   await page.waitForTimeout(900);assert.equal(state.changeSubscriptions,2);assert.equal(state.statusChecks,1);assert.equal(state.subscriptions,2,'Only the healthy Chat stream reconnects after offline');
  })),
  scenario('first-reset-deadline-despite-heartbeats',async({page,state,control,ready})=>{
   await page.getByRole('button',{name:'Add attachment',exact:true}).click();const chooser=page.waitForEvent('filechooser');await page.getByRole('menuitem',{name:'Images',exact:true}).click();await(await chooser).setFiles({name:'synthetic.png',mimeType:'image/png',buffer:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP438DwHwAGgAJ/EEwb4QAAAABJRU5ErkJggg==','base64')});await page.waitForFunction(()=>document.querySelector('.attachment-card')&&!document.querySelector('.attachment-card progress'));
   await control({fault:'reset',end:true});await page.waitForFunction(()=>document.querySelector('.attachment-plus').disabled);await ready();assert.equal(state.subscriptions,3);assert.equal(await page.locator('.attachment-card').count(),1);assert.equal(await page.locator('#pot-composer').inputValue(),'retained synthetic draft');
  }),
  scenario('headers-deadline',async({page,state,control,ready})=>{await control({fault:'headers',end:true});await page.waitForFunction(()=>document.querySelector('.attachment-plus').disabled);await ready();assert.equal(state.subscriptions,3);}),
  scenario('idle-heartbeat-expiry',async({state,control,ready,page})=>{await control({silence:true});await page.waitForFunction(()=>window.__streams.filter(s=>new URL(s.url).pathname==='/api/v1/events').length===2,{},{timeout:50000});await ready();assert.equal(state.subscriptions,2);}),
  scenario('normal-quiet-and-foreground-burst',async({page,state,ready})=>{await page.evaluate(()=>{window.dispatchEvent(new Event('focus'));window.dispatchEvent(new Event('pageshow'));window.dispatchEvent(new Event('online'));});await page.waitForTimeout(47000);await ready();assert.equal(state.subscriptions,1);}),
  scenario('paused-clock-owner-and-list-reentry',async({page,state,control,ready})=>{
   await page.evaluate(()=>{window.__visibility='hidden';Object.defineProperty(document,'visibilityState',{get:()=>window.__visibility});document.dispatchEvent(new Event('visibilitychange'));});await page.clock.setSystemTime((await page.evaluate(()=>Date.now()))+60000);await page.evaluate(()=>{window.__visibility='visible';document.dispatchEvent(new Event('visibilitychange'));window.dispatchEvent(new Event('focus'));window.dispatchEvent(new Event('pageshow'));});await page.waitForFunction(()=>window.__streams.filter(s=>new URL(s.url).pathname==='/api/v1/events').length===2);await ready();assert.equal(state.subscriptions,2);
   await control({fault:'reset',end:true});await page.waitForFunction(()=>document.querySelector('.attachment-plus').disabled);await page.locator('.titlebar .narrow-navigation').click();await page.getByRole('button',{name:'Synthetic beta',exact:true}).click();await ready();await page.locator('.titlebar .narrow-navigation').click();await page.getByRole('button',{name:'Synthetic alpha',exact:true}).click();await ready();const count=state.subscriptions;await page.waitForTimeout(21000);assert.equal(state.subscriptions,count);assert.equal(await page.locator('#pot-composer').inputValue(),'retained synthetic draft');
  }),
  scenario('authentication-terminal-and-live-locale',async({page,state,control})=>{await control({fault:'auth',authDenied:true,end:true});await page.waitForFunction(()=>document.querySelector('.connection-status').textContent.includes('session has expired'));const count=state.subscriptions;await page.waitForTimeout(700);assert.equal(state.subscriptions,count);if(process.env.POT_STREAM_CAPTURE_DIR)await page.screenshot({path:process.env.POT_STREAM_CAPTURE_DIR+'/auth-after.png'});await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}})));await page.waitForFunction(()=>document.querySelector('.connection-status').textContent.includes('浏览器会话已过期'));}),
  scenario('commit-held-and-abort-retain-readiness-gate',async({page,state,control,ready})=>{
   await page.evaluate(async()=>{const db=await new Promise(resolve=>{const req=indexedDB.open('pot-chat-cache');req.onsuccess=()=>resolve(req.result);});const tx=db.transaction(['conversations','messages','views','cacheStats'],'readwrite');let held=true;window.__release=()=>held=false;const pump=()=>{const request=tx.objectStore('cacheStats').get('chat');request.onsuccess=()=>{if(held)pump();};};pump();tx.oncomplete=()=>db.close();});await control({end:true});await page.waitForFunction(()=>document.querySelector('.attachment-plus').disabled);await page.waitForTimeout(20500);assert.equal(state.subscriptions,2,'Received frame must not be retried merely because its commit waits');assert(await page.locator('.attachment-plus').isDisabled());await page.evaluate(()=>window.__release());await ready();
   await page.evaluate(()=>{const original=IDBDatabase.prototype.transaction;IDBDatabase.prototype.transaction=function(...args){const tx=original.apply(this,args);if(window.__abort&&this.name==='pot-chat-cache'&&args[1]==='readwrite')queueMicrotask(()=>tx.abort());return tx;};window.__abort=true;});await control({end:true});await page.waitForFunction(()=>document.querySelector('.connection-status').textContent.includes('storage'));assert(await page.locator('.attachment-plus').isDisabled());await page.evaluate(()=>window.__abort=false);await page.locator('.connection-status').getByRole('button',{name:'Retry',exact:true}).click();await ready();assert(!/storage|cache/i.test(await page.locator('.connection-status').innerText()));
  }),
 ]);}finally{for(const state of states.values())for(const stream of state.streams)stream.res.end();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}
 if(process.env.POT_STREAM_RESULTS_FILE)await writeFile(process.env.POT_STREAM_RESULTS_FILE,JSON.stringify(results,null,2));
 if(cases)assert.equal(results.length,cases.length,'Every selected browser scenario must run');
 assert(results.every(r=>r.ok),JSON.stringify(results.filter(r=>!r.ok)));return results;
}
