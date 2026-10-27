// Serve the repository root over loopback and open fixtures/chat-switching.html.
// Production Chat DOM with synthetic subscriptions and independently delayed tasks.
import {mountApp} from '../../web/src/components/ui.js';
import {domainsFor} from '../../web/src/state/domains/index.mjs';

const results=document.querySelector('#results'),root=document.querySelector('#app');
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const settle=async()=>{await pause(100);await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));};
const wait=async(test)=>{for(let i=0;i<250;i++){if(test())return;await pause(20);}throw Error('Timed out: '+test);};
const check=(value,message)=>{if(!value)throw Error(message);results.textContent+='PASS '+message+'\n';};
const errors=[];
window.addEventListener('error',event=>errors.push(event.message));
window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason?.stack||event.reason)));
localStorage.setItem('pot-system-notifications-browser-v1','false');
document.documentElement.dataset.platform='browser';

async function run(){
  results.textContent='';
  for(const taskDelay of [350]){
    let scope={space:'one',owner:'alpha'},subscription,taskCount=0;
    const counts={alpha:80,beta:12},versions={alpha:1,beta:1},historyReads=[],requestCalls=[],settingsReads=[];
    const snapshot=owner=>{
      const count=counts[owner],messages=Array.from({length:count},(_,index)=>({id:`${owner}-${index+1}`,sequence:String(index+1),role:index%2?'assistant':'user',...(index%2?{reply_to:`${owner}-${index}`} : {}),...(index===count-2?{references:[{message_id:`${owner}-2`,quote:'Jump to the earlier answer'}]}:{}),text:`${owner} message ${index+1}. `+'Synthetic chat text for checking assistant navigation. '.repeat(10),status:'complete'}));
      return {kind:'reset',base_cursor:0,runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify(['one',owner]),conversation_id:owner,cursor:String(versions[owner]),replies:[],running:false,turn_id:null,messages,history:{before:'1',after:String(count),has_more:false,has_newer:false}};
    };
    const client={chatCacheName:'pot-switching-'+crypto.randomUUID(),production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},
      changes:()=>()=>{},subscribe(receive,status){const owner=scope.owner,record={owner,receive};subscription=record;const timer=setTimeout(()=>{status('connected');receive(snapshot(owner));record.ready=true;},200);return()=>clearTimeout(timer);},
      history:async query=>{historyReads.push(query);return snapshot(scope.owner);},
      async request(path,options={}){
        requestCalls.push(path);
        const url=new URL(path,location.href);
        if(url.pathname==='/api/v1/spaces')return {active_space_id:'one',items:[{id:'one',name:'Fixture',control_id:'fixture',connected:true}]};
        if(url.pathname==='/api/v1/assistants')return {items:[{id:'alpha',name:'Alpha'},{id:'beta',name:'Beta'},...Array.from({length:24},(_,i)=>({id:'extra-'+i,name:'Assistant '+i}))].map(item=>({...item,capabilities:{version:1,operations:['chat.read','chat.send','assistant.configure','task.list']}}))};
        if(url.pathname==='/api/v1/agent'||url.pathname==='/api/v1/instructions'){settingsReads.push({path:url.pathname,space:options.space,owner:options.owner});return url.pathname.endsWith('/agent')?{generation:1,revision:0,config:{model:'fixture'}}:{text:'',revision:'fixture'};}
        if(url.pathname==='/api/v1/tasks/query'){await pause(taskDelay);return {items:Array.from({length:taskCount},(_,index)=>({id:`task-${index}`,title:`Background task ${index+1}`,phase:'running',assistant_id:scope.owner}))};}
        return {items:[],pending:[],policy:{}};
      },
    };
    const dispose=mountApp(root,client);
    window.chatSwitchingIO=()=>({requests:requestCalls.length,history:historyReads.length,settings:settingsReads.slice()});
    const history=()=>root.querySelector('.history');
    const bottom=()=>history().scrollHeight-history().clientHeight-history().scrollTop<2;
    const select=async(owner,{beforeSnapshot=false}={})=>{
      if(matchMedia('(max-width: 719px)').matches&&!root.classList.contains('show-directory')){
        root.querySelector('.titlebar .narrow-navigation').click();
        if(beforeSnapshot)await pause(0);else await settle();
      }
      root.querySelector(`.assistant-row[data-view-key='["one","${owner}"]']`).click();
      await wait(()=>scope.owner===owner);
      if(!beforeSnapshot)await wait(()=>subscription?.ready);
      await settle();
    };
    const latest=async()=>{root.querySelector('.latest-button').click();await settle();};
    let passed=false;
    try{
      await wait(()=>subscription?.ready);await settle();
      check(bottom(),`Initial long Chat follows latest (tasks delayed ${taskDelay}ms)`);
      const composer=root.querySelector('#pot-composer');composer.value='Folded draft';composer.dispatchEvent(new Event('input',{bubbles:true}));
      root.querySelector('.directory-toggle').click();await settle();
      check(!root.querySelector('.assistant-directory').getClientRects().length,'Folding hides the directory');
      check(document.activeElement===root.querySelector('.directory-toggle'),'Folding retains keyboard focus on its trigger');
      root.querySelector('.directory-toggle').click();await settle();
      check(root.querySelector('#pot-composer')===composer&&composer.value==='Folded draft'&&bottom(),'Folding retains composer identity, draft and Latest');

      await select('beta');
      counts.alpha+=20;versions.alpha++;
      await select('alpha');
      check(bottom(),'Cached assistant remains at latest after a newer reset');
      check(root.querySelector('.latest-button').hidden,'Following remains active after cached return');
      // Remounts and row measurement can produce a delayed native offset after
      // Latest was already applied. No reader gesture authorized leaving it.
      history().scrollTop-=350;history().dispatchEvent(new Event('scroll'));await settle();
      check(bottom()&&root.querySelector('.latest-button').hidden,'A native layout scroll after cached return does not replace Latest');
      await select('beta');await select('alpha');
      check(bottom()&&root.querySelector('.latest-button').hidden,'Returning again retains Latest after a native layout scroll');
      // Leave at latest while the task region is visible; returning must not
      // reinterpret its saved visible anchor as intentional historical reading.
      taskCount=4;await select('beta');await pause(taskDelay+50);await latest();
      await select('alpha');await pause(taskDelay+100);await latest();
      await select('beta');counts.alpha+=20;versions.alpha++;
      await select('alpha');await pause(taskDelay+100);
      if(taskDelay===0&&new URLSearchParams(location.search).has('capture')){
        window.chatSwitchingCaptureReady=true;
        await new Promise(resolve=>window.chatSwitchingContinue=resolve);
      }
      check(bottom(),'Visible background task anchors retain latest following on return');
      const before=history().scrollTop;counts.alpha+=2;versions.alpha++;subscription.receive(snapshot('alpha'));await settle();
      check(bottom()&&history().scrollTop>before,'New reply continues following after returning');
      await select('beta',{beforeSnapshot:true});await select('alpha',{beforeSnapshot:true});
      await wait(()=>subscription?.ready);await settle();
      check(bottom(),'Switching back before the intermediate subscription responds retains latest');
      // Deliberate reading still survives both owner navigation and refresh.
      await pause(taskDelay+100);
      history().dispatchEvent(new WheelEvent('wheel',{deltaY:-50,bubbles:true}));
      history().scrollTop=history().scrollHeight/2;await settle();
      check(!bottom(),'Upward reading pauses following');
      const anchor=()=>{const top=history().getBoundingClientRect().top;return [...root.querySelectorAll('article.message')].find(row=>row.getBoundingClientRect().bottom>top&&row.getBoundingClientRect().top<top+history().clientHeight);};
      const row=anchor(),id=row.dataset.messageId,offset=row.getBoundingClientRect().top-history().getBoundingClientRect().top;
      await select('beta');await select('alpha');await pause(taskDelay+100);
      const restored=[...root.querySelectorAll('article.message')].find(row=>row.dataset.messageId===id);
      const restoredOffset=restored?.getBoundingClientRect().top-history().getBoundingClientRect().top;
      check(restored&&Math.abs(restoredOffset-offset)<3,`Intentional historical reading restores its message anchor (${id}: ${offset} → ${restoredOffset})`);
      check(!root.querySelector('.latest-button').hidden,'Historical reading keeps Latest available');
      await latest();check(bottom(),'Latest resumes following');
      history().scrollTop-=350;history().dispatchEvent(new Event('scroll'));await settle();
      await select('beta');await select('alpha');
      check(bottom()&&root.querySelector('.latest-button').hidden,'Latest retires earlier reading input across the next assistant switch');
      document.dispatchEvent(new KeyboardEvent('keydown',{key:',',ctrlKey:true,bubbles:true}));
      await wait(()=>history().hidden);window.history.back();await wait(()=>!history().hidden);await settle();
      check(bottom()&&root.querySelector('.latest-button').hidden,'Settings return retains latest following');
      check(historyReads.length===0,'Switching at latest does not request a historical window');
      {
        taskCount=0;await select('beta');await select('alpha');
        await domainsFor(client).tasks.active(client.scope()).refresh(true);
        await wait(()=>!root.querySelector('.background-task'));await latest();
        await navigationFrames({root,client,select,snapshot,historyReads,emit:frame=>{versions[client.scope().owner]=Number(frame.cursor);subscription.receive(frame);}});
      }
      check(errors.length===0,'No browser errors: '+errors.join('; '));
      passed=true;
    }finally{if(passed&&new URLSearchParams(location.search).get('contract')!=='navigation')dispose();else window.chatSwitchingDispose=dispose;}
  }
  results.textContent+='ALL CHECKS PASSED\n';
}
window.chatSwitchingRun=run;
run().catch(error=>results.textContent+='FAIL '+error.message+'\n'+error.stack+'\n');

// Sample every rendered frame, rather than just waiting for a final position.
// The first visible frame must already have the correct measured message window.
async function navigationFrames({root,client,select,snapshot,historyReads,emit}){
  const history=()=>root.querySelector('.history');
  const visible=()=>!history().hidden&&getComputedStyle(history()).visibility!=='hidden'&&!root.classList.contains('show-directory');
  const row=id=>[...history().querySelectorAll('article.message')].find(node=>node.dataset.messageId===id);
  const referenceEvidence=window.chatSwitchingReferenceEvidence=[];
  const openReference=async(title,{source}={})=>{
    const scope={...client.scope()},owner=JSON.stringify([scope.space,scope.owner]);let button;
    await wait(()=>{button=[...history().querySelectorAll('.message-reference-open')].find(node=>node.title===title);const article=button?.closest('article.message');return visible()&&root.querySelector('.assistant-row.is-selected')?.dataset.viewKey===owner&&!history().querySelector('.message-window[aria-busy]')&&button?.isConnected&&!button.disabled&&getComputedStyle(button).visibility==='visible'&&(!source||article?.dataset.messageId===source.id&&article.dataset.sequence===source.sequence);});
    const article=button.closest('article.message');referenceEvidence.push({at:performance.now(),scope,selected:root.querySelector('.assistant-row.is-selected')?.dataset.viewKey,title:button.title,source:article?.dataset.messageId,sequence:article?.dataset.sequence,expected:source&&{id:source.id,sequence:source.sequence,target:source.references?.find(ref=>ref.quote===title)?.message_id},visible:visible(),busy:!!history().querySelector('.message-window[aria-busy]'),disabled:button.disabled});button.click();
  };
  const offset=id=>row(id)?.getBoundingClientRect().top-history().getBoundingClientRect().top;
  const capture=()=>{const top=history().getBoundingClientRect().top;const node=[...history().querySelectorAll('article.message')].find(node=>{const box=node.getBoundingClientRect();return box.height&&box.bottom>top&&box.top<top+history().clientHeight;});return {id:node?.dataset.messageId,offset:node?.getBoundingClientRect().top-top};};
  async function frames(label,action,validate){
    let failure='',checked=0;
    const sample=()=>{if(!visible())return;checked++;try{validate();}catch(error){failure ||= error.message;}};
    let sampling=true;
    const tick=()=>{if(!sampling)return;sample();requestAnimationFrame(tick);};
    const observer=new MutationObserver(sample);observer.observe(root,{attributes:true,attributeFilter:['hidden','data-route']});
    try{const work=action();requestAnimationFrame(tick);await work;for(let i=0;i<12;i++)await new Promise(requestAnimationFrame);}
    finally{sampling=false;observer.disconnect();}
    check(checked>0&&!failure,`${label}: ${checked} visible frames${failure?' — '+failure:''}`);
  }
  const atLatest=()=>{
    const h=history();if(h.scrollHeight-h.clientHeight-h.scrollTop>1)throw Error('Chat is not at latest');
    const last=snapshot(client.scope().owner).messages.at(-1),node=row(last.id);
    if(!node||node.getBoundingClientRect().bottom>h.getBoundingClientRect().bottom+1)throw Error(`Latest message ${last.id} is not mounted in the viewport (${node?.getBoundingClientRect().bottom} > ${h.getBoundingClientRect().bottom})`);
  };
  const atAnchor=anchor=>()=>{if(!row(anchor.id)||Math.abs(offset(anchor.id)-anchor.offset)>2)throw Error(`Anchor ${anchor.id} drifted: ${anchor.offset} → ${offset(anchor.id)}`);};
  const narrow=matchMedia('(max-width:719px)').matches;
  const settings=async()=>{document.dispatchEvent(new KeyboardEvent('keydown',{key:',',ctrlKey:true,bubbles:true}));await wait(()=>history().hidden);await settle();};
  const returnFromSettings=async()=>{window.history.back();await wait(()=>visible());};
  const input=root.querySelector('.composer-input');input.value='A multiline draft\n'.repeat(7);input.dispatchEvent(new Event('input',{bubbles:true}));await settle();
  const composerHeight=root.querySelector('.composer-area').getBoundingClientRect().height;
  if(new URLSearchParams(location.search).has('capture-navigation')){
    window.navigationCaptureReady=true;await new Promise(resolve=>window.navigationCaptureContinue=resolve);
  }
  await frames('Typing in an already tall composer retains Latest',async()=>{
    input.value+='继续输入';input.dispatchEvent(new Event('input',{bubbles:true}));await settle();
  },()=>{atLatest();if(!root.querySelector('.latest-button').hidden)throw Error('Typing paused following');});
  const drag=history();drag.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}));drag.scrollTop-=600;await settle();
  const firstDrag=capture();drag.scrollTop-=600;await settle();const nextDrag=capture();
  check(nextDrag.id!==firstDrag.id||Math.abs(nextDrag.offset-firstDrag.offset)>100,'Scrollbar dragging continues across virtual row resize callbacks');
  window.dispatchEvent(new PointerEvent('pointerup',{bubbles:true}));root.querySelector('.latest-button').click();await settle();
  await settings();await frames('Settings return follows latest from its first frame',returnFromSettings,()=>{atLatest();if(Math.abs(root.querySelector('.composer-area').getBoundingClientRect().height-composerHeight)>1)throw Error('Composer height changed');});
  const draftOwner=client.scope().owner,cachedDraft=input.value;
  await frames('Cached chat switch restores its multiline draft height and Latest from its first frame',async()=>{await select('beta');await select('alpha');},()=>{
    atLatest();
    if(client.scope().owner===draftOwner&&(input.value!==cachedDraft||Math.abs(root.querySelector('.composer-area').getBoundingClientRect().height-composerHeight)>1))throw Error('Cached draft content or height changed');
  });
  history().dispatchEvent(new WheelEvent('wheel',{deltaY:-100,bubbles:true}));history().scrollTop=history().scrollHeight/2;await settle();
  const anchor=capture();check(!!anchor.id,'Reading frame has a message anchor');
  await frames('Composer growth and shrink preserve historical reading',async()=>{
    for(const text of ['Short draft','A multiline draft\n'.repeat(7),'A multiline draft\n'.repeat(7)+'继续输入']){
      input.value=text;input.dispatchEvent(new Event('input',{bubbles:true}));await settle();
    }
  },atAnchor(anchor));
  // A shell redraw can run before ResizeObserver after width-driven reflow.
  // Keep the saved reading anchor instead of capturing the reflowed viewport.
  const savedWidth=root.style.width;
  const reflow=async width=>{root.style.width=width;window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-theme',value:'light'}}));await settle();};
  await frames('Width reflow preserves reading before the resize observer',()=>reflow(narrow?'620px':'680px'),atAnchor(anchor));
  await frames('Restoring width preserves the same reading anchor',()=>reflow(savedWidth),atAnchor(anchor));
  await settings();await frames('Settings return restores reading before exposure',returnFromSettings,atAnchor(anchor));
  await select('beta');
  await frames('Cached chat restores reading before exposure',()=>select('alpha'),()=>{if(client.scope().owner==='alpha')atAnchor(anchor)();});
  // The actual upper-right assistant editor has a separate Back path.
  root.querySelector('.titlebar button[aria-label="Assistant settings"],.titlebar button[aria-label="助手设置"]').click();
  await wait(()=>root.querySelector('.editor-form'));await settle();
  await frames('Assistant editor Back restores reading before exposure',async()=>{root.querySelector('.titlebar button[aria-label="Back"],.titlebar button[aria-label="返回"]').click();await wait(()=>visible());},atAnchor(anchor));
  // Browser history and another root page must use the same restoration path.
  if(!narrow){
    root.querySelector('.rail-button[aria-label="Tasks"],.rail-button[aria-label="任务"]').click();await wait(()=>history().hidden);await settle();
    await frames('Tasks browser Back restores reading before exposure',returnFromSettings,atAnchor(anchor));
  }
  root.querySelector('.latest-button').click();await settle();
  const target=`${client.scope().owner}-2`;
  const beforeReference=capture();
  await frames('Reference jump retains the old reading frame until its prepared target commits centered',async()=>{
    root.querySelector('.message-reference-open').click();
    await wait(()=>!!row(target));
  },()=>{
    if(!row(target)){atAnchor(beforeReference)();return;}
    const box=row(target).getBoundingClientRect(),h=history().getBoundingClientRect();
    if(Math.abs((box.top+box.bottom-h.top-h.bottom)/2)>2)throw Error('Reference destination is not centered');
  });
  const referenceAnchor=capture();await settings();await frames('Reference location survives a page round trip',returnFromSettings,atAnchor(referenceAnchor));
  // References into a windowed tool tree must mount their ancestry and child.
  const nested=snapshot(client.scope().owner),turn=nested.messages[0].id;
  const tool=(id,parent='')=>({id,role:'tool',text:'',status:'complete',tool:{id,turn_id:turn,parent_id:parent,name:'fixture_tool',phase:'completed',args:{value:id},output:[]}});
  const parent=tool('parent-tool'),children=Array.from({length:40},(_,i)=>tool(`child-tool-${i}`,parent.id));
  parent.tool.script={failureID:'child-tool-25'};parent.tool.diagnostic='Synthetic nested failure';
  nested.cursor='90';nested.messages.splice(-2,0,parent,...children);nested.messages.forEach((message,i)=>message.sequence=String(i+1));nested.messages.at(-2).references=[{message_id:'child-tool-25',quote:'Nested tool destination'}];
  root.querySelector('.latest-button').click();emit(nested);await settle();
  await openReference('Nested tool destination',{source:nested.messages.at(-2)});await wait(()=>!!row('child-tool-25')&&row('parent-tool')?.querySelector('details')?.open);
  check(!!row('child-tool-25')&&row('parent-tool').querySelector('details').open,'Reference mounts its target inside an expanded virtual tool tree');
  const nestedAnchor=capture();await settings();await frames('Nested tool reading survives settings',returnFromSettings,atAnchor(nestedAnchor));
  const disclosure=row('parent-tool').querySelector('details');
  disclosure.querySelector('summary').dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}));disclosure.querySelector('summary').click();await settle();
  check(!disclosure.open,'User disclosure changes supersede the restored nested reading anchor');
  row('parent-tool').querySelector('.tool-failure').click();await wait(()=>!!row('child-tool-25')&&disclosure.open);
  check(!!row('child-tool-25')&&disclosure.open,'Tool failure navigation uses the cursor to mount a recycled child');
  const split=structuredClone(nested);split.cursor='91';
  split.messages.splice(split.messages.findIndex(message=>message.id==='child-tool-20'),0,{id:'work-boundary',role:'assistant',text:'Prose separates these work records.',status:'complete'});
  split.messages.forEach((message,i)=>message.sequence=String(i+1));
  root.querySelector('.latest-button').click();emit(split);await settle();
  await openReference('Nested tool destination',{source:split.messages.at(-2)});await wait(()=>{const group=row('child-tool-25')?.closest('.work-record-group');return group?.querySelector('.work-records')?.open&&!group.querySelector('[data-message-id="parent-tool"]');});
  const splitTarget=row('child-tool-25'),splitGroup=splitTarget?.closest('.work-record-group');
  check(!!splitTarget&&splitGroup?.querySelector('.work-records').open&&!splitGroup.querySelector('[data-message-id="parent-tool"]'),'Reference mounts and opens the target work group across a prose boundary');
  root.querySelector('.latest-button').click();await settle();
  const original=snapshot(client.scope().owner);original.cursor='92';emit(original);await settle();
  check(historyReads.length===0,'Cached navigation and loaded references do not fetch history');
  // A delayed locate completing after an explicit Latest action cannot pull back.
  const read=client.history;let release;
  client.history=query=>query.around?new Promise(resolve=>{const frame=snapshot(client.scope().owner);release=()=>resolve({...frame,cursor:'100',messages:[{id:query.around,sequence:'2',role:'assistant',reply_to:'alpha-1',text:'Late destination',status:'complete'}],history:{before:'2',after:'2',has_more:true,has_newer:true}});}):Promise.resolve({...snapshot(client.scope().owner),cursor:'100'});
  root.querySelector('.latest-button').click();await settle();
  const projected=snapshot(client.scope().owner);projected.cursor='100';projected.messages.at(-2).references=[{message_id:'missing-target',quote:'Delayed target'}];
  emit(projected);await openReference('Delayed target');await wait(()=>!!release);
  const beforeDelayed=capture();
  await frames('Delayed reference retains the old frame until its prepared destination mounts',async()=>{release();await wait(()=>!!row('missing-target'));},()=>{if(!row('missing-target'))atAnchor(beforeDelayed)();});
  root.querySelector('.latest-button').click();await settle();atLatest();
  projected.cursor='101';projected.messages.at(-2).references=[{message_id:'superseded-target',quote:'Superseded target'}];emit(projected);release=null;await openReference('Superseded target');await wait(()=>!!release);
  await frames('Latest supersedes an in-flight reference lookup',async()=>{root.querySelector('.latest-button').click();release();await settle();},atLatest);
  const again=snapshot(client.scope().owner);again.cursor='102';again.messages.at(-2).references=[{message_id:'retired-owner-target',quote:'Retired owner target'}];emit(again);
  release=null;await openReference('Retired owner target');await wait(()=>!!release);
  await select('beta');await select('alpha');release();await settle();
  check(client.scope().owner==='alpha'&&!row('retired-owner-target'),'A late reference cannot overwrite a later A → B → A navigation');
  client.history=read;
  input.value='';input.dispatchEvent(new Event('input',{bubbles:true}));await settle();
}
