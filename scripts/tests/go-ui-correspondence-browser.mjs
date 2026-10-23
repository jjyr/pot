// Production Chat with explicit synthetic correspondence; no model or product data.
import {mountApp} from '../../web/src/components/ui.js';

const params=new URLSearchParams(location.search),before=params.has('before');
const root=document.querySelector('#app');
document.documentElement.dataset.platform='browser';
localStorage.setItem('pot-system-notifications-browser-v1','false');
let scope={space:'fixture',owner:'main'},receive,version=1;
const errors=[];
window.addEventListener('error',event=>errors.push(event.message));
window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason)));
const incoming={id:'tony-message',kind:'message',from:{node_id:'fixture',kind:'assistant',agent_id:'tony'},to:{node_id:'fixture',kind:'assistant',agent_id:'main'},text:'设计方案已完成。请结合现有产品向用户汇报。<script>window.peerExecuted=true</script>'};
const messages=[
  {id:'owner',request_id:'owner-request',role:'user',status:'complete',text:'请结合托尼的设计，先向我汇报具体调整方向。'},
  {id:'receipt',request_id:'pot-result-tony-message',role:'system',status:'complete',text:'INTERNAL_REFERENCE_INPUT',...(before?{}:{correspondence:[incoming]})},
  ...before?[]:[{id:'report',role:'assistant',reply_to:'receipt',status:'complete',text:'已收到托尼的方案。我会结合现有产品，从配色、对话展示和 Agent 协作三个方面汇报调整建议。'}],
].map((message,index)=>({...message,sequence:String(index+1),version:1,created_at_ms:0}));
const snapshot=()=>({kind:'reset',runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify(['fixture',scope.owner]),conversation_id:'main',cursor:String(version),replies:[],running:false,turn_id:null,messages:scope.owner==='main'?messages:[],history:{before:'1',after:String(messages.length),has_more:false,has_newer:false}});
const client={production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},
  subscribe(callback,status){receive=callback;queueMicrotask(()=>{status('connected');callback(snapshot());});return()=>{};},
  async request(path){
    const url=new URL(path,location.href);
    if(url.pathname==='/api/v1/spaces')return {active_space_id:'fixture',items:[{id:'fixture',name:'Fixture',kind:'local',control_id:'fixture',connected:true}]};
    if(url.pathname==='/api/v1/assistants')return {items:[{id:'main',name:'Pot'},{id:'tony',name:'设计师托尼'}]};
    return {items:[],pending:[],policy:{}};
  },
};
mountApp(root,client);
window.correspondenceFixture={errors,refresh(){version++;receive?.(snapshot());},async verify(){
  const check=(value,message)=>{if(!value)throw Error(message);};
  const card=root.querySelector('details.tool-call');
  check(!!card&&!card.open,'incoming receipt starts collapsed');
  check(!root.textContent.includes('INTERNAL_REFERENCE_INPUT'),'internal input stays hidden');
  card.querySelector('summary').click();
  await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
  check(card.open&&card.textContent.includes(incoming.text),'expanded receipt contains the original text');
  check(!window.peerExecuted,'incoming HTML stays text');
  this.refresh();await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
  check(root.querySelector('details.tool-call')===card&&card.open,'refresh preserves the receipt and disclosure');
  check(root.querySelectorAll('details.tool-call').length===1,'refresh never duplicates a receipt');
  card.querySelector('summary').click();
  check(errors.length===0,'no browser runtime errors');
  return {passed:true,collapsed:true,expanded:true,refreshPreserved:true,htmlIsText:true};
}};
