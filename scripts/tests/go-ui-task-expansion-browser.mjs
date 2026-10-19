// Open fixtures/task-expansion.html on a loopback static server. All API data,
// conversations and clocks are synthetic; no model, notification or service calls.
const params=new URLSearchParams(location.search);
if(params.has('frame'))mountFixture().catch(error=>{document.querySelector('#results').textContent='FAIL '+error.stack;document.querySelector('#results').style.display='block';});else mountHarness().catch(error=>document.querySelector('#results').textContent='FAIL '+error.stack);

async function mountFixture(){
  document.body.className='frame';document.documentElement.dataset.platform='browser';
  const link=document.createElement('link');link.rel='stylesheet';link.href='/web/src/styles.css';document.head.append(link);
  await new Promise(resolve=>link.onload=resolve);
  localStorage.setItem('pot-system-notifications-browser-v1','false');
  const {mountApp}=await import('../../web/src/components/ui.js');
  let scope={space:'one',owner:'alpha'},cursor=0,receive=null;
  const changes=new Set(),now=Date.now(),realNow=Date.now;
  const f=window.taskExpansionFixture={items:new Map(),calls:[],now,errors:[],extraMessages:[]};Date.now=()=>f.now;
  window.addEventListener('error',event=>f.errors.push(event.message));window.addEventListener('unhandledrejection',event=>f.errors.push(String(event.reason?.stack||event.reason)));
  f.makeTasks=(count,space=scope.space)=>Array.from({length:count},(_,index)=>({id:`task-${index}`,title:`${index+1}. ${index%2?'Review background task updates, keyboard focus and conversation return context':'核查后台任务的原地展开、手机布局、阅读位置以及详情返回状态，长标题需要限制为两行显示'}`,phase:'running',assistant_id:'alpha',started_at_ms:now-400000-index*1000,updated_at_ms:index,_space_id:space}));
  for(const [space,count]of [['one',4],['two',4]])f.items.set(space,f.makeTasks(count,space));
  const snapshot=()=>({runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify([scope.space,scope.owner]),conversation_id:scope.space,cursor:String(++cursor),running:false,turn_id:null,agent:{phase:'ready'},messages:[...Array.from({length:12},(_,index)=>({id:`old-${index}`,role:index%2?'assistant':'user',text:`Fixture history ${index+1}. `+'Only synthetic conversation data is used for checking reading positions. '.repeat(5),status:'complete',...index%2?{reply_to:`old-${index-1}`}:{}})),{id:'user',role:'user',text:'请同时处理这些后台任务。',status:'complete'},{id:'assistant',role:'assistant',reply_to:'user',text:'任务正在进行，有结果我会告诉你。',status:'complete'},...f.extraMessages]});
  const client=f.client={production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},snapshot:async()=>snapshot(),
    onNotificationClick:callback=>{f.notificationClick=callback;return()=>{f.notificationClick=null;};},
    subscribe:(onSnapshot,status)=>{receive=onSnapshot;queueMicrotask(()=>{status('connected');onSnapshot(snapshot());});return()=>{receive=null;};},
    changes:callback=>{changes.add(callback);return()=>changes.delete(callback);},
    request:async(path,options={})=>{
      f.calls.push({path,options:{space:options.space,owner:options.owner}});
      const url=new URL(path,location.href),space=options.space||scope.space,items=f.items.get(space)||[];
      if(url.pathname==='/api/v1/spaces')return {active_space_id:'one',items:[{id:'one',name:'Fixture A',control_id:'control-a',connected:true},{id:'two',name:'Fixture B',control_id:'control-b',connected:true}]};
      if(url.pathname==='/api/v1/assistants')return [{id:'alpha',name:'Pot',revision:1,reply_count:0}];
      if(url.pathname==='/api/v1/permissions')return {pending:items.filter(task=>task.phase==='waiting_approval').map(task=>({id:'approval-'+task.id,request:{owner:'alpha',task:task.id,call_id:'call',turn:'run',machine_id:'fixture',tool:'exec_command',args:{cmd:'read fixture'}}})),policy:{machines:{}}};
      if((url.pathname==='/api/v1/tasks/query'||url.pathname==='/api/v1/tasks/catalog')){
        const offset=Number(url.searchParams.get('offset')||0),limit=Number(url.searchParams.get('limit')||50);
        return {items:structuredClone(items.slice(offset,offset+limit)),total:items.length,next_offset:offset+limit<items.length?offset+limit:null,counts:{active:items.length}};
      }
      if(url.pathname==='/api/v1/tasks/outcomes')return {items:[],cursor:0,has_more:false};
      if(url.pathname==='/api/v1/schedules'){
        const plan={id:'plan',spec:{title:'Fixture schedule '+space,goal:'Fixture goal',enabled:true,kind:'daily',timezone:'UTC',time:'09:00'}};
        return {items:[plan],selected:url.searchParams.get('selected')?plan:null,runs:[{id:'run',task_id:'task-0',summary:'Fixture scheduled task',state:'complete',scheduled_label:'Yesterday'}],runs_next_offset:null};
      }
      const task=items.find(item=>url.pathname==='/api/v1/tasks/'+item.id);
      if(task)return {run_id:'run',info:task,request:{text:task.title}};
      if(url.pathname.endsWith('/events'))return {events:[],cursor:0,has_more:false};
      if(url.pathname.endsWith('/messages'))return [];
      return {items:[]};
    },send:async()=>{throw Error('Sending is disabled in this fixture');},cancel:async()=>{},markRead:async value=>value,
  };
  f.change=(topics=['tasks'])=>{for(const callback of changes)callback(topics,{space:scope.space});};
  f.setTasks=async(count,space=scope.space)=>{f.items.set(space,f.makeTasks(count,space));if(space===scope.space)f.change();await waitFor(()=>document.querySelectorAll('.background-task').length===(count?Math.min(count,document.querySelector('.background-tasks-toggle')?.getAttribute('aria-expanded')==='true'?count:3):0));await settle();};
  f.snapshot=()=>receive?.(snapshot());
  f.preference=(key,value)=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:`pot-go-preview-${key}`,value}}));
  f.dispose=mountApp(document.querySelector('#app'),client);
  f.stop=()=>{f.dispose();Date.now=realNow;};
  await waitFor(()=>document.querySelectorAll('.background-task').length===3);await document.fonts.ready;await settle();
  document.querySelector('.background-tasks').scrollIntoView({block:'end'});f.ready=true;
}

async function mountHarness(){
  const frame=document.createElement('iframe');frame.title='Isolated production chat';frame.style.width='390px';frame.src=location.pathname+'?frame=1&conversation=alpha&assistant_id=alpha&locale=zh-CN&theme=light';document.querySelector('#app').append(frame);
  const result=document.querySelector('#results'),fixture=()=>frame.contentWindow.taskExpansionFixture;
  document.querySelector('#desktop').onclick=()=>{frame.style.width='1200px';frame.style.height='680px';};
  document.querySelector('#mobile').onclick=()=>{frame.style.width='390px';frame.style.height='680px';};
  document.querySelector('#short').onclick=()=>{frame.style.width='390px';frame.style.height='360px';};
  document.querySelector('#four').onclick=()=>fixture().setTasks(4);document.querySelector('#ten').onclick=()=>fixture().setTasks(10);
  document.querySelector('#dark').onclick=()=>fixture().preference('theme',frame.contentDocument.documentElement.dataset.theme==='dark'?'light':'dark');
  document.querySelector('#language').onclick=()=>fixture().preference('locale',frame.contentDocument.documentElement.lang==='en'?'zh-CN':'en');
  document.querySelector('#run').onclick=async()=>{
    result.textContent='Running…';const completed=[];
    try{await waitFor(()=>fixture()?.ready);for(const [width,height]of [[390,680],[1200,680],[390,360]]){frame.style.width=width+'px';frame.style.height=height+'px';await new Promise(resolve=>{frame.onload=resolve;frame.src=location.pathname+`?frame=1&conversation=alpha&assistant_id=alpha&locale=zh-CN&theme=light&case=${width}-${height}`;});await waitFor(()=>fixture()?.ready);await settle();await regression(frame,completed);result.textContent=completed.join('\n');}result.textContent='PASS\n'+completed.join('\n');}
    catch(error){result.textContent='FAIL '+error.stack+'\n'+completed.join('\n')+'\n'+(fixture()?.errors||[]).join('\n');}
  };
  await waitFor(()=>fixture()?.ready);result.textContent='Ready: actual production UI, synthetic local client. Click Expand inside the frame or run regression checks.';
  if(params.has('run'))document.querySelector('#run').click();
}
function settle(){return new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));}
async function waitFor(test){const end=performance.now()+5000;while(!test()){if(performance.now()>end)throw Error('Timed out waiting for fixture state');await new Promise(resolve=>setTimeout(resolve,20));}}
function check(value,message){if(!value)throw Error(message);}
async function regression(frame,completed){
  const w=frame.contentWindow,d=frame.contentDocument,f=w.taskExpansionFixture;
  const rows=()=>[...d.querySelectorAll('.background-task')],toggle=()=>d.querySelector('.background-tasks-toggle'),history=()=>d.querySelector('.history'),key=node=>node.dataset.viewKey,top=node=>node.getBoundingClientRect().top-history().getBoundingClientRect().top;
  f.preference('locale','zh-CN');f.preference('theme','light');await settle();
  await f.setTasks(2);f.items.get('one')[0].phase='queued';f.items.get('one')[1].phase='waiting';f.change();
  await waitFor(()=>rows().length===2&&rows()[0].textContent.includes('排队中')&&rows()[1].textContent.includes('等待中'));
  check(rows().length===2,'Queued and allowance-waiting tasks remain visible after silent dispatch');
  f.preference('locale','en');await settle();check(rows()[0].textContent.includes('Queued')&&rows()[1].textContent.includes('Waiting'),'Background phases follow English locale');
  f.preference('locale','zh-CN');await settle();check(rows()[0].textContent.includes('排队中')&&rows()[1].textContent.includes('等待中'),'Background phases follow Chinese locale');
  for(const n of [0,1,3,4,10]){await f.setTasks(n);check(rows().length===Math.min(n,3),'Preview count '+n);check(!!toggle()===(n>3),'Toggle threshold '+n);check(!n||d.querySelector('.background-tasks-heading').textContent===`后台任务 · ${n}`,'Localized count '+n);}
  d.querySelector('.background-tasks').scrollIntoView({block:'end'});await settle();
  const scroll=history().scrollTop,url=w.location.href,route=JSON.stringify(w.history.state),first=rows()[0],firstY=top(first);
  toggle().focus({preventScroll:true});const toggleNode=toggle();toggle().click();await settle();
  check(rows().length===10&&toggle().getAttribute('aria-expanded')==='true','Inline expansion');
  check(w.location.href===url&&JSON.stringify(w.history.state)===route&&!history().hidden,'Expansion does not navigate');
  check(Math.abs(history().scrollTop-scroll)<2&&Math.abs(top(first)-firstY)<2,'Expansion does not jump to bottom');
  check(toggle()===toggleNode&&d.activeElement===toggleNode,'Expansion retains control and keyboard focus');
  const computed=w.getComputedStyle(toggle());check(computed.textDecorationLine==='none'&&computed.color===w.getComputedStyle(d.querySelector('.background-tasks-heading')).color,'Neutral toggle style');
  check(toggle().getBoundingClientRect().height>=44,'Touch height');check(d.getElementById(toggle().getAttribute('aria-controls')),'Accessible controlled list');
  for(const row of rows()){const title=row.querySelector('.background-task-title'),meta=row.querySelector('.background-task-meta');check(title.getBoundingClientRect().height<=41,'Title is at most two lines');check(meta.getBoundingClientRect().top>=title.getBoundingClientRect().bottom,'Metadata below title');}
  check(d.documentElement.scrollWidth<=w.innerWidth,'No page overflow');check(!['auto','scroll'].includes(w.getComputedStyle(d.querySelector('.background-task-list')).overflowY),'No nested scrolling');
  f.extraMessages.push({id:'followup-user',role:'user',text:'Keep working on the background tasks.',status:'complete'},{id:'followup-assistant',role:'assistant',reply_to:'followup-user',text:'The background tasks are still running.',status:'complete'});f.snapshot();await settle();
  check(rows().length===10&&rows()[0]===first&&toggle()===toggleNode&&d.activeElement===toggleNode,'A new reply retains expansion, keyed rows and focus');
  const order=rows().map(key),nodes=new Map(rows().map(row=>[key(row),row]));
  f.items.get('one').reverse();f.change();await settle();check(rows().every((row,i)=>key(row)===order[i]&&row===nodes.get(key(row))),'Refresh preserves order and DOM');
  const reads=f.calls.length,timeNode=rows()[0].querySelector('[role=timer]'),oldTime=timeNode.textContent;f.now+=5000;await waitFor(()=>timeNode.textContent!==oldTime);await settle();
  check(timeNode.textContent!==oldTime&&f.calls.length===reads&&rows()[0]===first,'Clock only updates text');
  const anchor=rows()[5];history().scrollTop+=top(anchor)-20;await settle();const anchorY=top(anchor);
  f.items.set('one',f.items.get('one').filter(task=>!['task-0','task-1'].includes(task.id)));f.change();await waitFor(()=>rows().length===8);await settle();
  check(anchor.isConnected&&Math.abs(top(anchor)-anchorY)<2,`Task removal preserves visible row anchor: connected=${anchor.isConnected} before=${anchorY} after=${top(anchor)} top=${history().scrollTop} max=${history().scrollHeight-history().clientHeight}`);
  f.items.get('one').push({...f.makeTasks(11)[10]});f.change();await waitFor(()=>rows().length===9);check(rows().some(row=>row.title.startsWith('11.')),'New tasks appear while expanded');
  f.items.get('one').find(task=>task.id==='task-9').phase='waiting_approval';f.change(['tasks','permissions']);await waitFor(()=>d.querySelector('.background-task.needs-approval'));await settle();
  check(rows()[0].classList.contains('needs-approval')&&rows()[0].title.startsWith('10.')&&rows()[0].textContent.includes('等待授权')&&rows()[0].querySelector('[role=timer]'),'Approval priority, highlight and clock');
  const approval=d.querySelector('.chat-approvals .approval-card');check(approval,'Existing approval card remains available');approval.open=true;f.change(['tasks','permissions']);await settle();check(approval.isConnected&&approval.open,'Approval disclosure survives task refresh');
  check(rows().every(row=>row===nodes.get(key(row))||row.title.startsWith('11.')),'Status refresh keeps keyed rows');
  await f.setTasks(10);f.change(['permissions']);await waitFor(()=>!d.querySelector('.background-task.needs-approval'));await settle();
  const detailAnchor=rows()[4];history().scrollTop+=top(detailAnchor)-20;await settle();const detailY=top(detailAnchor),selected=key(detailAnchor);
  detailAnchor.click();await waitFor(()=>d.querySelector('.task-page-host'));await settle();
  check(f.calls.some(call=>call.path==='/api/v1/tasks/'+JSON.parse(selected)[2]&&call.options.space==='one'&&call.options.owner==='alpha'),'Detail uses original task ownership');
  const back=[...d.querySelectorAll('.titlebar button')].find(button=>button.getAttribute('aria-label')==='返回');check(back,'Detail Back');back.click();await waitFor(()=>!history().hidden&&rows().length===10);await settle();
  const returned=rows().find(row=>key(row)===selected);check(toggle().getAttribute('aria-expanded')==='true'&&Math.abs(top(returned)-detailY)<2,'Detail return restores expansion and anchor');
  d.dispatchEvent(new KeyboardEvent('keydown',{key:',',ctrlKey:true,bubbles:true}));await waitFor(()=>history().hidden);w.history.back();await waitFor(()=>!history().hidden);await settle();
  check(toggle().getAttribute('aria-expanded')==='true'&&Math.abs(top(returned)-detailY)<2,'Settings return restores expansion and anchor');
  d.querySelector('.assistant-row[data-view-key=\'["two","alpha"]\']').click();await waitFor(()=>f.client.scope().space==='two'&&rows().length===3);await settle();
  check(toggle().getAttribute('aria-expanded')==='false','Other Control/Space starts collapsed');toggle().click();await settle();check(rows().length===4,'Other Space expands independently');rows()[0].click();await waitFor(()=>d.querySelector('.task-page-host'));await settle();
  check(f.calls.some(call=>call.path==='/api/v1/tasks/task-0'&&call.options.space==='two'&&call.options.owner==='alpha'),'Same ID opens the correct Space');[...d.querySelectorAll('.titlebar button')].find(button=>button.getAttribute('aria-label')==='返回').click();await waitFor(()=>!history().hidden);await settle();
  d.querySelector('.assistant-row[data-view-key=\'["one","alpha"]\']').click();await waitFor(()=>f.client.scope().space==='one'&&rows().length===10);await settle();check(toggle().getAttribute('aria-expanded')==='true','Original expansion survives conversation switch');
  await f.setTasks(3);check(!toggle(),'Three tasks hide control');await f.setTasks(10);check(rows().length===10,'Temporary threshold decrease retains expansion');
  const hidden=rows()[7];history().scrollTop+=top(hidden)-20;await settle();toggle().click();await settle();check(rows().length===3,'Collapse restores preview');check(top(d.querySelector('.background-tasks-heading'))>=-1,'Collapse anchors at region when visible rows are hidden');
  await f.setTasks(0);await f.setTasks(4);check(rows().length===3&&toggle().getAttribute('aria-expanded')==='false','Empty region resets expansion');
  f.items.get('one')[0].phase='complete';f.change();await waitFor(()=>d.querySelector('.background-tasks-heading')?.textContent==='后台任务 · 3');check(rows().length===3&&!toggle(),'Finished tasks leave the active preview');
  await f.setTasks(51);check(d.querySelector('.background-tasks-heading').textContent==='后台任务 · 51','All active query pages counted');toggle().click();await settle();check(rows().length===51,'All active query pages expand');toggle().click();await settle();
  f.preference('locale','en');f.preference('theme','dark');await settle();check(toggle().textContent.includes('Expand 48 more tasks')&&d.querySelector('.background-tasks-heading').textContent==='Background tasks · 51','Live English translation');
  check(w.getComputedStyle(toggle()).textDecorationLine==='none'&&d.documentElement.dataset.theme==='dark','Dark theme');
  await f.setTasks(0);history().scrollTop=history().scrollHeight;f.extraMessages.push({id:'last-user',role:'user',text:'New turn',status:'complete'},{id:'last-assistant',role:'assistant',reply_to:'last-user',text:'A new live response should still follow when the reader is at the bottom. '.repeat(20),status:'complete'});f.snapshot();await settle();check(history().scrollHeight-history().clientHeight-history().scrollTop<2,'Ordinary new replies still follow across coalesced renders');
  await f.setTasks(4);d.querySelector('.background-tasks').scrollIntoView({block:'end'});await settle();
  await taskNavigationRegression(w,d,f);
  check(f.errors.length===0,'No page errors: '+f.errors.join('; '));completed.push(`${w.innerWidth}×${w.innerHeight}: counts, expansion, focus, clocks, refresh, additions/removals, anchors, details/settings, ownership, pagination, en/zh, dark, task entry/return rules PASS`);
}

async function taskNavigationRegression(w,d,f){
  const back=()=>d.querySelector('.titlebar button[aria-label="Back"]');
  const chat=()=>!d.querySelector('.history').hidden&&!d.querySelector('.pot-app').classList.contains('show-directory');
  const clickText=(selector,text)=>{const button=[...d.querySelectorAll(selector)].find(node=>node.textContent===text);check(button,'Missing '+text);button.click();};
  await f.setTasks(51);
  const draft=d.querySelector('#pot-composer');draft.value='Retain assistant draft';draft.dispatchEvent(new w.Event('input',{bubbles:true}));
  d.querySelector('.rail-button[aria-label="Tasks"]').click();await waitFor(()=>d.querySelector('.task-list .task-row'));
  const list=d.querySelector('.task-list');list.scrollTop=80;const scroll=list.scrollTop;check(scroll>0,'List return exercises a nonzero scroll position');
  d.querySelector('.task-row').click();await waitFor(()=>d.querySelector('.task-page-host'));
  check(w.history.state.potTaskReturn.route==='tasks','Only list entry captures a Tasks return');
  f.preference('locale','zh-CN');await settle();await waitFor(()=>d.querySelector('.task-page-host'));
  f.preference('locale','en');await settle();await waitFor(()=>d.querySelector('.task-page-host'));
  check(w.history.state.potTaskReturn.route==='tasks','Locale changes retain list return');
  back().click();await waitFor(()=>d.querySelector('.task-list'));await settle();
  check(d.querySelector('.task-list').scrollTop===scroll&&d.querySelector('.history').hidden,'List entry returns to list and restores scroll');
  clickText('.tasks-content>.segments button','Schedules');await waitFor(()=>[...d.querySelectorAll('.schedule-card button')].some(node=>node.textContent==='Fixture schedule two'));
  clickText('.schedule-card button','Fixture schedule two');await waitFor(()=>[...d.querySelectorAll('button')].some(node=>node.textContent==='View task'));
  clickText('button','View task');await waitFor(()=>d.querySelector('.task-page-host'));
  check(w.history.state.potTaskReturn.scope?.space==='two','Schedule entry captures task owner Space');
  d.dispatchEvent(new w.KeyboardEvent('keydown',{key:'[',metaKey:true,bubbles:true}));await waitFor(()=>chat()&&f.client.scope().space==='two');await settle();
  check(d.querySelector('#pot-composer').value==='','Schedule task returns to its owner, without another Space draft');
  f.notificationClick({space:'one',owner:'alpha',control:'control-a',task:'task-0',name:'Notification task',kind:'approval'});await waitFor(()=>d.querySelector('.task-page-host'));
  const taskState=structuredClone(w.history.state);
  check(taskState.potTaskReturn.route==='chat'&&taskState.potTaskReturn.scope.space==='one','Notification entry captures task owner instead of prior list context');
  // Exercise browser history restoration of the explicit task return destination.
  d.querySelector('.rail-button[aria-label="Tasks"]').click();await waitFor(()=>d.querySelector('.tasks-content'));
  w.history.back();await waitFor(()=>d.querySelector('.task-page-host'));await settle();
  check(JSON.stringify(w.history.state.potTaskReturn)===JSON.stringify(taskState.potTaskReturn),'History restores the task return destination');
  back().click();await waitFor(()=>chat()&&f.client.scope().space==='one');await settle();
  check(d.querySelector('#pot-composer').value==='Retain assistant draft','Notification Back restores owner conversation and draft');
}
