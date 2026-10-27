// The same synthetic records mount the actual Chat in each archived source tree.
// No transport, model, Task, approval, notification or product storage is used.
import {mountApp} from '../../web/src/components/ui.js';

const params=new URLSearchParams(location.search),scenario=params.get('scenario')||'send';
const local={node_id:'review-local',kind:'assistant',agent_id:'pot'};
const remote={node_id:'review-codex',kind:'assistant',agent_id:'review'};
const text='**Review summary**\n\n- Keep the message easy to read.\n- Check the same layout at phone width.\n\n```js\nconst safe = true;\nconst description = "A long code line stays inside its own scrollable code block at phone width.";\n```\n\nThis paragraph checks natural long-message reading, with clear spacing between paragraphs, lists and code. The same synthetic text is shown in both source versions.\n\n<script>window.reviewInjected = true</script>';
const tool=(id,name,args,result)=>({id,role:'tool',status:'complete',text:'',tool:{id,turn_id:'review',name,args,result,phase:'completed'}});
const edge=params.has('edge');
const taskA={node_id:'review-local',kind:'task',agent_id:edge?'8a1c1000-1111-4222-8333-444444444444':'a078c820-5dca-4aba-b473-e26a060224ab'};
const taskB={node_id:'review-local',kind:'task',agent_id:edge?'8a1cf000-1111-4222-8333-444444444444':'b16981fa-e96c-4bec-bace-f3f4e5479be8'};
const taskC={...taskA,node_id:'review-remote',agent_id:edge?taskA.agent_id:'c25abc79-83f3-48f6-9df9-9e5481f739e9'};
const send=(id,to,text)=>tool(id,'send_message',{to,text},{accepted:true});
const receive=(id,from,text)=>tool(id,'receive_message',{messages:[{from,to:local,text}]});
const messages=[
  {id:'request',request_id:'review',role:'user',status:'complete',text:'Synthetic fixture · Review adjacent task communication.'},
  send('mail-1',taskA,'Please review the communication layout.'),
  receive('mail-2',taskB,'The short ID stays visible in both directions.'),
  send('mail-3',taskB,'Check narrow screens and dark mode.'),
  receive('mail-4',taskA,'The source order is preserved.'),
  tool('mail-5','receive_message',{messages:[{from:taskC,to:local,text:edge?'This task has the same ID on another node.':'This task runs on another node.'},{from:taskA,to:local,text:'One receive record contains two messages.'}]}),
  send('mail-6',taskA,'Thanks. Keep call details available on demand.'),
  {id:'reply',reply_to:'review',role:'assistant',status:'complete',text:'Synthetic messages only. No real Task or model was started.'},
].map((message,index)=>({...message,sequence:String(index+1),version:1,created_at_ms:0}));
if(params.has('single'))messages.splice(2,5);
let emit,cursor=1;
const space={id:'review-local',control_id:'review-local',name:'Review fixture',kind:'local',connected:true};
let scope={space:space.id,owner:'pot'};
const errors=[];
window.addEventListener('error',event=>errors.push(event.message));
window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason)));
localStorage.setItem('pot-system-notifications-browser-v1','false');
const snapshot=()=>({kind:'reset',runtime_id:space.id,boot_id:'review',owner_id:JSON.stringify([space.id,scope.owner]),conversation_id:'main',cursor:String(cursor),running:false,turn_id:null,replies:[],messages,history:{before:'1',after:String(messages.length),has_more:false,has_newer:false}});
const catalogue=[{id:taskA.agent_id,address:taskA,title:'Layout review',phase:'complete'},{id:taskB.agent_id,address:taskB,title:'Responsive checks',phase:'complete'},{id:edge?'local-hash':taskC.agent_id,address:taskC,title:'Remote review',phase:'complete'}];
if(params.has('identity')){
  catalogue[0].phase='running';catalogue[1].phase='queued';catalogue[2].phase='waiting';
  catalogue[0].title='Layout review · Long task title that truncates while keeping its identity visible';
  catalogue.push({id:'unknown-catalogue',title:'Unconfirmed address',phase:'queued'},...Array.from({length:3},(_,index)=>({id:edge?'extra-'+index:['d3618b5a-6722-4f38-bdf4-cf269db87e83','e4724fa2-fdef-46f1-b36a-8eb1bd25ed13','f583a019-a3bf-43b9-80ef-f9d651f549bf'][index],address:{node_id:'review-local',kind:'task',agent_id:edge?'extra-'+index:['d3618b5a-6722-4f38-bdf4-cf269db87e83','e4724fa2-fdef-46f1-b36a-8eb1bd25ed13','f583a019-a3bf-43b9-80ef-f9d651f549bf'][index]},title:'Background check '+(index+1),phase:'running'})));
}
const changes=new Set(),requests=[];
const client={production:true,runtime:{runtime_id:space.id,boot_id:'review'},spaces:[space],scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:callback=>{changes.add(callback);return()=>changes.delete(callback);},
  subscribe(receive,status){emit=receive;queueMicrotask(()=>{status('connected');receive(snapshot());});return()=>{};},
  async request(path,options={}){
    if(options.method&&options.method!=='GET')throw Error('Review fixture rejects mutations');
    const name=new URL(path,location.href).pathname;
    requests.push({path:name,space:options.space,owner:options.owner});
    if(name==='/api/v1/spaces')return {items:[space],active_space_id:space.id};
    if(name==='/api/v1/tasks/query')return {items:catalogue};
    if(name==='/api/v1/runtime')return {runtime_id:'fixture',host:{}};
    const task=catalogue.find(task=>name==='/api/v1/tasks/'+encodeURIComponent(task.id));
    if(task)return {info:task,request:{text:task.title},capabilities:{version:1,operations:['task.read']}};
    if(name==='/api/v1/assistants')return {items:[{id:'pot',name:'Pot',capabilities:{version:1,operations:['chat.read','task.list']}}]};
    if(name==='/api/v1/agent-links')return {items:[{id:'review-link',peer:{node_id:remote.node_id,kind:'codex',name:'Codex'},status:'connected',assistants:[{address:remote,name:'Code reviewer',adapter:'codex'}]}]};
    return {items:[],pending:[],policy:{}};
  },
};
mountApp(document.querySelector('#app'),client);
const frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
window.taskCommunication={errors,messages,send,receive,taskA,taskB,taskC,client,catalogue,requests,
  async tasks(change){change(catalogue);for(const callback of changes)callback(['tasks'],{space:space.id});await frame();await frame();},
  async update(change){change(messages);cursor++;emit(snapshot());await frame();await frame();},
  async prepare(){
    for(let i=0;i<180&&!document.querySelector('.work-records,.communication-stack, [data-call-id="mail-1"]');i++)await frame();
    const stack=document.querySelector('.work-records,.communication-stack');
    if(scenario!=='collapsed'){
      if(stack){if(!stack.open)stack.querySelector('summary').click();}
      else for(const item of document.querySelectorAll('.communication-tool'))if(!item.open)item.querySelector('summary').click();
    }
    await frame();
    if(scenario!=='collapsed')for(const item of document.querySelectorAll('.communication-tool'))if(!item.open)item.querySelector('summary').click();
    await frame();await document.fonts.ready;
    const history=document.querySelector('.history');history.dispatchEvent(new WheelEvent('wheel',{deltaY:-10000,bubbles:true}));history.scrollTop=0;history.dispatchEvent(new Event('scroll'));await frame();
    if(errors.length)throw Error(errors.join('; '));this.ready=true;
  }
};
