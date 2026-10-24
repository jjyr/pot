// Production rows and the same shared clock used by Chat. Synthetic data only.
const params=new URLSearchParams(location.search),before=params.has('before');
const source=before?(params.has('alignment')?'/output/task-alignment-before':'/output/task-time-before'):'';
const css=document.createElement('link');css.rel='stylesheet';css.href=source+'/web/src/styles.css';document.head.prepend(css);
const {createBackgroundTasks}=await import(source+'/web/src/components/background-tasks.mjs');
const {bindTaskClock,taskElapsed}=await import(source+'/web/src/components/task-time.mjs');
let locale=params.get('locale')||'zh-CN';document.documentElement.lang=locale;
document.documentElement.dataset.theme=params.get('theme')||'light';
const realNow=Date.now;let now=Date.UTC(2026,9,18,6);Date.now=()=>now;
const started=now-138000;
const tasks=[
 {id:'6061b829-ae29-4e52-b5ce-c280847a1234',title:'核查聊天后台任务的耗时与窄屏布局 · Review elapsed time in long background task titles',phase:'running',started_at_ms:started,assistant_id:'fixture'},
 {id:'b16981fa-e96c-4bec-bace-f3f4e5479be8',title:'Queued work has not started yet',phase:'queued',created_at_ms:now-900000,started_at_ms:0,assistant_id:'fixture'},
 {id:'c25abc79-83f3-48f6-9df9-9e5481f739e9',title:'Waiting after execution started',phase:'waiting',started_at_ms:now-750000,assistant_id:'fixture'},
 {id:'d3618b5a-6722-4f38-bdf4-cf269db87e83',title:'Finished duration stays frozen',phase:'complete',started_at_ms:started,finished_at_ms:started+65000,assistant_id:'fixture'},
];
for(const task of tasks)task.address={node_id:'synthetic-receiver',kind:'task',agent_id:task.id};
const fixture=document.querySelector('#fixture'),result=document.querySelector('#result'),views=[];
let active=true,approval=false;
const widths=params.has('width')?[Number(params.get('width'))]:[720,390,320];
for(const width of widths){
 const host=document.createElement('section');host.className='probe';host.style.width=width+'px';host.setAttribute('aria-label',width+'px container');
 const view=createBackgroundTasks({locale:()=>locale,onOpen:()=>{},onToggle:()=>render()});
 const clock=bindTaskClock(host,{locale:()=>locale,active:()=>active});
 function render(){view.update('synthetic',tasks,id=>approval&&id===tasks[0].id);if(!view.element.isConnected)host.append(view.element);clock.refresh();}
 fixture.append(host);render();views.push({host,view,clock,render,width});
}
const failures=[],checks=[];
const assert=(value,label)=>{if(!value)throw Error(label);checks.push(label);};
const row=v=>v.host.querySelector('.background-task'),timer=v=>row(v).querySelector('.background-task-elapsed');
const statusPositions=v=>[...v.host.querySelectorAll('.background-task')].map(button=>button.querySelector('.background-task-status').getBoundingClientRect().x);
const checkLayout=v=>{
 const positions=statusPositions(v),indicators=[...v.host.querySelectorAll('.task-activity-dot')].map(dot=>dot.offsetLeft);
 assert(positions.every(x=>Math.abs(x-positions[0])<0.5),`${v.width}px: all status labels start in one column`);
 assert(indicators.every(x=>Math.abs(x-indicators[0])<0.5),`${v.width}px: status indicators share an untransformed layout origin`);
 const times=[...v.host.querySelectorAll('.background-task-elapsed')].map(time=>time.getBoundingClientRect().x);
 assert(times.every(x=>Math.abs(x-times[0])<0.5),`${v.width}px: elapsed labels share a separate column`);
 for(const button of v.host.querySelectorAll('.background-task')){
  const meta=button.querySelector('.background-task-meta'),short=button.querySelector('.task-identity-short'),title=button.querySelector('.background-task-title'),box=button.getBoundingClientRect();
  assert(box.height>=44&&button.scrollWidth<=button.clientWidth,`${v.width}px: 44px target and row fit`);
  assert(meta.getBoundingClientRect().right<=box.right&&short.getBoundingClientRect().left>=box.left,`${v.width}px: identity and metadata fit`);
  assert(short.getBoundingClientRect().left>=button.querySelector('.task-identity-mark').getBoundingClientRect().right,`${v.width}px: icon and ID never overlap`);
  assert(title.getBoundingClientRect().right<=meta.getBoundingClientRect().left,`${v.width}px: title truncates before metadata`);
  for(const part of [...meta.children])assert(getComputedStyle(part).visibility!=='hidden'&&part.getBoundingClientRect().width>0,`${v.width}px: status/time visible`);
 }
};
try{
 await document.fonts.ready;await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
 if(!before){
  for(const v of views){
   assert(timer(v)?.textContent==='2分18秒'||timer(v)?.textContent==='2m 18s','running duration is visible and compact');
   assert(!v.host.querySelectorAll('.background-task')[1].querySelector('.background-task-elapsed'),'initial queue omits elapsed time');checkLayout(v);
  }
  const v=views[0],button=row(v),elapsed=timer(v),order=[...v.host.querySelectorAll('.background-task')],y=button.getBoundingClientRect().y;
  button.focus({preventScroll:true});const scrollY=window.scrollY;
  const metaX=button.querySelector('.background-task-meta').getBoundingClientRect().x,tickPositions=views.map(statusPositions);
  now+=1000; // Let the actual shared timeout tick, without a render/refresh call.
  await new Promise(resolve=>setTimeout(resolve,1100));
  assert(elapsed.textContent===taskElapsed(started,now,locale,true),'shared scheduled tick advances the running clock');
  assert(button===row(v)&&elapsed===timer(v)&&document.activeElement===button&&window.scrollY===scrollY&&button.getBoundingClientRect().y===y,'tick retains row, timer, focus and scroll');
  now+=10000;views.forEach(v=>v.clock.refresh());
  assert(button.querySelector('.background-task-meta').getBoundingClientRect().x===metaX,'normal second changes keep metadata width stable');
  assert(views.every((v,i)=>statusPositions(v).every((x,j)=>x===tickPositions[i][j])),'scheduled seconds updates never move status text');
  views.forEach(checkLayout);
  tasks[0].title+=' · renamed';views.forEach(v=>v.render());
  assert(button===row(v)&&elapsed===timer(v)&&document.activeElement===button&&order.every((node,i)=>v.host.querySelectorAll('.background-task')[i]===node),'task update preserves keyed DOM, order and focus');
  for(const phase of ['complete','failed','cancelled','interrupted']){
   tasks[0].phase=phase;tasks[0].finished_at_ms=started+138000;views.forEach(v=>v.render());
   const frozen=timer(v).textContent;now+=90000;views.forEach(v=>v.clock.refresh());
   assert(timer(v).textContent===frozen,phase+' freezes at authoritative finish');
  }
  tasks[0].finished_at_ms=0;views.forEach(v=>v.render());assert(!timer(v),'unknown terminal time omits clock');views.forEach(checkLayout);
  tasks[0].phase='running';tasks[0].started_at_ms=0;views.forEach(v=>v.render());assert(!timer(v),'unknown start omits clock');views.forEach(checkLayout);
  tasks[0].started_at_ms=now+1000;views.forEach(v=>v.render());assert(timer(v).textContent===taskElapsed(now+1000,now,locale,true),'inverted clocks clamp to zero');
  tasks[0].started_at_ms=started;tasks[0].phase='waiting';views.forEach(v=>v.render());
  assert(timer(v).textContent===taskElapsed(started,now,locale,true),'later waits retain wall-time semantics');
  tasks[0].phase='running';tasks[0].started_at_ms=now-183600000;approval=true;views.forEach(v=>v.render());views.forEach(checkLayout);
  assert(timer(v).textContent===taskElapsed(tasks[0].started_at_ms,now,locale,true),'approval retains started wall time and day formatting');
  approval=false;tasks[0].started_at_ms=started;views.forEach(v=>v.render());
  const hiddenBefore=Object.getOwnPropertyDescriptor(document,'hidden');
  Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'));
  const hiddenText=timer(v).textContent;now+=10000;views.forEach(v=>v.clock.refresh());assert(timer(v).textContent===hiddenText,'hidden document stops ticking');
  if(hiddenBefore)Object.defineProperty(document,'hidden',hiddenBefore);else delete document.hidden;
  document.dispatchEvent(new Event('visibilitychange'));assert(timer(v).textContent===taskElapsed(started,now,locale,true),'visibility restoration refreshes the clock');
  active=false;const paused=timer(v).textContent;now+=10000;views.forEach(v=>v.clock.refresh());assert(timer(v).textContent===paused,'inactive view stops ticking');active=true;
  locale=locale==='en'?'zh-CN':'en';document.documentElement.lang=locale;views.forEach(v=>v.render());
  assert(timer(v).textContent===taskElapsed(started,now,locale,true),'mounted locale switch uses shared catalog');
  views[0].host.querySelector('.background-tasks-toggle').click();assert(views[0].host.querySelectorAll('.background-task').length===4,'expansion retained');
  views[0].render();assert(views[0].host.querySelectorAll('.background-task').length===4,'update retains expansion');
  views.forEach(checkLayout);
  // Different short/long durations, statuses and missing timers must not shift
  // individual rows; the entire time column stays reserved when all are absent.
  const saved=tasks.map(task=>({...task}));
  tasks[0].phase='running';tasks[0].started_at_ms=now-59000;
  tasks[1].phase='queued';tasks[1].started_at_ms=0;
  tasks[2].phase='waiting';tasks[2].started_at_ms=now-183600000;
  views.forEach(v=>v.render());views.forEach(checkLayout);
  const boundaryPositions=views.map(statusPositions);
  now+=1000;views.forEach(v=>v.clock.refresh());
  assert(views.every((v,i)=>statusPositions(v).every((x,j)=>x===boundaryPositions[i][j])),'minute rollover never moves status text');
  tasks.forEach(task=>{task.started_at_ms=0;});views.forEach(v=>v.render());views.forEach(checkLayout);
  assert(views.every((v,i)=>statusPositions(v).every((x,j)=>x===boundaryPositions[i][j])),'absent elapsed labels retain the reserved time column');
  tasks.forEach((task,i)=>Object.assign(task,saved[i]));
  // Restore equal capture state; stop clocks so before/after images match.
  now=started+138000;locale=params.get('locale')||'zh-CN';document.documentElement.lang=locale;
  tasks[0].phase='running';tasks[0].finished_at_ms=0;tasks[0].started_at_ms=started;
  tasks[0].title='核查聊天后台任务的耗时与窄屏布局 · Review elapsed time in long background task titles';
  views.forEach(v=>v.render());button.blur();
 }
 if(params.has('capture'))for(const v of views)if(v.host.querySelector('.background-tasks-toggle')?.getAttribute('aria-expanded')==='false')v.host.querySelector('.background-tasks-toggle').click();
 assert(document.documentElement.scrollWidth<=innerWidth,'fixture fits actual viewport');
 result.textContent=(before?'BASELINE':`PASS ${checks.length} assertions`)+` · ${innerWidth}px viewport; ${widths.join('/')}px containers; ${locale}; ${document.documentElement.dataset.theme}`;
}catch(error){failures.push(error.message);result.textContent='FAIL: '+error.stack;}
finally{views.forEach(v=>v.clock.dispose());}
const disposedText=timer(views[0])?.textContent,capturedNow=now;now+=10000;views.forEach(v=>v.clock.refresh());if(timer(views[0])?.textContent!==disposedText)failures.push('disposed clock refreshed');now=capturedNow;
addEventListener('pagehide',()=>{Date.now=realNow;},{once:true});
window.backgroundTaskTime={checks,failures,views};
