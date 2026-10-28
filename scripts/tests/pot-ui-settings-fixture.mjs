// Isolated production HTTP/SSE + UI shell. All identities, models and keys are synthetic.
import {createServer} from 'node:http';
import {resolve,extname} from 'node:path';
import {readFile} from 'node:fs/promises';
import {shellAssets} from './pwa-shell.mjs';
const root=resolve(process.env.POT_SETTINGS_SOURCE||resolve(import.meta.dirname,'../..'));
const outbox=process.env.POT_SETTINGS_OUTBOX==='1',accepted=new Map();
const shell=await shellAssets(root),streams=new Map(),held=[],calls=[];
for(const path of ['/scripts/tests/fixtures/assistant-settings.html','/scripts/tests/pot-ui-assistant-settings-browser.mjs'])shell.files.set(path,await readFile(resolve(import.meta.dirname,'../..'+path)));
let evidence=[];
const state={control:'settings-control',boot:'settings-boot',phase:'running',snapshot:true,hold:[],revoked:false,failSave:false,failRead:false,revision:1,configRevision:0,rulesRevision:'rules-1',name:'Synthetic Assistant',model:'synthetic-original',rules:'Synthetic fixture rules',pending:false,cursor:1};
const caps=()=>({version:1,operations:['chat.read','chat.events','chat.history',...(outbox?['chat.send','chat.steer','chat.subscribe','chat.attachments']:[]),...(state.revoked?[]:['assistant.configure','assistant.profile'])]});
const frame=()=>({kind:'reset',base_cursor:0,view:{state:{api_version:3,cursor:state.cursor,boot_id:state.boot,conversation_id:'synthetic-conversation',turn:state.phase==='running'?{user_message_id:'synthetic-active-turn',paused:false}:null,agent:{phase:state.phase,model:'synthetic-original'}},replies:[],messages:Array.from({length:outbox?state.historyCount||1:1},(_,index)=>({sequence:String(index+1),version:state.cursor,message:{id:index?'synthetic-message-'+index:'synthetic-message',role:'assistant',status:'complete',text:'Synthetic assistant execution continues independently of settings.'+(index?'\n\nSynthetic reading paragraph '+index+'. '.repeat(12):'')}})),history:{before:'1',after:String(outbox?state.historyCount||1:1),has_more:false,has_newer:false}}});
const json=(res,value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(value));};
const broadcast=(event,value,kind)=>{for(const [res,path]of streams)if(path.endsWith(kind))res.write('event: '+event+'\ndata: '+JSON.stringify(value)+'\n\n');};
const server=createServer(async(req,res)=>{try{
 const url=new URL(req.url,'http://fixture'),path=url.pathname;
 if(path==='/__evidence'){if(req.method==='POST'){let raw='';for await(const chunk of req)raw+=chunk;evidence.push(JSON.parse(raw));}return json(res,evidence);}
 if(path==='/__fixture'){
  if(req.method==='POST'){
   let raw='';for await(const chunk of req)raw+=chunk;const update=JSON.parse(raw);
   const {release,change,disconnect,...values}=update;Object.assign(state,values);
   if(release)for(let index=held.length-1;index>=0;index--)if(release==='all'||held[index].group===release)held.splice(index,1)[0].send();
   if(change){state.cursor++;broadcast('changed',{spaces:state.cursor,assistants:state.cursor,conversation:state.cursor},'/changes');if(state.snapshot)broadcast('conversation',frame(),'/events');}
   if(disconnect)for(const [stream,streamPath]of streams)if(disconnect==='all'||streamPath.endsWith('/events'))stream.end();
  }
  return json(res,{state,calls,held:held.map(item=>item.group),streams:[...streams.values()]});
 }
 if(path==='/__monitor.js'){res.writeHead(200,{'Content-Type':'text/javascript'});res.end(`const ids=new WeakMap();let count=0,last='';setInterval(()=>{const nodeID=node=>{if(!ids.has(node))ids.set(node,++count);return ids.get(node);};const value={route:document.querySelector('[data-route]')?.dataset.route,fields:[...document.querySelectorAll('.editor-form input,.editor-form select,.editor-form textarea')].map(node=>({id:nodeID(node),label:node.getAttribute('aria-label'),value:node.value,disabled:node.disabled})),inert:document.querySelector('.page-host')?.inert,buttons:[...document.querySelectorAll('button')].map(node=>({text:node.textContent,label:node.getAttribute('aria-label'),disabled:node.disabled})),text:document.body.innerText.slice(0,10000),profileResults:window.assistantSettingsResults};const encoded=JSON.stringify(value);if(encoded!==last){last=encoded;fetch('/__evidence',{method:'POST',headers:{'Content-Type':'application/json'},body:encoded}).catch(()=>{});}},100);`);return;}
 if(path==='/phone'){res.writeHead(200,{'Content-Type':'text/html'});res.end('<!doctype html><meta name="viewport" content="width=device-width"><style>body{margin:0;background:#ddd}iframe{display:block;width:390px;height:780px;border:0}</style><iframe src="/?locale=zh-CN"></iframe>');return;}
 if(path==='/access/status')return json(res,{authenticated:true,scope:'control'});
 if(path.startsWith('/api/')){
  let body; if(req.method==='POST'){let raw='';for await(const chunk of req)raw+=chunk;body=JSON.parse(raw||'{}');}
  const call={path,query:Object.fromEntries(url.searchParams),method:req.method,body};calls.push(call);
  const fail=(code,status)=>{call.status=status;return json(res,{code,message:'Synthetic fixture request failure'},status);};
  const answer=value=>{call.status=200;json(res,value);};
  const group=path==='/api/v1/spaces'?'spaces':path==='/api/v1/assistants'?'directory':path==='/api/v1/agent'?'configuration':path==='/api/v1/providers'?'providers':path==='/api/v1/instructions'?'rules':path==='/api/v1/snapshot'?'snapshot':req.method==='POST'?'save':'';
  const respond=()=>{
   if(url.searchParams.has('expected_control_id')&&url.searchParams.get('expected_control_id')!==state.control)return fail('control_changed',409);
   if(url.searchParams.has('configuration_control_id')||url.searchParams.has('configuration_boot_id'))if(url.searchParams.get('configuration_control_id')!==state.control||url.searchParams.get('configuration_boot_id')!==state.boot)return fail('identity_changed',409);
   if(req.method==='POST'){
    if(state.failSave)return fail('synthetic_network_failure',503);
    if(state.revoked)return fail('agent_link_capability_unsupported',403);
    if(url.searchParams.get('space_id')!=='local'||url.searchParams.get('assistant_id')!=='alpha')return fail('synthetic_wrong_target',409);
    if(outbox&&['/api/v1/messages','/api/v1/turn/steer'].includes(path)){
     const prior=accepted.get(body.request_id);if(prior&&JSON.stringify(prior.body)!==JSON.stringify(body))return fail('request_conflict',409);
     accepted.set(body.request_id,{path,body});return answer({message_id:body.request_id,duplicate:!!prior});
    }
    if(path==='/api/v1/assistants/alpha'){
     if(body.revision!==state.revision)return fail('assistant_changed',409);
     state.name=body.name;state.revision++;return answer({id:'alpha',name:state.name,revision:state.revision});
    }
    if(path==='/api/v1/agent'){
     if(body.expected_revision!==state.configRevision)return fail('assistant_changed',409);
     state.model=body.selection.model;state.configRevision++;state.pending=state.phase==='running';return answer({generation:1,revision:state.configRevision,pending:state.pending,selection:{...body.selection}});
    }
    if(path==='/api/v1/instructions'){state.rules=body.text;state.rulesRevision+='x';return answer({text:state.rules,revision:state.rulesRevision});}
    return fail('synthetic_forbidden_operation',400);
   }
   if(state.failRead&&['configuration','providers','rules'].includes(group))return fail('synthetic_read_failure',503);
   if(path==='/api/v1/runtime')return answer({runtime_id:state.control,boot_id:state.boot,host:{hostname:'Synthetic Host'}});
   if(outbox&&path==='/api/v1/messages/receipt'){const query=Object.fromEntries(url.searchParams),prior=accepted.get(query.request_id);return answer({...query,runtime_id:state.control,owner_id:'alpha',state:prior?'accepted':'unconfirmed',...(prior?{message_id:query.request_id}:{})});}
   if(path==='/api/v1/spaces')return answer({active_space_id:'local',items:[{id:'local',control_id:state.control,name:'Synthetic Space',kind:'local',connected:true}]});
   if(path==='/api/v1/assistants')return answer([{id:'alpha',name:state.name,revision:state.revision,phase:state.phase,executing:state.phase==='running',capabilities:caps()}]);
   if(path==='/api/v1/agent-links')return answer({items:[]});
   if(path==='/api/v1/assistants/profile')return answer({id:'alpha',name:state.name,avatar:'',revision:state.revision});
   if(path==='/api/v1/agent')return answer({generation:1,revision:state.configRevision,pending:state.pending,selection:{profile_id:'synthetic-provider',profile_revision:1,model:state.model}});
   if(path==='/api/v1/instructions')return answer({text:state.rules,revision:state.rulesRevision});
   if(path==='/api/v1/providers')return answer(state.providers||{default_provider_id:'synthetic-provider',profiles:[{id:'synthetic-provider',name:'Synthetic Provider',revision:1,model:'synthetic-original',models:['synthetic-original','synthetic-next']}],directory:[],provider_types:[],connections:[],configured_models:{'synthetic-provider':['synthetic-original','synthetic-next']}});
   if(path==='/api/v1/permissions')return answer({pending:[],policy:{machines:{},rules:[],task_grants:[]}});
   if(path==='/api/v1/tasks/catalog'||path==='/api/v1/tasks/query')return answer({items:[],total:0,counts:{},next_offset:null});
   if(path==='/api/v1/assistants/bootstrap')return answer({assistant:null});
   if(path==='/api/v1/snapshot')return state.snapshot?answer(frame()):fail('synthetic_snapshot_unavailable',503);
   if(path==='/api/v1/events'||path==='/api/v1/changes'){
    res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-store'});res.flushHeaders();streams.set(res,path);res.on('close',()=>streams.delete(res));res.write(': synthetic settings stream\n\n');
    if(path.endsWith('/events')&&state.snapshot)res.write('event: conversation\ndata: '+JSON.stringify(frame())+'\n\n');return;
   }
   return answer({});
  };
  if(state.hold.includes(req.method==='POST'?'save':group)){held.push({group:req.method==='POST'?'save':group,send:respond});return;}
  respond();return;
 }
 let file=shell.files.get(path==='/'?'/index.html':path.replace(/^\/web\/src/,''));if(file&&extname(path==='/'?'/index.html':path)==='.html')file=Buffer.from(file.toString()+'<script src="/__monitor.js"></script>');if(!file){res.writeHead(404).end();return;}
 res.writeHead(200,{'Cache-Control':'no-store','Content-Type':({'.js':'text/javascript','.mjs':'text/javascript','.css':'text/css','.html':'text/html','.json':'application/json','.svg':'image/svg+xml','.ttf':'font/ttf','.png':'image/png','.webmanifest':'application/manifest+json'})[extname(path==='/'?'/index.html':path)]||'application/octet-stream'});res.end(file);
}catch(error){json(res,{code:'synthetic_fixture_error',message:error.message},500);}});
await new Promise(done=>server.listen(0,'127.0.0.1',done));console.log('http://127.0.0.1:'+server.address().port);
const heartbeat=setInterval(()=>{for(const stream of streams.keys())stream.write(': heartbeat\n\n');},5000);
const stop=()=>{clearInterval(heartbeat);for(const stream of streams.keys())stream.end();for(const item of held.splice(0))item.send();server.closeAllConnections();server.close();};process.on('SIGTERM',stop);process.on('SIGINT',stop);
