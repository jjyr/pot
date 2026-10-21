import {createPages} from '../../../web/src/components/pages.mjs';
import {projectLinkedAssistants} from '../../../web/src/state/agent-links.mjs';
const assistant=(node,id,name,adapter)=>({address:{node_id:node,kind:'assistant',agent_id:id},name,adapter});
const fixture=window.topologyFixture={calls:[],language:'en',scenario:'ready',links:[
 {id:'codex-link',peer:{node_id:'codex-control',kind:'codex',name:'Codex',address:'tcp://dev-box.example:8793'},status:'connected',revision:1,default_assistant_id:'review',assistants:[assistant('codex-control','review','Code review','codex'),assistant('codex-control','build','Development','codex')]},
 {id:'cloud-link',peer:{node_id:'cloud-id',kind:'pot',name:'Cloud Pot',address:'tcp://100.88.0.8:8793'},status:'connected',revision:1,default_assistant_id:'cloud-assistant',assistants:[assistant('cloud-id','cloud-assistant','Cloud assistant','pot')]}
]};
const activity=(assistant_id,values={})=>({assistant_id,running:0,queued:0,waiting_approval:0,waiting_allowance:0,chat_state:'idle',fenced:false,...values});
const sample=(node_id,hostname,assistants)=>({observed_at_ms:Date.now(),value:{node_id,hostname,assistants,outbox:{pending:0,oldest_at_ms:0}}});
fixture.links[0].observation=sample('codex-control','dev-box.local',[activity('review'),activity('build',{running:1,queued:2})]);
fixture.links[0].outbox={pending:3,oldest_at_ms:Date.now()-40000};fixture.links[0].last_exchange_at_ms=Date.now()-40000;
fixture.links[1].observation=sample('cloud-id','cloud-worker',[activity('cloud-assistant',{waiting_allowance:1})]);fixture.links[1].outbox={pending:0,oldest_at_ms:0};
fixture.localObservation=sample('local-id','mac-studio.local',[activity('dev',{running:1,chat_state:'running'}),activity('pot')]);
const spaces=[{id:'local',control_id:'local-id',name:'Local Pot',kind:'local',connected:true}];
fixture.clipboard='';Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async text=>{if(fixture.clipboardFail)throw Error('Synthetic clipboard denied');fixture.clipboard=text;},readText:async()=>{if(fixture.clipboardReadWait)return await new Promise(resolve=>fixture.releaseClipboardRead=resolve);if(fixture.clipboardFail)throw Error('Synthetic clipboard denied');return fixture.clipboard;}}});
fixture.networks={local:{listen:'127.0.0.1:8793',endpoint:'tcp://100.101.102.103:8793',mode:'address'}};
const policy=()=>({machines:{'my-mac':{execution_access:'ask',revision:1}},rules:[],task_grants:[],application:{}});
const localAssistants=[{id:'dev',name:'Development assistant',space_id:'local'},{id:'pot',name:'Pot',space_id:'local'}];
const refreshDirectory=()=>{client.assistants=[...localAssistants,...projectLinkedAssistants({items:fixture.links},spaces[0])];};
const client={desktop:true,production:true,runtime:{boot_id:'fixture-boot'},spaces,assistants:localAssistants,scope:()=>({space:'local',owner:'dev'}),request:async(path,options={})=>{
 fixture.calls.push({path,...options});const body=options.body||{},space=options.space||'local';
 if(path.startsWith('/api/v1/agent-links/invitation'))return {address:(body.endpoint||'tcp://fixture.example:8793')+'#synthetic-local-invitation',expires_at_ms:Date.now()+3600000};
 if(path.startsWith('/api/v1/agent-links')){
  if(body.action==='configure'){
   if(body.mode==='tailscale'&&fixture.scenario==='tsmissing')throw Object.assign(Error('Synthetic Tailscale executable missing'),{code:'agent_link_tailscale_missing'});
   if(body.mode==='address'&&(!body.endpoint.startsWith('tcp://')||!/^(100\.|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(new URL(body.endpoint).hostname)))throw Object.assign(Error('Synthetic non-private TCP endpoint rejected'),{code:'agent_link_invalid'});
   fixture.networks[space]={...fixture.networks[space],...body,endpoint:body.mode==='tailscale'?'tcp://local-pot.fixture-tailnet.ts.net:8793':body.endpoint};fixture.scenario='ready';document.querySelector('#fixture-scenario').value='ready';return {network:fixture.networks[space]};
  }
  if(body.action==='preview'){
   if(fixture.previewWait)await new Promise(resolve=>fixture.releasePreview=resolve);
   if(fixture.scenario==='expired')throw Object.assign(Error('Synthetic invitation expired'),{code:'invitation_expired'});
   const peer={...(fixture.agentPeer||{node_id:'second-codex-control',kind:'codex',name:'Second Codex'})};if(fixture.omitPreviewNodeID)delete peer.node_id;else if(fixture.previewNodeID!==undefined)peer.node_id=fixture.previewNodeID;
   return {peer,endpoint:'tcp://100.88.0.5:8793',fingerprint:'7c9e83a26d412fb8a05c91e473b602df8a19c7e354b068fda2e6c08b91f437ad',expires_at_ms:Date.now()+3600000};
  }
  if(body.action==='connect'){
   if(fixture.connectWait)await new Promise(resolve=>fixture.releaseConnect=resolve);
   if(fixture.scenario==='expired')throw Object.assign(Error('Synthetic invitation expired'),{code:'invitation_expired'});
   const link={id:fixture.agentLinkID||'new-agent',peer:fixture.agentPeer||{node_id:'second-codex-control',kind:'codex',name:'Second Codex'},status:'connected',revision:1,default_assistant_id:'main',assistants:[assistant('second-codex-control','main','Codex assistant','codex')]};
   if(!fixture.links.some(item=>item.id===link.id))fixture.links.push(link);refreshDirectory();return fixture.rawConnectionResult?{...link,peer:{address:{node_id:link.peer.node_id,kind:'assistant',agent_id:link.peer.id||'main'},adapter:link.peer.kind,name:link.peer.name}}:link;
  }
  if(['disconnect','reconnect'].includes(body.action)){const link=fixture.links.find(item=>item.id===body.id);link.status=body.action==='disconnect'?'disconnected':'connected';link.state=link.status;refreshDirectory();return link;}
  if(body.action==='delete'){fixture.links=fixture.links.filter(item=>item.id!==body.id);refreshDirectory();return {deleted:true};}
  const network=fixture.networks[space],needsAccess=['access','tsmissing'].includes(fixture.scenario),endpoint=needsAccess?'':network.endpoint;
  if(!fixture.freezeObservations){fixture.localObservation.observed_at_ms=Date.now();for(const link of fixture.links)if(link.observation)link.observation.observed_at_ms=Date.now();}
  return {addresses:fixture.addresses||[{kind:'tailscale',endpoint:'tcp://local-pot.fixture-tailnet.ts.net:8793',requires_setup:true},{kind:'public',interface:'en0',endpoint:'tcp://203.0.113.10:8793'},{kind:'private',interface:'en0',endpoint:'tcp://192.168.1.24:8793'},{kind:'loopback',endpoint:'tcp://127.0.0.1:8793'}],self:{node_id:'local-id',kind:'pot',name:'Local Pot',address:endpoint,endpoint,status:needsAccess?'needs_access':'ready',observation:fixture.localObservation},items:fixture.links,network:{...network,endpoint}};
 }
 if(path==='/api/v1/machines')return {items:[{id:'my-mac',name:'my-mac',hostname:'mac-studio.local',ip_addresses:['192.168.1.24','fd00::24'],os:'darwin',connected:true}]};
 if(path==='/api/v1/permissions')return {policy:policy()};
 return {};
}};
refreshDirectory();fixture.client=client;
const title=document.querySelector('#fixture-title'),back=document.querySelector('#fixture-back'),host=document.querySelector('.page-host');
fixture.pages=createPages({client,host,approvalHost:document.querySelector('#fixture-approvals'),chatApprovalHost:document.createElement('div'),chatVisible:()=>false,locale:()=>fixture.language,notify:message=>console.info('fixture notification',message),changed:()=>{refreshDirectory();fixture.pages.refresh(['agent-links']);},titleChanged:()=>{title.textContent=fixture.pages.title;back.hidden=!fixture.pages.hasParent;},openChat:target=>{fixture.chat=target;}});
back.onclick=()=>fixture.pages.back();document.querySelector('#fixture-language').onchange=event=>{fixture.language=event.target.value;window.dispatchEvent(new Event('pot:locale'));};document.querySelector('#fixture-theme').onchange=event=>{document.documentElement.dataset.theme=event.target.value;};document.querySelector('#fixture-scenario').onchange=event=>{fixture.scenario=event.target.value;if(!fixture.pages.editing)fixture.pages.show('machines',{force:true});};await fixture.pages.show('machines');
