// Reproducible product imagery: actual shared UI, synthetic read-only records.
// No production transport, credentials, model, message or Task creation is used.
import {mountApp} from '../../../web/src/components/ui.js';
import {avatarFromFile} from '../../../web/src/components/avatar.mjs';
const avatar=await avatarFromFile(await (await fetch('../../../resources/app/assets/Pot384.png')).blob());
const developmentAvatar=await avatarFromFile(await (await fetch('./product-overview-avatar.png')).blob());
const linked=new URLSearchParams(location.search).get('view')==='link';
const now=Date.now();
const space={id:linked?'demo-linked':'demo-work',control_id:linked?'demo-linked':'demo-work',name:'Local Pot',kind:'local',connected:true};
const local={node_id:space.id,kind:'assistant',agent_id:'pot'};
const remote={node_id:'demo-codex',kind:'assistant',agent_id:'build'};
let scope={space:space.id,owner:'pot'};
const origin={node_id:remote.node_id,kind:'codex',name:'Codex',link_id:'demo-link',assistant_id:'build'};
const messages=linked?[
 {role:'user',text:'Ask Codex to review the installation docs and return the changes here.'},
 {role:'assistant',text:'I’ll delegate the documentation review to Codex through Agent Link. It will use its own workspace and permissions.'},
 {role:'tool',text:'',tool:{id:'delegate',turn_id:'demo',name:'send_message',args:{to:remote,text:'Review the installation docs. Check platform requirements, commands and first configuration.'},result:{accepted:true,message_id:'demo-receipt'},phase:'completed'}},
 {role:'tool',text:'',tool:{id:'returned',turn_id:'demo',name:'receive_message',args:{messages:[{from:remote,to:local,text:'Review complete. The draft now separates Experimental Linux desktop from Server and Docker, includes first setup, and keeps credentials in the local UI. The updated document is ready for your review.'}]},phase:'completed'}},
 {role:'assistant',text:'**Codex returned the documentation review.**\n\n- Platform requirements and build commands clarified.\n- Model provider and optional GitHub setup added.\n- No credentials included in the document.\n\nThe draft is ready. Would you like to refine the examples next?'},
]:[
 {role:'user',text:'Get this project ready to share: verify Linux installation, complete the getting-started docs, and prepare representative product screenshots.'},
 {role:'assistant',text:'I’ll handle installation, documentation and screenshots in parallel, then bring the results back here.'},
 {role:'assistant',text:'**The first results are ready.**\n\nThe getting-started draft is ready for review. Installation checks are running; desktop behavior still needs checking on the target system.\n\n[Getting-started draft](https://example.invalid/getting-started.md) · Markdown document'},

];
messages.forEach((m,i)=>Object.assign(m,{id:'demo-'+i,request_id:i===0?'demo-request':undefined,status:'complete',sequence:String(i+1),version:1,created_at_ms:0}));
const tasks=(linked?[['d3618b5a-6722-4f38-bdf4-cf269db87e83','Review installation docs with Codex','running']]:[
 ['a078c820-5dca-4aba-b473-e26a060224ab','Verify Linux installation','running'],['b16981fa-e96c-4bec-bace-f3f4e5479be8','Complete getting-started docs','waiting'],['c25abc79-83f3-48f6-9df9-9e5481f739e9','Prepare product screenshots','running'],
]).map(([id,title,phase])=>({id,title,phase,address:{node_id:space.id,kind:'task',agent_id:id},started_at_ms:now-146000,...(phase==='complete'?{finished_at_ms:now-24000}:{}),assistant_id:'pot',...(linked?{agent_link:origin,executor_icon:'codex'}:{})}));
const snapshot=()=>({kind:'reset',runtime_id:space.id,boot_id:'demo',owner_id:JSON.stringify([space.id,scope.owner]),conversation_id:'main',cursor:'1',running:false,turn_id:null,replies:[],messages,history:{before:'1',after:String(messages.length),has_more:false,has_newer:false}});
localStorage.setItem('pot-system-notifications-browser-v1','false');
const fixture=window.productOverview={errors:[],mutations:[]};
window.addEventListener('error',e=>fixture.errors.push(e.message));
window.addEventListener('unhandledrejection',e=>fixture.errors.push(String(e.reason)));
const reject=()=>{fixture.mutations.push('rejected');throw Error('Screenshot fixture is read-only');};
const client={production:true,runtime:{runtime_id:space.id,boot_id:'demo'},spaces:[space],scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},send:reject,
 subscribe(receive,status){queueMicrotask(()=>{status('connected');receive(snapshot());});return()=>{};},
 async request(path,options={}){
  if(options.method&&options.method!=='GET')return reject();
  const name=new URL(path,location.href).pathname;
  if(name==='/api/v1/spaces')return {items:[space],active_space_id:space.id};
  if(name==='/api/v1/assistants')return [{id:'pot',name:'Pot',avatar,revision:1,capabilities:{version:1,operations:['chat.read','chat.send','task.list','task.read']}},...['Operations','Research'].map((name,i)=>({id:'specialist-'+i,name,avatar:developmentAvatar,revision:1,capabilities:{version:1,operations:['chat.read']}}))];
  if(name==='/api/v1/agent-links')return {items:[{id:'demo-link',peer:{node_id:remote.node_id,kind:'codex',name:'Codex'},status:'connected',assistants:[{address:remote,name:'Development',avatar:developmentAvatar,adapter:'codex',capabilities:{version:1,operations:['chat.read','task.list','task.read']}}]}]};
  if(name==='/api/v1/tasks/query'||name==='/api/v1/tasks/catalog')return {items:tasks,counts:{active:tasks.length},total:tasks.length};
  if(name==='/api/v1/tasks/outcomes')return {items:[],cursor:0,has_more:false};
  return {items:[],pending:[],policy:{machines:{}}};
 },
};
mountApp(document.querySelector('#app'),client);
