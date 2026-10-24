// Serve the repository root over loopback and open scripts/tests/fixtures/cached-loading.html.
// The fixture uses production DOM components and controlled, credential-free IO.
const baseline=new URLSearchParams(location.search).has('baseline');
const prefix=baseline?'/output/cached-loading/baseline/web/src':'/web/src';
const {createPages}=await import(`${prefix}/components/pages.mjs`);
const {createResourceReads}=await import('/web/src/state/resource-cache.mjs');
const host=document.querySelector('#page'),results=document.querySelector('#results'),counts=new Map();
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const check=(value,message)=>{if(!value)throw Error(message);results.textContent+='PASS '+message+'\n';};
const wait=async(test)=>{for(let i=0;i<150;i++){if(test())return;await pause(20);}throw Error('Timed out: '+test);};
let locale='en',failProviders=false,delays={},context={space:'a',owner:'alpha'};
const profiles={profiles:[{id:'p',name:'Fixture Provider',provider:'fixture',model:'fixture-model',model_settings:{}}],connections:[],directory:[],default_provider_id:'p',configured_models:{}};
const taskItems=Array.from({length:51},(_,i)=>({id:'t'+i,assistant_id:'alpha',title:'Fixture Task '+i,phase:'running',created_at_ms:1000-i,started_at_ms:Date.now()-10000}));
const client={production:true,desktop:false,runtime:{runtime_id:'fixture-control',boot_id:'boot'},spaces:[{id:'a',control_id:'ca',name:'Space A',connected:true},{id:'b',control_id:'cb',name:'Space B',connected:true}],assistants:[{id:'alpha',space_id:'a',control_id:'ca',name:'Alpha'}],scope:()=>({...context}),setScope:value=>Object.assign(context,value),
  async request(path,options={}){const url=new URL(path,location.origin),base=url.pathname,key=options.space+':'+path;counts.set(key,(counts.get(key)||0)+1);await pause(delays[base]??150);
    if(base==='/api/v1/providers'){if(failProviders)throw Error('Fixture offline');return structuredClone({...profiles,profiles:profiles.profiles.map(p=>({...p,name:options.space==='b'?'Space B Provider':p.name}))});}
    if(base==='/api/v1/agent/routing')return {profile_limits:{},provider_limits:{}};
    if(base==='/api/v1/usage/providers')return {providers:[{provider:'fixture',active:false,policy:{period:'none'}}]};
    if((base==='/api/v1/tasks/query'||base==='/api/v1/tasks/catalog')){if(options.space==='b')return {items:[],total:0,counts:{}};const offset=Number(url.searchParams.get('offset')||0);return {items:taskItems.slice(offset,offset+50),next_offset:offset===0?50:null,counts:{active:51,attention:0,archived:0},total:51};}
    if(base==='/api/v1/tasks/t0')return {info:taskItems[0],run_id:'run',request:{text:'Fixture task input'},capabilities:{version:1,operations:['task.read','task.input','task.events']}};
    if(base==='/api/v1/tasks/t0/messages')return {items:[]};
    if(base==='/api/v1/tasks/t0/events')return {events:Number(url.searchParams.get('after'))===0?[{sequence:1,event:{update:{type:'message_started'}}},{sequence:2,event:{update:{type:'text_delta',text:'Cached task transcript'}}},{sequence:3,event:{update:{type:'message_finished'}}}]:[],has_more:false};
    if(base==='/api/v1/schedules')return {items:[],next_offset:null};
    if(base==='/api/v1/permissions')return {pending:[],machines:[],policy:{}};
    if(base==='/api/v1/integrations'){const loggedIn=counts.get(key)>1;return {items:[{id:'github',name:'GitHub',login_available:true,status:loggedIn?'connected':'disconnected',...(loggedIn?{account:{revision:1,identity:{username:'fixture-login'}}}:{login:{id:'fixture-login',status:'pending'}})}]};}
    if(base==='/api/v1/machines')return [{id:'m',name:'Fixture Machine',status:'online'}];
    if(base==='/api/v1/usage/requests'){const offset=Number(url.searchParams.get('offset')||0);return {items:Array.from({length:20},(_,i)=>({label:'Request '+(60-offset-i),model:'fixture-model',input:10,output:2,state:'complete'})),next_offset:offset<40?offset+20:null};}
    if(base==='/api/v1/usage/summary')return {total:{input:100,output:20,cache:5},lifetime:{input:100,output:20,cache:5},quota:{policy:{period:'none'},active:false},buckets:[],providers:[]};
    if(base.startsWith('/api/v1/usage/'))return {items:[{agent:'pot',model:'fixture-model',input:100,output:20,cache:5}],next_offset:null};
    if(base==='/api/v1/about')return {version:'fixture',licenses:[]};
    return {};},downloadFile:()=>{}};
if(!baseline){client.resources=createResourceReads(client);client.read=(...args)=>client.resources.read(...args);}
const pages=createPages({client,host,approvalHost:document.querySelector('#approvals'),chatApprovalHost:document.querySelector('#chat-approvals'),chatVisible:()=>false,locale:()=>locale,notify:text=>{results.textContent+='NOTICE '+text+'\n';},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});
const button=text=>[...host.querySelectorAll('button')].find(node=>node.textContent===text);
const click=text=>{const node=button(text);if(!node)throw Error('Missing button '+text);node.click();};
const count=fragment=>[...counts].filter(([key])=>key.includes(fragment)).reduce((sum,[,value])=>sum+value,0);
window.addEventListener('error',event=>{results.textContent+='ERROR '+event.message+'\n';});
window.addEventListener('unhandledrejection',event=>{results.textContent+='ERROR '+event.reason+'\n';});
async function run(){results.textContent='Running '+(baseline?'baseline':'cached loading')+'\n';
  await pages.showProviders();check(host.textContent.includes('Fixture Provider'),'Providers initially render');
  delays['/api/v1/usage/providers']=800;click('Task Configuration');await pause(40);
  const independent=host.textContent.includes('Fixture Provider');await wait(()=>host.textContent.includes('Unlimited'));
  click('AI Providers');await pause(40);const immediate=host.textContent.includes('Fixture Provider');await wait(()=>host.textContent.includes('Fixture Provider'));
  results.textContent+=JSON.stringify({providersReads:count('/api/v1/providers'),returnVisibleBeforeNetwork:immediate,configurationVisibleBeforeAllowances:independent})+'\n';
  if(baseline)return;
  check(count('/api/v1/providers')===1,'AI → task configuration → AI shares one providers read');check(immediate,'Return renders provider cache before delayed IO');check(independent,'Configuration renders before slow allowances');
  click('Third-party accounts');await wait(()=>host.textContent.includes('@fixture-login'));check(count('/api/v1/integrations')===2,'Existing login progress refresh bypasses the configuration TTL');
  delays['/api/v1/permissions']=700;const machines=pages.show('machines');await pause(200);check(host.textContent.includes('Fixture Machine'),'Machine inventory renders before slow permission reads');await machines;const machineReturn=pages.show('machines',{force:true});await pause(40);check(host.textContent.includes('Fixture Machine'),'Machine return displays cached inventory during authority checks');await machineReturn;delays['/api/v1/permissions']=150;
  await pages.show('tasks');await wait(()=>host.querySelectorAll('.task-row').length===20);const taskReads=count('/api/v1/tasks/catalog');click('Next →');await wait(()=>host.textContent.includes('Page 2'));
  check(count('/api/v1/tasks/catalog')===taskReads,'Task pagination reuses the bounded cached prefix');click('← Previous');await wait(()=>host.textContent.includes('Page 1'));
  await pages.openTask({...taskItems[0],_space_id:'a'});await wait(()=>host.textContent.includes('Cached task transcript'));check(counts.get('a:/api/v1/tasks/t0')===1,'First task visit reads detail only once');
  await pages.show('tasks',{force:true});delays['/api/v1/tasks/t0']=800;delays['/api/v1/tasks/t0/events']=800;
  const returning=pages.openTask({...taskItems[0],_space_id:'a'});await pause(40);check(host.textContent.includes('Cached task transcript'),'Task return keeps transcript during refresh');await returning;
  check([...counts.keys()].some(key=>key.includes('/events?after=3')),'Task return resumes from cached event cursor');
  await pages.showProviders();failProviders=true;client.resources.invalidate(['providers'],{space:'a'});pages.refresh(['providers'],{space:'a'});await wait(()=>host.querySelector('.resource-refresh-error'));
  check(host.textContent.includes('Fixture Provider'),'Refresh failure retains provider content');locale='zh-CN';window.dispatchEvent(new Event('pot:locale'));await pause(50);check(host.textContent.includes('刷新失败'),'Refresh failure follows the current locale');
  // Successful navigation keeps shared cache; the error notice uses current locale on its next failure.
  failProviders=false;locale='en';window.dispatchEvent(new Event('pot:locale'));await pause(50);
  await pages.showProviders();click('Configure');await wait(()=>host.querySelector('form'));const name=host.querySelector('input[aria-label="Name"]')||host.querySelector('input');name.value='Unsaved fixture draft';name.dispatchEvent(new Event('input',{bubbles:true}));
  client.resources.invalidate(['providers'],{space:'a'});pages.refresh(['providers'],{space:'a'});await pause(200);check(name.isConnected&&name.value==='Unsaved fixture draft','Refresh preserves dirty editor inputs');
  const leaving=pages.leave();await wait(()=>document.querySelector('dialog[open]'));check(document.querySelector('dialog[open]').textContent.includes('Discard changes?'),'Existing leave protection remains active');
  [...document.querySelectorAll('dialog[open] button')].find(node=>node.textContent==='Keep editing').click();check(await leaving===false,'Keep editing cancels navigation');
  const discard=pages.leave();await wait(()=>document.querySelector('dialog[open]'));[...document.querySelectorAll('dialog[open] button')].find(node=>node.textContent==='Discard changes').click();await discard;
  client.desktop=true;await pages.showProviders();const selector=host.querySelector('select[aria-label="Configuration location"]');
  delays['/api/v1/providers']=600;client.resources.invalidate(['providers'],{space:'a'});pages.refresh(['providers'],{space:'a'});selector.value='b';selector.dispatchEvent(new Event('change',{bubbles:true}));
  await wait(()=>host.textContent.includes('Space B Provider'));await pause(650);check(!host.textContent.includes('Fixture Provider'),'Late Space A responses cannot replace Space B');
  client.desktop=false;delays['/api/v1/providers']=150;await pages.conversationUsage('Alpha');await wait(()=>host.querySelector('.usage-list-requests')?.textContent.includes('Request 60'));
  const requests=()=>host.querySelector('.usage-list-requests');[...requests().querySelectorAll('button')].find(node=>node.textContent==='Next page').click();await wait(()=>requests().textContent.includes('Request 40'));
  check(!requests().textContent.includes('Request 60'),'Model request pagination keeps server ordering');await pages.show('tasks');
  const usageReturn=pages.conversationUsage('Alpha');await pause(40);check(requests()?.textContent.includes('Request 40'),'Usage returns with its cached selected page');await usageReturn;
  await pages.permissions();await pages.permissions();check(count('/api/v1/permissions')>=2,'Approval refresh always reads authority');
  results.textContent+='ALL PAGE CHECKS PASSED\n';
  pages.dispose();client.resources.clear();await chatChecks();
}
document.querySelector('#run').onclick=()=>{document.querySelector('#run').disabled=true;return run().catch(error=>{results.textContent+='FAIL '+error.stack+'\n';});};
document.querySelector('#providers').onclick=()=>pages.showProviders();
document.querySelector('#tasks').onclick=()=>pages.show('tasks');
window.addEventListener('pagehide',()=>{pages.dispose();client.resources?.clear();});

async function chatChecks(){
  const {mountApp}=await import('/web/src/components/ui.js');
  let subscriptions=0,snapshotReads=0,change,deliver,chatBoot='boot';const directoryReads={catalog:0,a:0,b:0};
  context={space:'a',owner:'alpha'};delays={};failProviders=false;
  const raw=client.request;
  client.request=async(path,options={})=>{if(path==='/api/v1/spaces'){directoryReads.catalog++;return {items:client.spaces,active_space_id:'a'};}if(path==='/api/v1/assistants'){directoryReads[options.space]++;return {items:options.space==='a'?[{id:'alpha',name:'Alpha'},{id:'beta',name:'Beta'}]:[{id:'alpha',name:'Remote Alpha'}]};}return raw(path,options);};
  const snapshot=(scope,cursor=1,text=scope.owner+' confirmed')=>({runtime_id:'fixture-control',boot_id:chatBoot,owner_id:JSON.stringify([scope.space,scope.owner]),conversation_id:scope.owner,cursor:String(cursor),replies:[],running:false,turn_id:null,messages:[{id:'reply',role:'assistant',text:scope.space+' '+text,status:'complete'}]});
  client.snapshot=async()=>{snapshotReads++;return snapshot(client.scope());};
  client.subscribe=(receive,status)=>{subscriptions++;const scope=client.scope(),timer=setTimeout(()=>receive(snapshot(scope)),250);deliver=(cursor,text)=>receive(snapshot(scope,cursor,text));status('connected');return ()=>clearTimeout(timer);};
  client.changes=callback=>{change=callback;return ()=>{};};
  host.replaceChildren();host.style.height='700px';const dispose=mountApp(host,client);
  await wait(()=>host.querySelector('.message-column')?.textContent.includes('a alpha confirmed'));
  const select=name=>{const row=[...host.querySelectorAll('.assistant-row')].find(node=>node.querySelector('.assistant-title').textContent===name);if(!row)throw Error('Missing assistant '+name);row.click();};
  select('Beta');await wait(()=>host.querySelector('.message-column').textContent.includes('a beta confirmed'));select('Alpha');await pause(40);
  check(host.querySelector('.message-column').textContent.includes('a alpha confirmed'),'Chat restores confirmed cached messages before subscription');
  check(host.querySelector('.connection-status').textContent.includes('Connecting'),'Cached chat waits for an authoritative snapshot');
  await pause(300);deliver(3,'newer stream');await pause(30);deliver(2,'old stream');await pause(30);check(host.querySelector('.message-column').textContent.includes('newer stream'),'Older snapshots cannot roll back streaming content');
  select('Remote Alpha');await wait(()=>host.querySelector('.message-column').textContent.includes('b alpha confirmed'));select('Alpha');await pause(40);check(host.querySelector('.message-column').textContent.includes('a newer stream'),'Identical assistant IDs stay isolated by Space');await pause(300);
  check(snapshotReads===0&&subscriptions===5,'Initial subscription eliminates duplicate snapshot GETs');
  const before={...directoryReads};change(['assistants'],{space:'a',boot:'boot'});await pause(200);
  check(directoryReads.catalog===before.catalog&&directoryReads.b===before.b&&directoryReads.a===before.a+1,'Directory invalidation reads only the affected Space');
  chatBoot='boot2';deliver(1,'after restart');await pause(300);check(host.querySelector('.message-column').textContent.includes('a alpha confirmed')&&!host.textContent.includes('identity changed'),'Control restart establishes a new authoritative subscription');
  check(snapshotReads===0,'Boot recovery also uses the subscription initial snapshot');dispose();
  results.textContent+='ALL CHECKS PASSED\n';
}
