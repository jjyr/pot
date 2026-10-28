// Actual Chat/Task renderers, in-memory synthetic snapshots only. Every business
// mutation is rejected; controls only replace this fixture's source records.
import {mountApp} from '../../web/src/components/ui.js';
const params=new URLSearchParams(location.search),actor={node_id:'review-local',kind:'assistant',agent_id:'pot'},peer={node_id:'review-peer',kind:'assistant',agent_id:'review'},other={...peer,agent_id:'missing'};
const taskA={node_id:'review-local',kind:'task',agent_id:'6061c07a-0e65-4ed8-b74a-591a4312ce70'},taskB={node_id:'review-remote',kind:'task',agent_id:'6061f20c-7625-4e08-910f-2e474adcb778'};
const png=await (await fetch(new URL('./fixtures/product-overview-avatar.png',import.meta.url))).blob();
const avatar=await new Promise(resolve=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result);reader.readAsDataURL(png);});
const tool=(id,name,args={},result={},extra={})=>({id,viewKey:id,role:'tool',status:'complete',text:'',tool:{id,turn_id:'review',name,args,result,phase:'completed',started_at_ms:1000,finished_at_ms:2000,...extra}});
const send=(id,to,text)=>tool(id,'send_message',{to,text},{accepted:true,message_id:id});
const receive=(id,from,text)=>tool(id,'receive_message',{messages:[{id,from,to:actor,text}]});
const messages=[
 {id:'request',request_id:'review',role:'user',status:'complete',text:'请整体核对工具、任务和通信记录，给出紧凑展示方案。'},
 tool('exec','exec',{code:'const results = await Promise.all([tools.exec_command({cmd:"rg createToolCall web/src"}), tools.demo_search({query:"compact work"})]); text(results);'},{completed:true,output:'Two synthetic calls completed.'}),
 tool('read','exec_command',{cmd:'rg createToolCall web/src'},{exit_code:0,stdout:'ui.js\ntool-call.mjs\nwork-record-window.mjs\n'},{parent_id:'exec'}),
 tool('search','demo_search',{query:'compact work'},{items:[{title:'Synthetic source'}]},{parent_id:'exec'}),
 tool('create','create_task',{text:'核对聊天记录的边界'},{id:taskA.agent_id,address:taskA,title:'核对聊天记录',phase:'queued'}),
 send('m1',taskA,'请同时核对普通工具、编排子调用和助手通信。'),
 receive('m2',peer,'**核对结论**\n\n工具与通信应按原顺序混排。审批和交付保持独立。'),
 tool('browser','cua_repl',{code:'let state = await tab.observe();'},{demo:true}),
 receive('m3',taskB,'同前缀任务的完整地址不同，四位 ID 不扩展。'),
 receive('m4',other,'资料头像缺失时使用通用图标。'),
 tool('inspect','get_task',{task_id:taskB.agent_id},{phase:'complete'}),
 {id:'compact',viewKey:'compact',role:'system',text:'',compaction:{state:'complete',reason:'threshold',before:32000,after:9600,finished_at_ms:2700},created_at_ms:2200},
 {id:'reply',reply_to:'review',role:'assistant',status:'complete',text:'统一为一行工作记录；展开后保留工具、任务与通信的原始顺序。审批与交付继续直接可见。'},
];
messages.forEach((m,i)=>{m.status||='complete';m.sequence=String((i+1)*10);m.version=1;});
let emit,cursor=1,running=false,scope={space:'review-local',owner:'pot'};const changes=new Set(),errors=[];
const space={id:'review-local',control_id:'review-local',name:'Synthetic review',kind:'local',connected:true};
const catalogue=[{id:taskA.agent_id,address:taskA,title:'核对聊天记录',phase:'complete'},{id:'local-directory-hash',address:taskB,title:'窄屏检查',phase:'complete'}];
const snapshot=()=>({kind:'reset',runtime_id:space.id,boot_id:'work-records',owner_id:JSON.stringify([space.id,scope.owner]),conversation_id:'main',cursor:String(cursor),running,turn_id:running?'request':null,replies:[],messages,history:{before:'1',after:String(messages.length),has_more:false,has_newer:false}});
window.addEventListener('error',e=>errors.push(e.message));window.addEventListener('unhandledrejection',e=>errors.push(String(e.reason)));
localStorage.setItem('pot-system-notifications-browser-v1','false');
const client={production:true,runtime:{runtime_id:space.id,boot_id:'work-records'},spaces:[space],scope:()=>({...scope}),setScope:v=>scope={...scope,...v},changes:cb=>{changes.add(cb);return()=>changes.delete(cb)},subscribe(receive,status){emit=receive;queueMicrotask(()=>{status('connected');receive(snapshot())});return()=>{}},async request(path,options={}){
 if(options.method&&options.method!=='GET')throw Error('Fixture rejects business mutations');const name=new URL(path,location.href).pathname;
 if(name==='/api/v1/spaces')return {items:[space],active_space_id:space.id};
 if(name==='/api/v1/tasks/query')return {items:catalogue};
 if(name==='/api/v1/assistants')return {items:[{id:'pot',name:'协调助手（演示）',avatar,capabilities:{version:1,operations:['chat.read','task.list']}}]};
 if(name==='/api/v1/agent-links')return {items:[{id:'demo-link',peer:{node_id:peer.node_id,kind:'pot',name:'Demo peer'},status:'connected',assistants:[{address:peer,name:'开发助手',avatar,adapter:'pot'},{address:other,name:'资料助手',adapter:'pot'}]}]};
 return {items:[],pending:[],policy:{}};
}};
mountApp(document.querySelector('#app'),client);
const frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
const fixture=window.workRecordsFixture={messages,errors,client,catalogue,actor,peer,other,taskA,taskB,tool,send,receive,
 async update(change){change(messages);cursor++;emit(snapshot());await frame();await frame();},
 async prepare(){for(let i=0;i<120&&!document.querySelector('.work-records,[data-call-id="exec"]');i++)await frame();await document.fonts.ready;
  if(params.get('scenario')==='expanded')for(const group of document.querySelectorAll('.work-records'))if(!group.open)group.querySelector('summary').click();
  const history=document.querySelector('.history:not([data-preparation-surface])');history.dispatchEvent(new WheelEvent('wheel',{deltaY:-10000,bubbles:true}));history.scrollTop=0;history.dispatchEvent(new Event('scroll'));await frame();this.ready=true;},
 async run(){running=true;await this.update(rows=>{const browser=rows.find(m=>m.id==='browser');browser.tool={...browser.tool,phase:'running',started_at_ms:Date.now(),finished_at_ms:undefined};browser.version++;rows.splice(rows.findIndex(m=>m.id==='reply'),1);});},
 async fail(){running=false;await this.update(rows=>{const browser=rows.find(m=>m.id==='browser');browser.tool={...browser.tool,phase:'failed',finished_at_ms:Date.now(),result:{error:'Synthetic source unavailable'}};browser.version++;});}
};
if(params.has('controls')){
 const controls=document.createElement('div');controls.style.cssText='position:fixed;z-index:100;right:12px;top:10px;display:flex;gap:6px';
 for(const [label,run]of [['展开工作记录',()=>{for(const d of document.querySelectorAll('.work-records'))if(!d.open)d.querySelector('summary').click()}],['模拟运行',()=>fixture.run()],['模拟失败',()=>fixture.fail()]]){const b=document.createElement('button');b.textContent=label;b.onclick=run;controls.append(b);}document.body.append(controls);
}
