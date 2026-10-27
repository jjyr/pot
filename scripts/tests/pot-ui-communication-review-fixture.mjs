// The same synthetic records mount the actual Chat in each archived source tree.
// No transport, model, Task, approval, notification or product storage is used.
import {mountApp} from '../../web/src/components/ui.js';

const params=new URLSearchParams(location.search),scenario=params.get('scenario')||'send';
const local={node_id:'review-local',kind:'assistant',agent_id:'pot'};
const remote={node_id:'review-codex',kind:'assistant',agent_id:'review'};
const text='**Review summary**\n\n- Keep the message easy to read.\n- Check the same layout at phone width.\n\n```js\nconst safe = true;\nconst description = "A long code line stays inside its own scrollable code block at phone width.";\n```\n\nThis paragraph checks natural long-message reading, with clear spacing between paragraphs, lists and code. The same synthetic text is shown in both source versions.\n\n<script>window.reviewInjected = true</script>';
const tool=(id,name,args,result)=>({id,role:'tool',status:'complete',text:'',tool:{id,turn_id:'review',name,args,result,phase:'completed'}});
const messages=[
  {id:'request',request_id:'review',role:'user',status:'complete',text:'Review the communication UI using this synthetic conversation.'},
  tool('review-send','send_message',{to:remote,text},{accepted:true,message_id:'synthetic-receipt'}),
  tool('review-receive','receive_message',{messages:[{from:remote,to:local,text}]}),
  {id:'reply',reply_to:'review',role:'assistant',status:'complete',text:'This fixture shows a simulated accepted receipt and an incoming message. No message was sent and no review task was started.'},
].map((message,index)=>({...message,sequence:String(index+1),version:1,created_at_ms:0}));
const space={id:'review-local',control_id:'review-local',name:'Review fixture',kind:'local',connected:true};
let scope={space:space.id,owner:'pot'};
const errors=[];
window.addEventListener('error',event=>errors.push(event.message));
window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason)));
localStorage.setItem('pot-system-notifications-browser-v1','false');
const snapshot=()=>({kind:'reset',runtime_id:space.id,boot_id:'review',owner_id:JSON.stringify([space.id,scope.owner]),conversation_id:'main',cursor:'1',running:false,turn_id:null,replies:[],messages,history:{before:'1',after:String(messages.length),has_more:false,has_newer:false}});
const client={production:true,runtime:{runtime_id:space.id,boot_id:'review'},spaces:[space],scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},
  subscribe(receive,status){queueMicrotask(()=>{status('connected');receive(snapshot());});return()=>{};},
  async request(path,options={}){
    if(options.method&&options.method!=='GET')throw Error('Review fixture rejects mutations');
    const name=new URL(path,location.href).pathname;
    if(name==='/api/v1/spaces')return {items:[space],active_space_id:space.id};
    if(name==='/api/v1/assistants')return {items:[{id:'pot',name:'Pot'}]};
    if(name==='/api/v1/agent-links')return {items:[{id:'review-link',peer:{node_id:remote.node_id,kind:'codex',name:'Codex'},status:'connected',assistants:[{address:remote,name:'Code reviewer',adapter:'codex'}]}]};
    return {items:[],pending:[],policy:{}};
  },
};
mountApp(document.querySelector('#app'),client);
const frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
window.communicationReview={errors,scenario,text,async prepare(){
  const id=scenario==='receive'?'review-receive':'review-send';
  let card;
  for(let i=0;i<180;i++){
    card=document.querySelector(`[data-call-id="${id}"]`);
    if(card)break;
    await frame();
  }
  if(!card)throw Error('Review card did not mount');
  // Use native production disclosures; never inject alternate UI or screenshot CSS.
  for(const item of document.querySelectorAll('details.tool-call'))if(item!==card&&item.open)item.querySelector('summary').click();
  if(scenario!=='collapsed'&&!card.open)card.querySelector('summary').click();
  await frame();
  const raw=card.querySelector('.tool-raw');
  if(scenario==='details'&&!raw.open){raw.querySelector('summary').click();await frame();}
  card.scrollIntoView({block:'start'});
  await document.fonts.ready;
  await frame();
  // Long phone records can hit the scroll extent during disclosure reflow.
  // Keep the actual summary below the scroll viewport's top edge.
  const history=document.querySelector('.history:not([data-preparation-surface])'),inset=history.getBoundingClientRect().top+16-card.getBoundingClientRect().top;
  if(inset>0){history.dispatchEvent(new WheelEvent('wheel',{deltaY:-inset,bubbles:true}));history.scrollTop=Math.max(0,history.scrollTop-inset);history.dispatchEvent(new Event('scroll'));await frame();}
  if(window.reviewInjected||errors.length)throw Error('Unsafe rendering or runtime error: '+errors.join('; '));
  window.communicationReview.ready=true;
  return {scenario,primaryOpen:card.open,rawOpen:raw.open,errors};
}};
// Desktop starts in Chat. At phone widths, select Pot in the actual directory.
if(innerWidth>=720)window.communicationReview.preparation=window.communicationReview.prepare();
