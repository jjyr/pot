// Actual product shell and pages with isolated synthetic API responses. No model or product service.
const params=new URLSearchParams(location.search),prefix=params.has('before')?'/output/tasks-before':'';
const css=document.createElement('link');css.rel='stylesheet';css.href=prefix+'/web/src/styles.css';document.head.append(css);
await new Promise(resolve=>css.onload=resolve);
localStorage.setItem('pot-system-notifications-browser-v1','false');
const {mountApp}=await import(prefix+'/web/src/components/ui.js');
const capabilities={version:1,operations:['chat.read','chat.send','task.list','task.read','task.events','task.input','task.interrupt','task.control','task.archive','task.cancel','approvals.manage','schedules.manage']};
let scope={space:'one',owner:'alpha'};const changes=new Set(),now=Date.UTC(2026,6,10,9);Date.now=()=>now;
const f=window.tasksSchedulesFixture={calls:[],errors:[],delay:0,offline:false};
window.addEventListener('error',e=>f.errors.push(e.message));window.addEventListener('unhandledrejection',e=>f.errors.push(String(e.reason?.stack||e.reason)));
const spaces=[{id:'one',name:'Work computer',control_id:'node-a',connected:true},{id:'two',name:'Research node',control_id:'node-b',connected:true}];
// Synthetic saved-profile PNGs enter through the same local/Agent Link directory fields.
function profileAvatar(color) {
 const canvas=document.createElement('canvas');canvas.width=canvas.height=128;const c=canvas.getContext('2d');
 c.fillStyle=color;c.fillRect(0,0,128,128);c.fillStyle='#fff';c.beginPath();c.arc(64,49,23,0,Math.PI*2);c.fill();c.beginPath();c.ellipse(64,112,43,31,0,0,Math.PI*2);c.fill();
 c.fillStyle=color;c.beginPath();c.arc(56,47,3,0,Math.PI*2);c.arc(72,47,3,0,Math.PI*2);c.fill();return canvas.toDataURL('image/png');
}
f.avatars={local:profileAvatar('#168778'),linked:profileAvatar('#7755bb'),research:profileAvatar('#367ba6')};
const routeOwner=(space,id)=>space==='two'?'agent-link:research:'+id:id;
f.profiles={one:[{id:'alpha',name:'Pot',avatar:f.avatars.local,revision:1,reply_count:0,capabilities},{id:'beta',name:'Research',avatar:'',revision:1,reply_count:0,capabilities}],two:[{address:{node_id:'node-b',kind:'assistant',agent_id:'alpha'},name:'Pot',avatar:f.avatars.linked,capabilities},{address:{node_id:'node-b',kind:'assistant',agent_id:'beta'},name:'Research',avatar:f.avatars.research,capabilities}]};
const phases=['running','queued','waiting','waiting_approval','complete','failed','interrupted','cancelled'];
f.tasks=Array.from({length:94},(_,i)=>{const space=i%3===0?'two':'one',node=space==='one'?'node-a':'node-b',realID=(0xa810+i*0x17).toString(16)+'c820-a9bc-4def-8abc-'+i.toString(16).padStart(12,'0'),id=space==='two'?'catalogue-task-'+i:realID;return {id,title:['核查周报中的引用与来源','整理项目进展与后续步骤','检查隔离工作区内的构建输出','Review reference links and follow-up notes'][i%4]+' '+(i+1),assistant_id:routeOwner(space,i%5===0?'beta':'alpha'),phase:phases[i%8],archived:i>80,paused:i===8,pending:0,started_at_ms:now-123000,updated_at_ms:now-i*1000,created_at_ms:now-300000,address:{node_id:node,kind:'task',agent_id:realID},space};});
f.schedules=Array.from({length:46},(_,i)=>({id:'schedule-'+i,space:i%3===0?'two':'one',assistant_id:i===45?'missing-owner':routeOwner(i%3===0?'two':'one',i%5===0?'beta':'alpha'),spec:{title:['每日项目回顾','每周参考资料检查','工作区健康摘要'][i%3]+' '+(i+1),goal:'Review only the synthetic fixture workspace.',kind:i%2?'interval':'cron',cron:'0 9 * * *',every_minutes:60,timezone:i%2?'Asia/Shanghai':'UTC',enabled:i%4!==0,misfire:'coalesce',notify:'all'},next_at_ms:now+i*60000,next_label:'2026-07-11 09:00',created_at_ms:now}));
const snapshot=()=>({runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify([scope.space,scope.owner]),conversation_id:'fixture',cursor:'1',replies:[],running:false,agent:{phase:'ready'},messages:[]});
f.client={production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:v=>scope={...scope,...v},snapshot:async()=>snapshot(),subscribe:(cb,status)=>{queueMicrotask(()=>{status('connected');cb(snapshot());});return()=>{};},changes:cb=>{changes.add(cb);return()=>changes.delete(cb);},request:async(path,options={})=>{
 f.calls.push({path,options:structuredClone({...options,signal:undefined})});const u=new URL(path,location.href),space=options.space||scope.space;
 if(f.delay&&u.pathname.includes('/tasks/catalog'))await new Promise(r=>setTimeout(r,f.delay));
 if(f.offline&&u.pathname.includes('/tasks/catalog'))throw Error('Fixture source unavailable');
 if(u.pathname==='/api/v1/spaces')return {active_space_id:'one',items:spaces};
 if(u.pathname==='/api/v1/assistants')return space==='one'?structuredClone(f.profiles.one):[];
 if(u.pathname==='/api/v1/agent-links')return {items:space==='two'?[{id:'research',status:'connected',name:'Research node',peer:{node_id:'node-b',kind:'pot'},assistants:structuredClone(f.profiles.two)}]:[]};
 if(u.pathname==='/api/v1/permissions')return {pending:[],policy:{machines:{}}};
 if(u.pathname==='/api/v1/tasks/outcomes')return {items:[],cursor:0,has_more:false};
 if(u.pathname==='/api/v1/tasks/catalog'||u.pathname==='/api/v1/tasks/query'){
  const own=u.searchParams.get('assistant_id'),q=(u.searchParams.get('q')||'').toLowerCase(),phase=u.searchParams.get('phase'),view=u.searchParams.get('scope')||'active',offset=Number(u.searchParams.get('offset')||0),limit=Number(u.searchParams.get('limit')||50);
  let items=f.tasks.filter(t=>t.space===space&&(!own||t.assistant_id===own)&&(!q||(t.title+' '+t.id).toLowerCase().includes(q)));
  const matches=(t,v)=>v==='all'||v==='unarchived'&&!t.archived||v==='archived'&&t.archived||!t.archived&&(v==='active'?['running','queued','waiting','waiting_approval'].includes(t.phase):v==='attention'?['complete','failed','interrupted','cancelled'].includes(t.phase):t.phase===v);
  const counts=Object.fromEntries(['active','attention','archived','complete','failed','interrupted','waiting_approval'].map(v=>[v,items.filter(t=>matches(t,v)).length]));items=items.filter(t=>matches(t,view)&&(!phase||t.phase===phase));
  return {items:structuredClone(items.slice(offset,offset+limit)),total:items.length,next_offset:offset+limit<items.length?offset+limit:null,counts};
 }
 if(u.pathname==='/api/v1/schedules'){
  if(options.method==='POST'){const selected=f.schedules.find(s=>s.id===options.body?.schedule_id);if(selected){if(options.body.action==='pause')selected.spec.enabled=false;if(options.body.action==='resume')selected.spec.enabled=true;if(options.body.action==='delete')selected.deleted=true;}return {id:'saved'};}
  const items=f.schedules.filter(s=>s.space===space&&(s.assistant_id===options.owner||s.assistant_id==='missing-owner'&&options.owner===routeOwner(space,'alpha'))&&!s.deleted),selected=items.find(s=>s.id===u.searchParams.get('selected'));
  return {items:structuredClone(items),total:items.length,next_offset:null,selected,runs:selected?[{id:'run',task_id:f.tasks.find(t=>t.space===space&&t.assistant_id===options.owner)?.id,summary:'Fixture run completed',state:'complete',scheduled_label:'2026-07-10 09:00'}]:[],runs_next_offset:null};
 }
 if(u.pathname.startsWith('/api/v1/schedules/'))return {ok:true};
 const id=u.pathname.split('/')[4],task=f.tasks.find(t=>t.id===id&&t.space===space);
 if(u.pathname.endsWith('/events'))return {events:[],cursor:0,has_more:false};
 if(u.pathname.endsWith('/messages'))return options.method==='POST'?{accepted:true}:[];
 if(u.pathname.endsWith('/dispositions')){for(const d of options.body?.decisions||[]){const t=f.tasks.find(t=>t.id===d.task_id&&t.space===space);if(t)t.archived=d.archived;}return {ok:true};}
 if(task&&options.method==='POST'){
  if(u.pathname.endsWith('/cancel'))task.phase='cancelled';
  if(u.pathname.endsWith('/interrupt'))task.phase='interrupted';
  if(u.pathname.endsWith('/control'))task.paused=options.body.action==='pause';
  return {ok:true};
 }
 if(task)return {run_id:'fixture-run',info:structuredClone(task),request:{text:task.title},capabilities};
 return {items:[]};
},send:async()=>{throw Error('No model allowed in fixture');},cancel:async()=>{},markRead:async v=>v};
f.change=(domains=['tasks','schedules'])=>{for(const cb of changes)cb(domains,{space:scope.space});};
f.preference=(key,value)=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:`pot-ui-${key}`,value}}));
f.dispose=mountApp(document.querySelector('#app'),f.client);
f.ready=true;
