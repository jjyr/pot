import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {mkdir} from 'node:fs/promises';
import {extname,resolve} from 'node:path';
import {launchBrowser} from './browser-harness.mjs';
import {shellAssets} from './pwa-shell.mjs';

const shell=await shellAssets(resolve('.')),before=process.env.POT_MCP_BEFORE_SOURCE?await shellAssets(process.env.POT_MCP_BEFORE_SOURCE):null;
const calls=[],held=[],hold=new Set();let fail=false,holdPost=false,consentSuccess=false;
const initial=[{id:'outbound',url:'https://other.example/mcp',remote_node_id:'node-other',state:'active',status:'connected',directory:{name:'Research Pot',targets:[{token:'secret-target'}]},last_error:'',authorization_url:'secret-authorize',session:'secret-session'},{id:'inbound',url:'',remote_node_id:'node-receiver',state:'active',status:'connected',inbound:true,directory:{name:'Incoming collaborator'}},{id:'paused',url:'https://paused.example/mcp',remote_node_id:'node-paused',state:'disconnected',status:'disconnected'},{id:'expired',url:'https://expired.example/mcp',remote_node_id:'node-expired',state:'active',status:'offline',last_error:'authorization_expired'},{id:'revoked',url:'https://revoked.example/mcp',remote_node_id:'node-revoked',state:'revoked',status:'revoked'}];
let records=structuredClone(initial);
const json=(res,value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(value));};
const server=createServer(async(req,res)=>{try{
 const url=new URL(req.url,'http://fixture'),path=url.pathname;
 if(path==='/__fixture'){res.writeHead(200,{'Content-Type':'text/html'});res.end('<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><style>html,body{position:static!important;height:auto!important;overflow:auto!important}main{max-width:860px;margin:auto;padding:24px}h1{margin-top:0}</style><main><h1>Connections</h1><div id="connections"></div></main>');return;}
 if(path==='/access/status')return json(res,{authenticated:true});
 if(path==='/api/v1/agent-links/mcp'){
  let raw='';for await(const chunk of req)raw+=chunk;const body=raw?JSON.parse(raw):null,scope=url.searchParams.get('space_id')||'local';calls.push({method:req.method,body,scope});
  if(body){const send=()=>{if(body.action==='authorize')return json(res,{authorization_url:'/__authorized'});if(body.action==='complete')return json(res,{id:'complete'});const item=records.find(item=>item.id===body.id);if(item){item.state=body.action==='revoke'?'revoked':body.action==='disconnect'?'disconnected':'active';item.status=item.state;}json(res,item||{});};if(holdPost){held.push({scope,post:true,send});return;}send();return;}
  const snapshot=structuredClone(records),send=()=>fail?json(res,{code:'synthetic_unavailable'},503):json(res,snapshot);
  if(hold.has(scope)){held.push({scope,send});return;}send();return;
 }
 if(path==='/api/v1/agent-links')return json(res,{addresses:[{kind:'tailscale',endpoint:'tcp://research.tailnet.ts.net:8793'}],items:[{id:'legacy',peer:{node_id:'node-other',kind:'pot',name:'Research Pot'},status:'connected'}]});
 if(path==='/oauth/consent'){
  if(req.method==='POST'){let raw='';for await(const chunk of req)raw+=chunk;calls.push({method:'POST',body:JSON.parse(raw),scope:'consent'});held.push({scope:'consent',send:()=>consentSuccess?json(res,{redirect:'#synthetic-authorized'}):json(res,{code:'synthetic_failure'},503)});return;}
  return json(res,{client_name:'Synthetic client',receiver_node:'Synthetic receiver'});
 }
 if(path==='/__authorized'){res.writeHead(200).end('Synthetic authorized destination');return;}
 const isBefore=path.startsWith('/before/'),asset=(isBefore?before:shell)?.files.get(isBefore?path.slice(7):path);
 if(!asset){res.writeHead(404).end();return;}res.writeHead(200,{'Content-Type':({'.mjs':'text/javascript','.js':'text/javascript','.css':'text/css','.html':'text/html','.json':'application/json','.ttf':'font/ttf'})[extname(path)]||'application/octet-stream'});res.end(asset);
}catch(error){json(res,{code:'fixture_failure',message:error.message},500);}});
await new Promise(done=>server.listen(0,'127.0.0.1',done));const origin='http://127.0.0.1:'+server.address().port;
const release=scope=>{for(let i=held.length-1;i>=0;i--)if(!scope||held[i].scope===scope)held.splice(i,1)[0].send();};
const until=async check=>{for(let n=0;n<200;n++){if(check())return;await new Promise(done=>setTimeout(done,10));}throw Error('Fixture condition timed out');};
const browser=await launchBrowser(),context=await browser.newContext(),page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
const mount=async({boot='boot-1',locale='en',baseline=false}={})=>page.evaluate(async({boot,locale,baseline})=>{
 window.component?.dispose();window.resources?.clear();document.querySelector('#connections').replaceChildren();window.language=locale;window.selectedScope={space:'local',owner:''};
 const request=async(path,options={})=>{const url=new URL(path,location.origin);url.searchParams.set('space_id',options.space||'local');const response=await fetch(url,{signal:options.signal,...(options.body?{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(options.body)}:{})});if(!response.ok)throw Error('synthetic_failure');return response.json();};
 const {createAgentLinks}=await import((baseline?'/before':'')+'/components/agent-links.mjs');
 if(!baseline){const {createResourceReads}=await import('/state/resource-cache.mjs');window.resources=createResourceReads({production:true,chatCacheName:'mcp-browser',runtime:{runtime_id:'test-control',boot_id:boot},connectionGeneration:1,scope:()=>window.selectedScope,spaces:[{id:'local',control_id:'test-control'},{id:'other',control_id:'other-control'}],transport:request});}
 window.component=createAgentLinks({root:document.querySelector('#connections'),request,resources:baseline?null:window.resources,locale:()=>window.language,scope:()=>window.selectedScope,showConnections:true,onConnected:()=>{window.connectedCallbacks=(window.connectedCallbacks||0)+1;}});
 window.component.load();
},{boot,locale,baseline});
try{
 await page.goto(origin+'/__fixture');hold.add('local');await mount();await until(()=>held.length>0);
 assert(!await page.getByText('No Pot MCP connections.',{exact:false}).isVisible(),'Cold load has no false empty prose');
 hold.clear();release();await page.getByText('Research Pot',{exact:true}).first().waitFor();await page.waitForFunction(()=>document.querySelectorAll('.agent-links')[0].querySelectorAll('.agent-link-row').length===5);
 await page.evaluate(()=>{window.savedInput=document.querySelector('input[type=url]');window.savedRow=document.querySelector('.agent-link-row');window.savedButton=window.savedRow.querySelector('button');savedInput.value='https://draft.example/mcp';savedButton.focus();});
 await page.evaluate(()=>component.load());assert(await page.evaluate(()=>savedRow===document.querySelector('.agent-link-row')&&savedButton===document.activeElement&&savedInput.value==='https://draft.example/mcp'),'Background refresh retains row, focused action and URL draft');
 await page.evaluate(()=>{window.language='zh-CN';window.dispatchEvent(new Event('pot:locale'));});assert(await page.evaluate(()=>document.activeElement===savedButton),'Locale refresh preserves focus');
 await mount({boot:'boot-2'});await page.waitForFunction(()=>document.querySelectorAll('.agent-links')[0].querySelectorAll('.agent-link-row').length===5);
 hold.add('local');await mount({boot:'boot-3'});await until(()=>held.length>0);await page.getByText('Last known status: Connected',{exact:true}).first().waitFor();
 assert.equal(await page.locator('.agent-links').first().locator('.agent-link-row').count(),5,'New boot publishes real IndexedDB rows while transport is held');
 const postsBefore=calls.filter(call=>call.method==='POST').length;await page.getByRole('button',{name:'Disconnect',exact:true}).first().click();assert.equal(calls.filter(call=>call.method==='POST').length,postsBefore,'Cached button waits for current authority');
 hold.clear();release();await until(()=>calls.filter(call=>call.method==='POST').length>postsBefore);await until(()=>held.length===0);
 await page.waitForFunction(()=>!document.querySelector('input[type=url]').closest('form').querySelector('button').disabled);
 records=structuredClone(initial);await page.evaluate(()=>component.load());await page.waitForFunction(()=>document.querySelector('.agent-link-row button').textContent==='Disconnect');
 records[0].state='revoked';records[0].status='revoked';await page.evaluate(()=>component.load());
 const countBefore=calls.filter(call=>call.method==='POST').length;
 await page.evaluate(async()=>{const {createPotMCPConnections}=await import('/components/pot-mcp-connections.mjs');const root=document.createElement('div');document.body.append(root);const c=createPotMCPConnections({root,locale:()=>language,scope:()=>selectedScope,resources,request:async(path,options)=>{const response=await fetch(path+'?space_id=local',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(options.body)});return response.json();}});await c.load();await c.change({id:'outbound',url:'https://other.example/mcp',remote_node_id:'node-other',state:'active',status:'connected'},'disconnect');c.dispose();root.remove();});
 assert.equal(calls.filter(call=>call.method==='POST').length,countBefore,'Obsolete retained action rejects revoked live target');
 records=structuredClone(initial);await page.evaluate(()=>component.load());
 fail=true;await page.evaluate(()=>component.load());assert(await page.getByRole('alert').first().isVisible());assert.equal(await page.locator('.agent-links').first().locator('.agent-link-row').count(),5,'Failed refresh retains rows');fail=false;
 // Authorize completion after A -> B -> A selection must not navigate.
 holdPost=true;await page.locator('input[type=url]').fill('https://fresh.example/mcp');await page.getByRole('button',{name:'Connect with browser',exact:true}).click();await until(()=>held.some(item=>item.post));
 await page.evaluate(async()=>{selectedScope={space:'other',owner:''};await component.load();selectedScope={space:'local',owner:''};await component.load();});holdPost=false;release();await new Promise(done=>setTimeout(done,50));assert(new URL(page.url()).pathname==='/__fixture','Retired authorization cannot redirect after A-B-A');
 const privateData=await page.evaluate(async()=>{const {createIndexedStore,idbRequest}=await import('/state/indexed-store.mjs');const db=createIndexedStore({name:'mcp-browser'});const rows=await db.transaction(['projections'],'readonly',tx=>idbRequest(tx.objectStore('projections').getAll()));db.close();return JSON.stringify(rows);});assert(!/secret-|authorization_url|session/.test(privateData),'Persisted connection metadata excludes authorization/session/targets');
 // Actual IndexedDB failure is distinct from a failed remote connection. The
 // committed-display contract must not silently fall back to response memory.
 const beforeStorageFailure=calls.filter(call=>call.method==='GET').length;
 await page.evaluate(()=>{window.originalTransaction=IDBDatabase.prototype.transaction;IDBDatabase.prototype.transaction=function(stores,mode,...args){if(mode==='readonly'&&[...stores].includes('projections'))throw new DOMException('Synthetic storage unavailable','UnknownError');return originalTransaction.call(this,stores,mode,...args);};});
 await mount({boot:'boot-storage'});await page.waitForFunction(()=>document.querySelector('.agent-links [role=alert]')?.textContent.includes('Local data storage'));
 assert.equal(calls.filter(call=>call.method==='GET').length,beforeStorageFailure,'Storage failure does not silently bypass committed display storage');
 await page.evaluate(()=>{IDBDatabase.prototype.transaction=window.originalTransaction;});await page.evaluate(()=>component.load());await page.getByText('Research Pot',{exact:true}).first().waitFor();
 if(process.env.POT_MCP_IMAGES){await mkdir(process.env.POT_MCP_IMAGES,{recursive:true});for(const baseline of before?[true,false]:[false])for(const width of [1280,390]){await page.setViewportSize({width,height:900});await mount({locale:'zh-CN',baseline});await page.getByText('Research Pot',{exact:true}).first().waitFor();await page.evaluate(async()=>{await document.fonts.ready;await new Promise(done=>requestAnimationFrame(()=>requestAnimationFrame(done)));});await page.screenshot({path:resolve(process.env.POT_MCP_IMAGES,`connections-${width}-${baseline?'before':'after'}.png`),fullPage:false});}}
 await page.goto(origin+'/mcp-pot-callback.html?state=synthetic-state&code=synthetic-code&space_id=other');await page.getByRole('link').waitFor();assert.equal(new URL(page.url()).search,'');assert(calls.some(call=>call.body?.action==='complete'&&call.scope==='other'&&call.body.state==='synthetic-state'),'Callback consumes query and preserves captured Space');
 await page.goto(origin+'/mcp-connect.html?request=synthetic-consent');await page.getByRole('button',{name:'Connect',exact:true}).click();await until(()=>held.some(item=>item.scope==='consent'));assert(await page.getByRole('button',{name:'Deny',exact:true}).isDisabled());release('consent');await page.getByRole('alert').waitFor();await page.getByRole('button',{name:'Connect',exact:true}).click();await until(()=>held.some(item=>item.scope==='consent'));release('consent');await page.waitForFunction(()=>!document.querySelector('button').disabled);assert.equal(await page.getByRole('alert').count(),1,'Repeated failures retain one notice');
 // A native/browser handoff may leave this document visible. A same-document
 // redirect exercises that success path without launching any external app.
 consentSuccess=true;const decisions=calls.filter(call=>call.scope==='consent').length;
 await page.evaluate(()=>document.querySelector('button').click());await until(()=>held.some(item=>item.scope==='consent'));release('consent');await page.waitForFunction(()=>location.hash==='#synthetic-authorized');
 assert(await page.getByRole('button',{name:'Connect',exact:true}).isDisabled(),'Successful consent stays pending when handoff leaves the document visible');
 await page.evaluate(()=>{for(const button of document.querySelectorAll('button'))button.dispatchEvent(new MouseEvent('click',{bubbles:true}));});assert.equal(calls.filter(call=>call.scope==='consent').length,decisions+1,'Successful consent cannot be submitted twice');
 assert.deepEqual(errors,[]);console.log('MCP local-first, stable DOM, current authority, retired scope, callback and consent browser checks passed');
}finally{hold.clear();holdPost=false;release();await context.close();await browser.close();server.closeAllConnections();await new Promise(done=>server.close(done));}
