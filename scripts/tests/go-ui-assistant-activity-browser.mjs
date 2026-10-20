// Shared production DOM with synthetic catalogue/SSE/read boundaries. No model
// calls or product data; backend execution projection is covered by Go tests.
const params=new URLSearchParams(location.search);
const until=async test=>{for(let i=0;i<250;i++){if(test())return;await new Promise(resolve=>setTimeout(resolve,20));}throw Error('Timed out: '+test);};
const paint=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
if(params.has('frame'))await mount();else harness();

async function mount(){
  document.body.className='frame';
  const base=params.has('baseline')?'/output/pr-images/baseline/web/src':'/web/src';
  const css=document.createElement('link');css.rel='stylesheet';css.href=base+'/styles.css';document.head.append(css);await new Promise(resolve=>css.onload=resolve);
  const {mountApp}=await import(base+'/components/ui.js');
  const f=window.activityFixture={scope:{space:'one',owner:'main'},errors:[],calls:[],cursor:0,readPending:false,readRequests:[]};
  window.addEventListener('error',event=>f.errors.push(event.message));window.addEventListener('unhandledrejection',event=>f.errors.push(String(event.reason?.stack||event.reason)));
  f.items={one:[{id:'main',name:'Pot',phase:'ready',reply_count:0},{id:'research',name:'研究助手',phase:'ready',reply_count:0,executing:true},{id:'code',name:'代码助手',phase:'ready',reply_count:1,reply_id:'code-reply'},{id:'long',name:'拥有很长名字的研究与开发助手',phase:'ready',reply_count:0,executing:true}],two:[{id:'research',name:'远程研究助手',phase:'ready',reply_count:0}]};
  let receive,readListener;const changes=new Set();
  const snapshot=()=>({runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify([f.scope.space,f.scope.owner]),conversation_id:f.scope.owner,cursor:String(++f.cursor),running:false,turn_id:null,agent:{phase:'ready'},messages:f.scope.owner==='research'?[{id:'input',role:'user',text:'整理一下这次调研的结论。',status:'complete'},{id:'research-reply',reply_to:'input',role:'assistant',text:'调研已完成，结论和下一步建议已整理好。',status:'complete'}]:[{id:'intro',role:'assistant',text:'帮我整理调研资料，并检查项目中的待办事项。',status:'complete'}]});
  const client=f.client={production:true,desktop:true,runtime:{runtime_id:'fixture',boot_id:'boot'},readBaselines:[{control_id:'control-one',owner:'code',reply_count:0}],
    scope:()=>({...f.scope}),setScope:value=>Object.assign(f.scope,value),snapshot:async()=>snapshot(),
    subscribe:(callback,status)=>{receive=callback;queueMicrotask(()=>{status('connected');callback(snapshot());});return()=>{receive=null;};},
    changes:callback=>{changes.add(callback);return()=>changes.delete(callback);},
    onRead:callback=>{readListener=callback;return()=>{readListener=null;};},
    markRead:async value=>{f.readRequests.push(value);if(f.readPending)await new Promise(resolve=>f.resolveRead=resolve);return {control_id:'control-'+value.space_id,owner:value.owner,reply_count:value.reply_count,reply_id:value.reply_id};},
    notificationView:async()=>{},notificationStatus:async()=>({enabled:false,permission:'denied'}),notificationReady:async()=>{},setReducedMotion:async()=>{},
    request:async(path,options={})=>{
      f.calls.push({path,space:options.space});const name=new URL(path,location.href).pathname;
      if(name==='/api/v1/spaces')return {active_space_id:'one',items:[{id:'one',name:'本机',control_id:'control-one',kind:'local',connected:true},{id:'two',name:'远程',control_id:'control-two',kind:'remote',connected:true}]};
      if(name==='/api/v1/assistants')return structuredClone(f.items[options.space||f.scope.space]);
      if(name==='/api/v1/permissions')return {pending:[],policy:{machines:{}}};
      if(name==='/api/v1/providers')return {profiles:[],connections:[],directory:[]};
      return {items:[],counts:{},total:0,cursor:0,has_more:false};
    },send:async()=>{throw Error('No model calls in activity fixture');},cancel:async()=>{},
  };
  f.change=(topics=['tasks'],space='one')=>{for(const callback of changes)callback(topics,{space});};
  f.update=(value,topics=['tasks'],space='one')=>{Object.assign(f.items[space].find(item=>item.id==='research'),value);f.change(topics,space);};
  f.preference=(key,value)=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-'+key,value}}));
  f.readElsewhere=()=>readListener?.({control_id:'control-one',owner:'research',reply_count:1,reply_id:'research-reply'});
  f.publish=()=>receive?.(snapshot());
  f.scenario=state=>{if(state==='cooking')f.update({executing:true,reply_count:0,reply_id:''});else if(state==='ready')f.update({executing:false,reply_count:1,reply_id:'research-reply'});else f.readElsewhere();};
  f.dispose=mountApp(document.querySelector('#app'),client);
  window.addEventListener('pagehide',f.dispose);
  await until(()=>document.querySelectorAll('.assistant-row').length===5);await document.fonts.ready;await paint();f.ready=true;
}

function harness(){
  const frame=document.createElement('iframe');frame.title='Production sidebar with synthetic work';document.querySelector('#app').append(frame);
  frame.src=location.pathname+'?frame=1&conversation=main&locale=zh-CN&theme=light'+(params.has('baseline')?'&baseline=1':'');
  const result=document.querySelector('#result'),fixture=()=>frame.contentWindow.activityFixture;
  document.querySelector('#desktop').onclick=()=>{frame.style.width='1200px';};document.querySelector('#mobile').onclick=()=>{frame.style.width='390px';};
  for(const state of ['cooking','ready','read'])document.querySelector('#'+state).onclick=()=>fixture().scenario(state);
  document.querySelector('#theme').onclick=()=>fixture().preference('theme',frame.contentDocument.documentElement.dataset.theme==='dark'?'light':'dark');
  document.querySelector('#locale').onclick=()=>fixture().preference('locale',frame.contentDocument.documentElement.lang==='en'?'zh-CN':'en');
  document.querySelector('#run').onclick=async()=>{
    result.textContent='Running…';const checks=[];
    try{
      for(const width of [1200,390]){
        frame.style.width=width+'px';
        const loaded=new Promise(resolve=>frame.onload=resolve);frame.src=location.pathname+`?frame=1&conversation=main&locale=zh-CN&theme=light&case=${width}`;await loaded;await until(()=>fixture()?.ready);
        await regression(frame,checks);result.textContent=checks.join('\n');
      }
      result.textContent='PASS\n'+checks.join('\n');
    }catch(error){result.textContent='FAIL '+error.stack+'\n'+checks.join('\n')+'\n'+fixture()?.errors.join('\n');}
  };
  until(()=>fixture()?.ready).then(()=>{result.textContent='Ready — real shared UI; synthetic work, no model calls.';if(params.has('run'))document.querySelector('#run').click();});
}

async function regression(frame,checks){
  const win=frame.contentWindow,doc=frame.contentDocument,f=win.activityFixture,width=win.innerWidth;
  const check=(value,message)=>{if(!value)throw Error(`${width}: ${message}`);checks.push(`${width}: ${message}`);};
  const row=(owner,space='one')=>[...doc.querySelectorAll('.assistant-row')].find(node=>node.dataset.viewKey===JSON.stringify([space,owner]));
  const indicator=(owner='research',space='one')=>row(owner,space)?.querySelector('.assistant-activity');
  const state=()=>indicator()?.dataset.state||'';
  if(width<720){
    const back=[...doc.querySelectorAll('button')].find(node=>/返回助手|Back to Assistants/.test(node.getAttribute('aria-label')||''));back?.click();await paint();
  }
  const initial=indicator();
  check(state()==='cooking','background work shows cooking after the name');
  check(!indicator('research','two'),'same assistant ID in another Control stays idle');
  check(indicator('code')?.dataset.state==='ready','existing native unread baseline shows a ready Pot');
  check(win.getComputedStyle(initial.querySelector('.activity-pot-rig')).animationName==='pot-boil','running pot rocks');
  check(win.getComputedStyle(initial.querySelector('.activity-pot-lid')).animationName==='pot-lid-rattle','lid rattles independently');
  const steam=initial.querySelector('.activity-pot-steam');const before=win.getComputedStyle(steam).transform;
  await new Promise(resolve=>win.setTimeout(resolve,120));
  check(win.getComputedStyle(steam).transform!==before,'steam really moves between rendered frames');
  // Apply the production reduced-motion block without changing the host OS
  // preference. This verifies the actual static poses on every test machine.
  const reduced=doc.createElement('style');
  const motionSheet=[...doc.styleSheets].flatMap(sheet=>[...sheet.cssRules]).find(rule=>rule.href?.endsWith('/styles/assistant-activity.css'))?.styleSheet;
  const motionRule=[...(motionSheet?.cssRules||[])].find(rule=>rule.conditionText==='(prefers-reduced-motion: reduce)');
  check(!!motionRule,'production stylesheet declares a reduced-motion fallback');
  reduced.textContent=[...motionRule.cssRules].map(rule=>rule.cssText).join('\n');doc.head.append(reduced);await paint();
  check(win.getComputedStyle(initial.querySelector('.activity-pot-rig')).animationName==='none'&&win.getComputedStyle(steam).opacity==='0.65','reduced-motion rules keep static steam and stop rocking');
  reduced.remove();await paint();
  const long=row('long'),pot=long.querySelector('.assistant-activity'),title=long.querySelector('.assistant-title');
  check(pot.getBoundingClientRect().width===24,'indicator retains a fixed 24px slot');
  check(title.getBoundingClientRect().right<=pot.getBoundingClientRect().left,'long name truncates before the pot');
  const animation=initial.querySelector('.activity-pot-rig').getAnimations()[0];
  f.update({preview:'Another event without a phase change'},['runner']);await until(()=>row('research').textContent.includes('Another event'));
  check(indicator()===initial&&initial.querySelector('.activity-pot-rig').getAnimations()[0]===animation,'unrelated refresh retains the node and animation');
  Object.defineProperty(doc,'visibilityState',{configurable:true,value:'hidden'});doc.dispatchEvent(new win.Event('visibilitychange'));
  check(win.getComputedStyle(initial.querySelector('.activity-pot-rig')).animationPlayState==='paused','hidden-document lifecycle pauses activity');
  delete doc.visibilityState;doc.dispatchEvent(new win.Event('visibilitychange'));await paint();
  f.update({executing:false,reply_count:1,reply_id:'research-reply'});await until(()=>state()==='ready');
  check(win.getComputedStyle(indicator().querySelector('.activity-pot-rig')).animationName==='none','completion stops rocking');
  check(win.getComputedStyle(indicator().querySelector('.activity-pot-check')).display==='block','ready Pot has a visible success check');
  f.update({executing:true},['runner']);await until(()=>state()==='cooking');
  check(row('research').querySelector('.unread-badge')?.textContent==='1','new execution takes priority without erasing unread count');
  f.update({executing:false});await until(()=>state()==='ready');
  f.preference('locale','en');await until(()=>indicator().getAttribute('aria-label')==='Ready · Unread reply');
  check(row('research').querySelector('.unread-badge').getAttribute('aria-label')==='1 unread replies','live locale change translates accessible unread count');
  f.preference('theme','dark');await until(()=>win.getComputedStyle(indicator()).color===win.getComputedStyle(row('research')).color);
  check(win.getComputedStyle(indicator()).color===win.getComputedStyle(row('research')).color,'monochrome pot follows dark text color');
  f.preference('locale','zh-CN');f.preference('theme','light');await paint();
  f.readElsewhere();await until(()=>state()==='');
  check(!row('research').querySelector('.unread-badge'),'shared read receipt removes pot and count');
  f.update({executing:true},['tasks'],'two');await until(()=>indicator('research','two')?.dataset.state==='cooking');
  check(!indicator(),'remote work cannot change the local assistant');
  // Simulate disconnected catalogue entries through the real Space projection.
  const original=f.client.request;
  f.client.request=async(path,options)=>{const value=await original(path,options);if(path==='/api/v1/spaces')value.items[1].connected=false;return value;};
  f.change(['spaces'],'two');await until(()=>!indicator('research','two'));
  check(!indicator('research','two'),'offline cached execution never cooks');
  f.client.request=original;f.change(['spaces'],'two');await until(()=>indicator('research','two'));
  f.readPending=true;f.update({executing:false,reply_count:2,reply_id:'research-reply'});await until(()=>state()==='ready');
  row('research').click();await until(()=>doc.querySelector('.window-title').textContent.includes('研究助手'));
  doc.querySelector('#pot-composer').focus();win.dispatchEvent(new win.Event('focus'));await until(()=>f.resolveRead);
  check(state()==='ready','opening Chat waits for a successful read acknowledgement');
  f.resolveRead();await until(()=>state()==='');
  check(!indicator(),'reading the completed reply clears the ready Pot');
  check(doc.documentElement.scrollWidth===width,'page has no horizontal overflow');
  check(f.errors.length===0,'no browser errors');
}
