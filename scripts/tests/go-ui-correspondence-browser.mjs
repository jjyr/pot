// Production Chat with explicit synthetic correspondence; no model or product data.
import {translate} from '../../web/src/locales/messages.mjs';
import {mountApp} from '../../web/src/components/ui.js';

const params=new URLSearchParams(location.search),before=params.has('before');
const root=document.querySelector('#app');
document.documentElement.dataset.platform='browser';
localStorage.setItem('pot-system-notifications-browser-v1','false');
let scope={space:'fixture',owner:'main'},receive,changed,version=1,peerName='设计师托尼';
const errors=[];
window.addEventListener('error',event=>errors.push(event.message));
window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason)));
const incoming={id:'tony-message',kind:'message',from:{node_id:'fixture',kind:'assistant',agent_id:'tony'},to:{node_id:'fixture',kind:'assistant',agent_id:'main'},text:'设计方案已完成。请结合现有产品向用户汇报。<script>window.peerExecuted=true</script>'};
const messages=[
  {id:'owner',request_id:'owner-request',role:'user',status:'complete',text:'请结合托尼的设计，先向我汇报具体调整方向。'},
  {id:'receipt',request_id:'pot-result-tony-message',role:'system',status:'complete',text:'INTERNAL_REFERENCE_INPUT',...(before?{}:{correspondence:[incoming]})},
  ...before?[]:[{id:'outbound',role:'tool',text:'',status:'complete',tool:{id:'send-call',turn_id:'owner-request',name:'send_message',args:{to:incoming.from,text:'收到，请继续完善配色方案。'},phase:'completed',result:{accepted:true}}},{id:'report',role:'assistant',reply_to:'receipt',status:'complete',text:'已收到托尼的方案。我会结合现有产品，从配色、对话展示和 Agent 协作三个方面汇报调整建议。'}],
].map((message,index)=>({...message,sequence:String(index+1),version:1,created_at_ms:0}));
const snapshot=()=>({kind:'reset',runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify(['fixture',scope.owner]),conversation_id:'main',cursor:String(version),replies:[],running:false,turn_id:null,messages:scope.owner==='main'?messages:[],history:{before:'1',after:String(messages.length),has_more:false,has_newer:false}});
const client={production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:callback=>{changed=callback;return()=>{};},
  subscribe(callback,status){receive=callback;queueMicrotask(()=>{status('connected');callback(snapshot());});return()=>{};},
  async request(path){
    const url=new URL(path,location.href);
    if(url.pathname==='/api/v1/spaces')return {active_space_id:'fixture',items:[{id:'fixture',name:'Fixture',kind:'local',control_id:'fixture',connected:true}]};
    if(url.pathname==='/api/v1/assistants')return {items:[{id:'main',name:'Pot'},{id:'tony',name:peerName}]};
    return {items:[],pending:[],policy:{}};
  },
};
mountApp(root,client);
window.correspondenceFixture={errors,refresh(){version++;receive?.(snapshot());},async verify(){
  const check=(value,message)=>{if(!value)throw Error(message);};
  const card=root.querySelector('details.tool-call');
  check(!!card&&!card.open,'incoming receipt starts collapsed');
  const locale=()=>document.documentElement.lang,expected=key=>translate(locale(),key).replace('{name}','@'+peerName);
  check(card.querySelector('.tool-title').textContent===expected('tool.receiveMessageFrom'),'incoming uses sender display name');
  const outbound=root.querySelector('[data-call-id=send-call]');
  check(outbound?.querySelector('.tool-title').textContent===expected('tool.sendMessageTo'),'outgoing uses receiver display name');
  check(!root.textContent.includes('INTERNAL_REFERENCE_INPUT'),'internal input stays hidden');
  card.querySelector('summary').click();
  await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
  check(card.open&&card.textContent.includes(incoming.text),'expanded receipt contains the original text');
  check(!window.peerExecuted,'incoming HTML stays text');
  check(getComputedStyle(card.querySelector('.communication-text')).fontSize==='14px','Chat communication body keeps its reading typography inside tool rows');
  this.refresh();await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
  check(root.querySelector('details.tool-call')===card&&card.open,'refresh preserves the receipt and disclosure');
  check(root.querySelectorAll('details.tool-call').length===2,'refresh never duplicates a receipt');
  card.querySelector('summary').click();
  const input=root.querySelector('.composer-input');input.value='Unsent fixture draft';input.dispatchEvent(new Event('input',{bubbles:true}));
  peerName='设计师托尼（更新）';changed?.(['assistants']);
  for(let attempts=0;attempts<120&&card.querySelector('.tool-title').textContent!==expected('tool.receiveMessageFrom');attempts++)await new Promise(resolve=>requestAnimationFrame(resolve));
  check(card.querySelector('.tool-title').textContent===expected('tool.receiveMessageFrom'),'directory rename refreshes mounted receipt');
  check(outbound.querySelector('.tool-title').textContent===expected('tool.sendMessageTo'),'directory rename refreshes mounted send');
  check(input===root.querySelector('.composer-input')&&input.value==='Unsent fixture draft','directory refresh retains composer and draft');
  check(errors.length===0,'no browser runtime errors');
  return {passed:true,collapsed:true,expanded:true,refreshPreserved:true,htmlIsText:true};
}};

if(params.has('verify')){
  const result=document.createElement('output');result.style='position:fixed;bottom:0;left:0;z-index:100;background:white;color:black';document.body.append(result);
  try{for(let i=0;i<120&&!root.querySelector('[data-call-id=send-call]');i++)await new Promise(resolve=>requestAnimationFrame(resolve));await window.correspondenceFixture.verify();result.textContent='PASS: Chat names, disclosure, refresh, draft, escaping';}
  catch(error){result.textContent='FAIL: '+error.message;throw error;}
}
