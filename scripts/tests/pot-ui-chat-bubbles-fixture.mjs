// Actual Chat, in-memory synthetic data only. No model, transport or product store.
import {mountApp} from '../../web/src/components/ui.js';
const params=new URLSearchParams(location.search),scenario=params.get('scenario')||'conversation';
const space={id:'bubbles-local',control_id:'bubbles-local',name:'Review fixture',kind:'local',connected:true};
const capabilities={version:1,operations:['chat.read','chat.send','chat.cancel','chat.references','task.list','task.read','assistant.configure']};
const local={node_id:space.id,kind:'assistant',agent_id:'pot'},remote={node_id:'bubbles-peer',kind:'assistant',agent_id:'review'};
const avatarBlob=await (await fetch(new URL('../../web/src/assets/PotCompanion.png',import.meta.url))).blob();
const avatar=await new Promise(resolve=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result);reader.readAsDataURL(avatarBlob);});
const resource={id:'synthetic-report',name:'review-notes.md',entry:'review-notes.md',mime_type:'text/markdown',size:64,files:[{path:'review-notes.md'}],download:{path:'review-notes.md'}};
const attachment={id:'synthetic-attachment',name:'layout.png',mime_type:'image/png',size:avatarBlob.size};
const prose='## Keep the conversation easy to read\n\nThe user’s question stays on the right. Assistant replies keep their identity and let the content breathe. This synthetic paragraph checks natural reading width and wrapping across desktop and phone layouts.\n\n- Preserve **Markdown**, links and code.\n- Keep attachments, references and results available.\n\n```js\nconst description = "A deliberately long line stays inside the code block without widening the page: abcdefghijklmnopqrstuvwxyz0123456789";\nconst ready = true;\n```\n\n| Item | Review |\n| --- | --- |\n| Layout | Readable at narrow widths |\n\n[Review documentation](https://example.com/review)\n\n<script>window.bubblesInjected = true</script>';
const tool=(id,name,args,result)=>({id,role:'tool',status:'complete',text:'',tool:{id,turn_id:'request',name,args,result,phase:'completed'}});
const sets={
  conversation:[
    {id:'request',role:'user',request_id:'request',text:'Please summarize the review. Keep long code readable and preserve the assistant identity.'},
    {id:'short',role:'assistant',reply_to:'request',text:'I’ll keep the reading order clear and preserve the existing message actions.'},
    {id:'long',role:'assistant',reply_to:'request',text:prose},
  ],
  rich:[
    {id:'source',role:'assistant',text:'Keep the same reading width and make attachments and results easy to open.'},
    {id:'request',role:'user',request_id:'request',text:'Use this synthetic attachment and keep the report available.',attachments:[attachment],references:[{message_id:'source',quote:'Keep the same reading width.'}]},
    {id:'resource',role:'assistant',reply_to:'request',text:'The review notes are ready.',presentation:{mode:'preview',resource}},
    tool('review-send','send_message',{to:remote,text:'**Review request**\n\nPlease check the narrow layout and keep all controls available.'},{accepted:true,message_id:'synthetic-receipt'}),
    tool('review-receive','receive_message',{messages:[{from:remote,to:local,text:'**Review complete**\n\nThe same content remains readable at phone width.'}]}),
  ],
  failure:[
    {id:'request',role:'user',request_id:'request',text:'Summarize the results so far. Keep any partial response readable.'},
    {id:'partial',role:'assistant',reply_to:'request',status:'failed',text:'The first checks are complete. **This partial answer remains available**, including its [review link](https://example.com/review).\n\n```js\nconst retained = true;\n```',error:'Synthetic provider failure: HTTP 503; partial output retained.'},
  ],
};
let messages=(sets[scenario]||sets.conversation).map((m,i)=>({status:'complete',...m,sequence:String(i+1),version:1,created_at_ms:0}));
let scope={space:space.id,owner:'pot'},receive,version=1,sendCount=0,cancelCount=0,downloads=0;
const errors=[];
window.addEventListener('error',event=>errors.push(event.message));
window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason)));
localStorage.setItem('pot-system-notifications-browser-v1','false');
const snapshot=()=>({kind:'reset',runtime_id:space.id,boot_id:'bubbles',owner_id:JSON.stringify([space.id,scope.owner]),conversation_id:'main',cursor:String(version),running:messages.some(m=>m.status==='streaming'),turn_id:messages.some(m=>m.status==='streaming')?'request':null,replies:[],messages,agent:{attachments:{image:true,pdf:true}},history:{before:'1',after:String(messages.length),has_more:false,has_newer:false}});
const task={id:'synthetic-task',title:'Check the narrow layout',phase:'running',assistant_id:'pot',address:local,started_at_ms:0};
const client={chatCacheName:'pot-bubbles-'+crypto.randomUUID(),production:true,runtime:{runtime_id:space.id,boot_id:'bubbles'},spaces:[space],scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},
  subscribe(callback,status){receive=callback;queueMicrotask(()=>{status('connected');callback(snapshot());});return()=>{};},
  snapshot:async()=>snapshot(),
  async send(){sendCount++;throw Object.assign(Error('Synthetic send rejection; retry remains local.'),{status:409});},
  async cancel(){cancelCount++;messages=messages.map(m=>m.id==='stream'?{...m,status:'cancelled',version:++version}:m);receive(snapshot());},
  attachmentURL:()=>URL.createObjectURL(avatarBlob),downloadAttachment:async()=>{downloads++;},
  acquireResource:()=>({read:async()=>new Blob(['# Review notes\n\nSynthetic result. No private information.']),release(){}}),
  downloadResource:async()=>{downloads++;},
  async request(path,options={}){
    if(options.method&&options.method!=='GET')throw Error('Fixture rejects backend mutations');
    const name=new URL(path,location.href).pathname;
    if(name==='/api/v1/spaces')return {items:[space],active_space_id:space.id};
    if(name==='/api/v1/assistants')return {items:[{id:'pot',name:'Pot',avatar,capabilities}]};
    if(name==='/api/v1/agent-links')return {items:[{id:'review-link',peer:{node_id:remote.node_id,kind:'codex',name:'Codex'},status:'connected',assistants:[{address:remote,name:'Code reviewer',adapter:'codex'}]}]};
    if(name==='/api/v1/tasks/query')return {items:scenario==='rich'?[task]:[]};
    if(name.includes('/tasks/synthetic-task'))return {task,...task,messages:[],inputs:[]};
    return {items:[],pending:[],policy:{}};
  },
};
mountApp(document.querySelector('#app'),client);
const frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
window.chatBubbles={errors,scenario,prose,get sendCount(){return sendCount;},get cancelCount(){return cancelCount;},get downloads(){return downloads;},async ready(){
  for(let i=0;i<180&&!document.querySelector('.message');i++)await frame();
  if(!document.querySelector('.message'))throw Error('Chat did not mount');
  await document.fonts.ready;await frame();
  const history=document.querySelector('.history:not([data-preparation-surface])');history.dispatchEvent(new WheelEvent('wheel',{deltaY:-10000,bubbles:true}));history.scrollTop=0;history.dispatchEvent(new Event('scroll'));await frame();
  return {scenario,errors};
},async stream(text='Streaming synthetic reply…',status='streaming'){
  messages=messages.filter(m=>m.id!=='stream');messages.push({id:'stream',role:'assistant',reply_to:'request',status,text,sequence:String(messages.length+1),version:++version});receive(snapshot());await frame();
}};
