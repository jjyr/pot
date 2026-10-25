const params=new URLSearchParams(location.search),before=params.has('before');
const source=before?'/output/update-before/web/src':'/web/src';
const {mountApp}=await import(`${source}/components/ui.js`);
const {createUpdates}=await import(`${source}/components/updates.mjs`);
const scope={space:'local',owner:'main'},listeners=new Set(),posts=[],errors=[];
let update={supported:!params.has('manual'),auto_install:!params.has('manual'),reason:params.has('manual')?'migration_path':'',discovery:'verified',current:'0.1.0',version:'0.2.0',release_url:'https://github.com/jjyr/pot/releases/tag/v0.2.0',phase:'available',work:{tasks:params.has('busy')?2:0,replies:0,sessions:0}};
if(before){document.querySelector('link[rel="stylesheet"]').href=source+'/styles.css';}
window.addEventListener('error',event=>errors.push(event.message));window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason)));
const snapshot={runtime_id:'fixture',owner_id:JSON.stringify(['local','main']),boot_id:'boot',conversation_id:'conversation',kind:'reset',base_cursor:0,cursor:'2',replies:[],running:false,turn_id:null,history:{before:'1',after:'2',has_more:false,has_newer:false},messages:[{id:'user',role:'user',text:'帮我整理一下项目的下一步工作。',status:'complete',sequence:'1',version:1},{id:'reply',role:'assistant',reply_to:'user',text:'可以。我们先确认当前进度，再按优先级安排接下来的工作。',status:'complete',sequence:'2',version:2}]};
const emit=()=>{for(const listener of listeners)listener(['updates'],{space:'local'});};
const client={production:true,runtime:{runtime_id:'fixture'},scope:()=>({...scope}),setScope:value=>Object.assign(scope,value),changes:handler=>{listeners.add(handler);return()=>listeners.delete(handler);},subscribe(handler,status){queueMicrotask(()=>{status('connected');handler(structuredClone(snapshot));});return()=>{};},snapshot:async()=>structuredClone(snapshot),history:async()=>({messages:[],history:{}}),
 async request(path,options={}){
  if(path==='/api/v1/updates'){
   if(params.has('http')){const response=await fetch(path,{method:options.method||'GET',headers:{'Content-Type':'application/json'},body:options.body?JSON.stringify(options.body):undefined});if(!response.ok)throw Error(`HTTP ${response.status}`);if(options.body)posts.push(structuredClone(options));return await response.json();}
   if(options.method==='POST'){posts.push(structuredClone(options));if(options.body.action==='check'){await new Promise(resolve=>setTimeout(resolve,100));}else if(options.body.action==='download'){update.phase='downloading';setTimeout(()=>{update.phase='ready';emit();},80);}else update.phase='installing';}
   return structuredClone(update);
  }
  if(path==='/api/v1/spaces')return {active_space_id:'local',items:[{id:'local',name:'This machine',control_id:'fixture',connected:true}]};
  if(path==='/api/v1/assistants')return [{id:'main',name:'Pot',control_id:'fixture',reply_id:'reply',reply_count:1}];
  if(path==='/api/v1/permissions')return {pending:[],policy:{}};
  if(path.startsWith('/api/v1/tasks/outcomes'))return {items:[],cursor:0};
  if(path.startsWith('/api/v1/tasks'))return {items:[],counts:{active:0}};
  if(path==='/api/v1/providers')return {profiles:[]};
  if(path==='/api/v1/about')return {version:'0.1.0',revision:'fixture',go_version:'go1.26.1',licenses:[],notices_available:false};
  return {};
 }};
const dispose=mountApp(document.querySelector('#app'),client);
window.fixture={client,posts,errors,update:value=>{Object.assign(update,value);emit();},dispose};
window.runUpdateChecks=async()=>{
 const assert=(condition,message)=>{if(!condition)throw Error(message);};
 const tick=()=>new Promise(resolve=>setTimeout(resolve,25));
 // Exercise the real component with controlled IO and no product processes.
 let locale='en',value={supported:true,auto_install:true,current:'0.1.0',version:'0.2.0',phase:'ready',work:{tasks:2,replies:0,sessions:0}},calls=[],reloads=0,resolver;
 const testClient={request:async(path,options)=>{if(options.method==='POST'){calls.push(options);return {...value,phase:'installing'};}return structuredClone(value);}};
 const component=createUpdates({client:testClient,locale:()=>locale,changed:()=>{},notify:message=>{throw Error(message);},reload:()=>reloads++});document.body.append(component.dialog);await component.refresh({space:'one'});
 component.button().click();component.dialog.querySelector('button.primary').click();await tick();assert(component.dialog.open,'Busy work requires confirmation');assert(calls.length===0,'Opening confirmation cannot interrupt work');
 component.dialog.querySelector('button').click();assert(!component.dialog.open,'Cancel keeps work running');
 component.button().click();component.dialog.querySelector('button.primary').click();locale='zh-CN';window.dispatchEvent(new Event('pot:locale'));assert(component.dialog.textContent.includes('中断并升级'),'Pending confirmation follows locale changes');
 component.dialog.querySelector('button.primary').click();await tick();assert(calls.length===1&&calls[0].body.interrupt===true,'Only explicit confirmation authorizes interruption');assert(calls[0].space==='one','Installation is scoped to its Control');
 value={...value,phase:'current',current:'0.2.0'};await component.refresh();assert(reloads===1,'Successful restart reloads frontend assets');assert(component.button()===null,'Updated installation hides the action');component.close();
 const stale=createUpdates({client:{request:()=>new Promise(resolve=>resolver=resolve)},locale:()=>locale,changed:()=>{},notify:()=>{}});const late=stale.refresh({space:'old'});stale.close();resolver(value);await late;assert(stale.button()===null,'Retired read cannot revive the action');
 assert(errors.length===0,errors.join('\n'));return 'PASS: confirmation, cancellation, interruption, locale switch, Control scope, restart reload, stale reads';
};
