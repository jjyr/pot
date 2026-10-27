const params=new URLSearchParams(location.search),before=params.has('before'),source=before?'/output/task-status-before':'';
const css=document.createElement('link');css.rel='stylesheet';css.href=source+'/web/src/styles.css';document.head.append(css);
const {createBackgroundTasks}=await import(source+'/web/src/components/background-tasks.mjs');
const {bindTaskClock}=await import(source+'/web/src/components/task-time.mjs');
const {createResourceReads}=await import('/web/src/state/resource-cache.mjs');
const {createTasksDomain}=await import('/web/src/state/domains/tasks.mjs');
let locale=params.get('locale')||'zh-CN',now=1800000000000;Date.now=()=>now;
document.documentElement.lang=locale;document.documentElement.dataset.theme=params.get('theme')||'light';
const tasks=[
 {id:'6061b829-ae29-4e52-b5ce-c280847a1234',title:'Review message positioning',phase:'running',started_at_ms:now-138000},
 {id:'b16981fa-e96c-4bec-bace-f3f4e5479be8',title:'Wait for review input',phase:'waiting',started_at_ms:now-65000},
 {id:'c25abc79-83f3-48f6-9df9-9e5481f739e9',title:'Completed checks',phase:'complete',started_at_ms:now-90000,finished_at_ms:now-20000},
].map(task=>({...task,assistant_id:'synthetic-owner',_space_id:'synthetic-space',address:{node_id:'synthetic-node',kind:'task',agent_id:task.id}}));
let approval=false,opened;
const host=document.querySelector('#tasks'),view=createBackgroundTasks({locale:()=>locale,onOpen:task=>opened=task,onToggle:()=>render()});
const clock=bindTaskClock(host,{locale:()=>locale,active:()=>true});
const render=(rows=tasks)=>{view.update('synthetic',rows,()=>approval);if(!view.element.isConnected)host.append(view.element);clock.refresh();};
const read=()=>[...host.querySelectorAll('.background-task')].map(row=>({status:row.querySelector('.background-task-status').textContent,phase:row.querySelector('.task-activity-dot').dataset.phase,time:row.querySelector('.background-task-elapsed')?.textContent||'',fixed:row.querySelector('.background-task-elapsed')?.dataset.finishedAt}));
const assert=(value,message)=>{if(!value)throw Error(message);checks.push(message);},checks=[];
function retain(){render(tasks.map(task=>({...task,_stale:true})));}
function confirm(phase){tasks[0]={...tasks[0],phase,finished_at_ms:phase==='complete'?now:undefined};render();}
render();
try{
 if(!before){
  const initial=read();retain();assert(JSON.stringify(read().map(({status,phase,time})=>({status,phase,time})))===JSON.stringify(initial.map(({status,phase,time})=>({status,phase,time}))),'retained phase, dot and elapsed stay visible');
  now+=90000;retain();assert(read()[0].time===initial[0].time,'retained running clock does not grow');
  render(tasks.map(task=>({...task,phase:'queued',_stale:true})));assert(read()[0].phase==='running','older retained payload cannot roll a confirmed phase back');
  host.querySelector('.background-task').click();assert(opened._stale&&opened.phase==='queued','navigation receives original retained record, not display authority');
  confirm('waiting');assert(read()[0].phase==='waiting'&&!read()[0].fixed,'new confirmed phase updates and clock resumes');
  approval=true;render();retain();approval=false;retain();assert(read()[0].phase==='waiting_approval','retained approval presentation survives temporary capability loss');approval=false;
  confirm('complete');const end=read()[0].time;now+=90000;retain();assert(read()[0].phase==='complete'&&read()[0].time===end,'terminal receipt duration remains fixed');
  const cold=createBackgroundTasks({locale:()=>locale,onOpen:()=>{},onToggle:()=>{}});
  cold.update('cold',[{...tasks[1],_stale:true}],()=>false);assert(cold.element.querySelector('.task-activity-dot').dataset.phase==='waiting'&&!cold.element.querySelector('.background-task-elapsed'),'cold retained phase has no invented active duration');
  cold.update('unknown',[{id:'unknown',_stale:true}],()=>false);assert(cold.element.querySelector('.task-activity-dot').dataset.phase==='unknown'&&!cold.element.querySelector('.background-task-elapsed'),'unknown phase does not default to running');
  const scope={space:'synthetic-space',owner:'synthetic-owner'};let release,held=false,phase='running';
  const client={runtime:{runtime_id:'synthetic-runtime'},spaces:[],scope:()=>scope,transport:async()=>{if(held)await new Promise(resolve=>release=resolve);return {items:[{...tasks[1],phase}],next_offset:null};}};
  const resources=createResourceReads(client,{storage:null}),queries={define:(_key,load)=>({load})},domain=createTasksDomain(client,{resources,queries,commands:{}}),active=domain.active(scope);
  let result=await active.load(false);view.update('domain',result.data,()=>false);assert(result.freshness==='fresh','initial domain state is fresh');
  held=true;await resources.invalidate(['tasks'],scope);result=await active.load(false);view.update('domain',result.data,()=>false);assert(result.data[0]._stale&&view.element.querySelector('.task-activity-dot').dataset.phase==='running','real cache invalidation retains running phase');
  phase='waiting';held=false;release();await new Promise(resolve=>setTimeout(resolve,0));result=await active.load(true);view.update('domain',result.data,()=>false);assert(!result.data[0]._stale&&view.element.querySelector('.task-activity-dot').dataset.phase==='waiting','fresh domain result changes phase');domain.dispose();resources.clear();
  // Restore equivalent screenshot data and exercise locale changes while retained.
  now=1800000000000;tasks[0]={...tasks[0],phase:'running',finished_at_ms:undefined};render();retain();const savedLocale=locale;locale=locale==='en'?'zh-CN':'en';retain();assert(!host.textContent.includes('tasks.')&&!host.textContent.includes('已保存状态')&&!host.textContent.includes('Saved state'),'retained locale switch uses translated phase');locale=savedLocale;
 }
 now=1800000000000;tasks[0]={...tasks[0],phase:'running',finished_at_ms:undefined};render();retain();clock.dispose();
 document.querySelector('#results').textContent='PASS '+checks.length+' checks\n'+checks.join('\n');
 window.taskStatus={read,checks,retain,confirm,advance:ms=>{now+=ms;clock.refresh();},render};
 document.querySelector('#retain').onclick=retain;document.querySelector('#waiting').onclick=()=>confirm('waiting');document.querySelector('#complete').onclick=()=>confirm('complete');
}catch(error){document.querySelector('#results').textContent='FAIL '+error.stack;throw error;}
