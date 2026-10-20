// Serve the repository root over loopback and open fixtures/chat-switching.html.
// Production Chat DOM with synthetic subscriptions and independently delayed tasks.
import {mountApp} from '../../web/src/components/ui.js';

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
  for(const taskDelay of [0,350]){
    let scope={space:'one',owner:'alpha'},subscription,taskCount=0;
    const counts={alpha:80,beta:12},versions={alpha:1,beta:1},historyReads=[];
    const snapshot=owner=>{
      const count=counts[owner],messages=Array.from({length:count},(_,index)=>({id:`${owner}-${index+1}`,sequence:String(index+1),role:index%2?'assistant':'user',...(index%2?{reply_to:`${owner}-${index}`} : {}),text:`${owner} message ${index+1}. `+'Synthetic chat text for checking assistant navigation. '.repeat(10),status:'complete'}));
      return {kind:'reset',base_cursor:0,runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify(['one',owner]),conversation_id:owner,cursor:String(versions[owner]),running:false,turn_id:null,messages,history:{before:'1',after:String(count),has_more:false,has_newer:false}};
    };
    const client={production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},
      changes:()=>()=>{},subscribe(receive,status){const owner=scope.owner,record={owner,receive};subscription=record;const timer=setTimeout(()=>{status('connected');receive(snapshot(owner));},200);return()=>clearTimeout(timer);},
      history:async query=>{historyReads.push(query);return snapshot(scope.owner);},
      async request(path){
        const url=new URL(path,location.href);
        if(url.pathname==='/api/v1/spaces')return {active_space_id:'one',items:[{id:'one',name:'Fixture',control_id:'fixture',connected:true}]};
        if(url.pathname==='/api/v1/assistants')return {items:[{id:'alpha',name:'Alpha'},{id:'beta',name:'Beta'}]};
        if(url.pathname==='/api/v1/tasks/query'){await pause(taskDelay);return {items:Array.from({length:taskCount},(_,index)=>({id:`task-${index}`,title:`Background task ${index+1}`,phase:'running',assistant_id:scope.owner}))};}
        return {items:[],pending:[],policy:{}};
      },
    };
    const dispose=mountApp(root,client);
    const history=()=>root.querySelector('.history');
    const bottom=()=>history().scrollHeight-history().clientHeight-history().scrollTop<2;
    const select=async(owner,{beforeSnapshot=false}={})=>{
      if(matchMedia('(max-width: 719px)').matches&&!root.classList.contains('show-directory')){
        root.querySelector('.titlebar .narrow-navigation').click();
        if(beforeSnapshot)await pause(0);else await settle();
      }
      root.querySelector(`.assistant-row[data-view-key='["one","${owner}"]']`).click();
      await wait(()=>scope.owner===owner);
      if(!beforeSnapshot)await wait(()=>root.querySelector('.connection-status')?.textContent.includes('Connected'));
      await settle();
    };
    const latest=async()=>{root.querySelector('.latest-button').click();await settle();};
    let passed=false;
    try{
      await wait(()=>root.querySelector('.connection-status')?.textContent.includes('Connected'));await settle();
      check(bottom(),`Initial long Chat follows latest (tasks delayed ${taskDelay}ms)`);
      await select('beta');
      counts.alpha+=20;versions.alpha++;
      await select('alpha');
      check(bottom(),'Cached assistant remains at latest after a newer reset');
      check(root.querySelector('.latest-button').hidden,'Following remains active after cached return');
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
      await wait(()=>root.querySelector('.connection-status')?.textContent.includes('Connected'));await settle();
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
      document.dispatchEvent(new KeyboardEvent('keydown',{key:',',ctrlKey:true,bubbles:true}));
      await wait(()=>history().hidden);window.history.back();await wait(()=>!history().hidden);await settle();
      check(bottom()&&root.querySelector('.latest-button').hidden,'Settings return retains latest following');
      check(historyReads.length===0,'Switching at latest does not request a historical window');
      check(errors.length===0,'No browser errors: '+errors.join('; '));
      passed=true;
    }finally{if(passed)dispose();else window.chatSwitchingDispose=dispose;}
  }
  results.textContent+='ALL CHECKS PASSED\n';
}
window.chatSwitchingRun=run;
run().catch(error=>results.textContent+='FAIL '+error.stack+'\n');
