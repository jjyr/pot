// Production chat with synthetic API data. No model or host commands execute.
import {mountApp} from '../../web/src/components/ui.js';
const query=new URLSearchParams(location.search),f=window.linkedApprovalFixture={pending:true,allowed:true,calls:[],errors:[]};
let scope={space:'one',owner:'main'},cursor=0;const changes=new Set();
localStorage.setItem('pot-system-notifications-browser-v1','false');
window.addEventListener('error',event=>f.errors.push(event.message));window.addEventListener('unhandledrejection',event=>f.errors.push(String(event.reason)));
const capabilities={version:1,scope:'owner',operations:['chat.read','chat.send','chat.receipt','chat.subscribe','approvals.manage','task.list']};
const pending=()=>({id:'linked:fixture-request',once_only:true,can_decide:f.allowed,source:'Codex · mac-mini',details:{threadId:'fixture-thread',turnId:'fixture-turn',command:'ssh deployment-host hostname',cwd:'/workspace',reason:'Verify the deployment host'},request:{owner:'main',tool:'item/commandExecution/requestApproval',args:{cmd:'ssh deployment-host hostname',justification:'Verify the deployment host'},turn:'fixture-work',call_id:'fixture-call',machine_id:'codex'}});
const snapshot=()=>({runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify([scope.space,scope.owner]),conversation_id:'chat',cursor:String(++cursor),replies:[],running:false,turn_id:null,agent:{phase:'ready'},messages:[{id:'question',role:'user',text:'请让 Codex 协助配置部署机访问。',status:'complete'}]});
const client={production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},snapshot:async()=>snapshot(),subscribe:(receive,status)=>{queueMicrotask(()=>{status('connected');receive(snapshot());});return()=>{};},changes:callback=>{changes.add(callback);return()=>changes.delete(callback);},
 request:async(path,options={})=>{
   const url=new URL(path,location.href);
   if(url.pathname==='/api/v1/spaces')return {active_space_id:'one',items:[{id:'one',name:'devpc',control_id:'fixture',connected:true}]};
   if(url.pathname==='/api/v1/assistants')return [{id:'main',name:'Pot',revision:1,capabilities}];
   if(url.pathname==='/api/v1/permissions'){
     if(options.method==='POST'){f.calls.push(options.body);if(!f.pending)throw Error('expired');f.pending=false;f.refresh();return {accepted:true};}
     return {policy:{machines:{}},pending:f.pending?[pending()]:[]};
   }
   return {items:[],cursor:0,has_more:false};
 },send:async()=>{throw Error('Fixture send disabled');},cancel:async()=>{},markRead:async value=>value};
f.refresh=()=>{for(const changed of changes)changed(['permissions'],{space:'one'});};
f.language=locale=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value:locale}}));
f.reset=({allowed=true}={})=>{f.pending=true;f.allowed=allowed;f.refresh();};
f.dispose=mountApp(document.querySelector('#app'),client);
f.ready=true;
