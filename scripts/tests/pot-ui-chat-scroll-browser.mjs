// Production Chat, synthetic long tool output and delayed bidirectional pages.
// Serve the repository over loopback; all assertions run in the browser itself.
import {mountApp} from '../../web/src/components/ui.js';
import {createChatCache} from '../../web/src/state/chat-cache.mjs';
import {createMessageWindow} from '../../web/src/components/message-window.mjs';
import {createToolCall} from '../../web/src/components/tool-call.mjs';
import {createTaskConversation} from '../../web/src/components/task-conversation.mjs';

const root=document.querySelector('#app'),results=document.querySelector('#results');
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const frame=()=>new Promise(resolve=>requestAnimationFrame(resolve));
const settle=async()=>{await frame();await frame();await pause(60);};
const errors=[],reads=[];
window.addEventListener('error',event=>errors.push(event.message));
window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason?.stack||event.reason)));
document.documentElement.dataset.platform='browser';
localStorage.setItem('pot-system-notifications-browser-v1','false');
let receive,version=1,count=1600,scope={space:'fixture',owner:'alpha'},inflight=0,maxInflight=0;
const longOutput=Array.from({length:350},(_,i)=>`- Record ${i}: synthetic inspection evidence, retained for tool disclosure testing.`).join('\n');
const message=index=>{
  const base={id:`m${index}`,sequence:String(index),version:1,status:'complete',text:''};
  if(index%4===1)return {...base,role:'user',request_id:`turn${index}`,text:`Review batch ${index}. Check the evidence and describe the result.`};
  if(index%4===2)return {...base,role:'tool',tool:{id:`tool${index}`,turn_id:`turn${index-1}`,name:'inspect_records',args:{batch:index,query:'Synthetic records only'},phase:'completed',started_at_ms:1,finished_at_ms:1001,result:{content:[{type:'text',text:longOutput}]}}};
  return {...base,role:'assistant',reply_to:`m${index-index%4+1-4*(index%4===0)}`,text:`### Batch ${index} findings\n\n`+'The records are available and the conversation remains readable while scrolling. '.repeat(index%7+2)};
};
const page=(first,last)=>({kind:'reset',runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify(['fixture','alpha']),conversation_id:'alpha',cursor:String(version),replies:[],running:false,turn_id:null,messages:Array.from({length:last-first+1},(_,i)=>message(first+i)),history:{before:String(first),after:String(last),has_more:first>1,has_newer:last<count}});
const client={chatCacheName:'pot-scroll-'+crypto.randomUUID(),production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},
  subscribe(callback,status){receive=callback;queueMicrotask(()=>{status('connected');callback(page(count-79,count));});return()=>{};},
  async history(query){
    const start=performance.now();maxInflight=Math.max(maxInflight,++inflight);
    reads.push({...query});await pause(160);inflight--;
    const first=query.before?Math.max(1,Number(query.before)-80):query.after?Number(query.after)+1:count-79;
    const last=query.before?Number(query.before)-1:Math.min(count,first+79);
    const result=page(first,last);result.elapsed=performance.now()-start;return result;
  },
  async request(path){const url=new URL(path,location.href);
    if(url.pathname==='/api/v1/spaces')return {active_space_id:'fixture',items:[{id:'fixture',name:'Fixture',kind:'local',control_id:'fixture',connected:true}]};
    if(url.pathname==='/api/v1/assistants')return {items:[{id:'alpha',name:'Scroll fixture'}]};
    return {items:[],pending:[],policy:{}};
  },
};
mountApp(root,client);
const history=()=>root.querySelector('.history');
function scroll(top){const h=history();h.dispatchEvent(new WheelEvent('wheel',{deltaY:top-h.scrollTop||-1,bubbles:true}));h.scrollTop=top;h.dispatchEvent(new Event('scroll'));}
function anchor(){const h=history(),top=h.getBoundingClientRect().top;const row=[...h.querySelectorAll('article.message')].find(e=>{const r=e.getBoundingClientRect();return r.height&&r.bottom>top&&r.top<top+h.clientHeight;});return row?{id:row.dataset.messageId,offset:row.getBoundingClientRect().top-top,node:row}:null;}
function offset(id){const h=history(),row=h.querySelector(`[data-message-id="${id}"]`);return row?.getBoundingClientRect().top-h.getBoundingClientRect().top;}
const check=(value,label)=>{results.textContent+=`${value?'PASS':'FAIL'} ${label}\n`;};
async function run(){
  results.textContent='Running…\n';
  try{
    root.querySelector('.latest-button').click();await settle();
    const h=history();
    const collapsed=[...h.querySelectorAll('details.tool-call')].filter(e=>!e.open);
    const hiddenChars=collapsed.reduce((sum,e)=>sum+(e.querySelector('.tool-body')?.textContent.length||0),0);
    results.textContent+=`Collapsed tool body characters: ${hiddenChars}\n`;
    check(collapsed.length>0&&hiddenChars<1000,'collapsed tools defer output and raw JSON');
    scroll(h.scrollTop-h.clientHeight*.5);await settle();
    const saved=anchor();h.dispatchEvent(new Event('scroll'));await settle();
    check(h.querySelector(`[data-message-id="${saved.id}"]`)===saved.node,'unchanged viewport reuses its mounted message DOM');
    const timings=[];let blanks=0,gaps=0,maxMounted=0;
    for(let i=0;i<90;i++){
      const start=performance.now();scroll(Math.max(0,h.scrollTop+(i%30<15?-1:1)*h.clientHeight*.8));await frame();
      timings.push(performance.now()-start);if(!anchor())blanks++;
      const viewport=h.getBoundingClientRect();if([...h.querySelectorAll('.message-window-spacer')].some(e=>{const r=e.getBoundingClientRect();return r.height>1&&r.bottom>viewport.top+1&&r.top<viewport.top+h.clientHeight-1;}))gaps++;
      maxMounted=Math.max(maxMounted,h.querySelectorAll('article.message').length);
    }
    check(blanks===0,'rapid bidirectional scrolling keeps a visible message on every frame');
    check(gaps===0,'virtual spacers never enter the sampled viewport');
    check(maxMounted<180,`mounted rows stay bounded (${maxMounted})`);
    check(maxInflight<=1,'history prefetch serializes delayed reads');
    const ordered=timings.sort((a,b)=>a-b);results.textContent+=`Scroll sample p95: ${ordered[Math.floor(ordered.length*.95)].toFixed(1)}ms (one frame); pages: ${reads.length}\n`;
    for(let i=0;i<10;i++){scroll(100);await pause(190);await settle();}
    check(reads.filter(query=>query.before).length>=9,'traversal pages beyond the 640-message data window');
    const persisted=createChatCache({name:client.chatCacheName}),cacheKey=JSON.stringify(['fixture','fixture','alpha']);
    const beforePrefetch=(await persisted.read(cacheKey)).snapshot.history.after;scroll(h.scrollHeight-h.clientHeight*2.1);
    check(h.scrollHeight-h.clientHeight-h.scrollTop>h.clientHeight,'newer prefetch starts before the last viewport');
    // Observe the committed window, not a fixed 80 ms slice of its asynchronous
    // cache read, transaction and geometry reconciliation.
    let afterPrefetch=beforePrefetch;
    for(let attempt=0;attempt<40&&afterPrefetch===beforePrefetch;attempt++){await pause(25);afterPrefetch=(await persisted.read(cacheKey)).snapshot.history.after;}
    persisted.close();
    check(BigInt(afterPrefetch)>BigInt(beforePrefetch),'newer history prefetch reads disk or backend more than one viewport before the edge');
    await pause(190);await settle();
    scroll(h.scrollTop-h.clientHeight*.2);await settle();
    const reading=anchor();version++;count+=4;const delta=page(count-3,count);receive({...delta,kind:'delta',base_cursor:String(version-1)});await settle();
    check(Math.abs(offset(reading.id)-reading.offset)<3,'new output preserves historical reading');
    const tool=h.querySelector('details.tool-call');
    if(tool){tool.open=true;await settle();check(tool.querySelector('.tool-content')?.textContent.includes('Record'),'opening a tool renders its output');const raw=tool.querySelector('.tool-raw');check(!raw.querySelector('pre'),'raw JSON remains deferred');raw.open=true;await settle();check(!!raw.querySelector('pre'),'opening raw details renders JSON');tool.open=false;await settle();}
    root.querySelector('.latest-button').click();await pause(250);await settle();
    check(h.scrollHeight-h.clientHeight-h.scrollTop<2,'Latest follows the newest message');
    await componentChecks();
    await virtualizerChecks();
    check(errors.length===0,'no browser exceptions: '+errors.join('; '));
    results.textContent+='DONE scrolling checks\n';
  }catch(error){results.textContent+='STOP '+error.stack+'\n';}
}
document.querySelector('#run').onclick=run;
document.querySelector('#capture').onclick=async()=>{const h=history();scroll(h.scrollHeight-h.clientHeight*3);await settle();results.textContent='Reading synthetic history. Scroll to inspect continuity and the scrollbar.';};

async function componentChecks(){
  const scroller=document.createElement('div'),host=document.createElement('div'),live=new Set();
  scroller.style.cssText='position:fixed;left:-20000px;top:0;width:600px;height:240px;overflow:auto';host.className='message-window';scroller.append(host);document.body.append(scroller);
  const rows=Array.from({length:240},(_,i)=>({id:`bounded-${i}`,viewKey:`bounded-${i}`,role:'user'})),heights=new Map(rows.map(row=>[row.viewKey,136]));
  const windowed=createMessageWindow({scroller,host,heights,requestRender:()=>{},render(item,existing){if(existing)return existing;const row=document.createElement('article');row.className='message';row.style.cssText='height:120px;overflow:hidden;white-space:pre';row.textContent='Synthetic retained text. '.repeat(12000);const entry={row};live.add(entry);return entry;},dispose(entry){entry.row.remove();live.delete(entry);}});
  try{
    for(let i=0;i<18;i++){
      scroller.scrollTop=i*1300;windowed.setMessages(rows);
      const cached=[...live].filter(entry=>!entry.row.isConnected);
      if(cached.length)throw Error('A retired message DOM remained cached in memory');
    }
    check(true,'retired message DOM is released throughout long traversal');
    windowed.setMessages([]);check(live.size===0,'data/owner retirement disposes every mounted row');
    const payload='x'.repeat(1100000),heavy=rows.map(row=>({...row,text:payload}));
    windowed.setMessages(heavy);scroller.scrollTop=8000;windowed.setMessages(heavy);
    check([...live].every(entry=>entry.row.isConnected),'large deferred message payloads do not retain detached DOM');
    windowed.setMessages([]);
    windowed.setMessages(rows);windowed.close();check(live.size===0,'closing the window releases every mounted row');
  }finally{windowed.close();scroller.remove();}
  const call=createToolCall(),tool={id:'mutable',name:'fixture_tool',phase:'running',args:{},output:[{text:'initial output'}]};
  document.body.append(call.element);call.element.open=true;call.update({tool});
  tool.output[0].text+=' updated';call.update({tool});
  check(call.element.textContent.includes('initial output updated'),'in-place Task output updates invalidate expanded tool content');
  tool.output[0].text='Synthetic output line\n'.repeat(3000);call.update({tool});
  const outputText=call.element.querySelector('.tool-output'),output=outputText.closest('.tool-section');
  outputText.scrollTop=0;outputText.dispatchEvent(new Event('scroll'));tool.output[0].text+='LATEST TAIL';call.update({tool});
  check(!outputText.textContent.includes('LATEST TAIL'),'paused long tool output retains its displayed text');
  output.querySelector('.tool-section-heading button:last-child').click();
  check(outputText.textContent.endsWith('LATEST TAIL'),'tool output Latest invalidates the deferred body and reveals new output');
  call.element.remove();
  const taskRoot=document.createElement('div');taskRoot.className='page-content';taskRoot.style.cssText='position:fixed;left:-20000px;top:0;width:800px;height:700px';document.body.append(taskRoot);
  const task={id:'scroll-task',phase:'running'},record={info:{phase:'running'},request:{text:'Synthetic task'},capabilities:{version:1,operations:['task.read','task.input']}};
  const messages=Array.from({length:400},(_,i)=>({id:`task-${i}`,sequence:i+1,role:'assistant',text:'Synthetic task history. '.repeat(12),phase:'complete'}));
  const active={id:'task-live',sequence:401,role:'assistant',text:'First task output',phase:'streaming'};messages.push(active);
  const state={cursor:401,messages,active,tools:new Map(),inputs:new Map()};
  const taskUI=createTaskConversation({root:taskRoot,task,record,locale:()=>document.documentElement.lang,client,scope:client.scope(),draft:'',onDraft:()=>{},toolMemory:new Map(),approvalFor:()=>null});
  try{
    taskUI.update({state});taskUI.start();await settle();
    check(taskRoot.querySelectorAll('article.message').length<100,'long Task transcripts retain bounded DOM windows');
    active.text+=' with a streamed continuation';state.cursor++;taskUI.update({state});await settle();
    check(taskRoot.textContent.includes('First task output with a streamed continuation'),'Task projection refreshes in-place streaming updates');
    const row=taskRoot.querySelector('[data-message-id="task-live"]');taskUI.history.dispatchEvent(new Event('scroll'));await settle();
    check(taskRoot.querySelector('[data-message-id="task-live"]')===row,'Task viewport refresh retains its current message node');
  }finally{taskUI.dispose();taskRoot.remove();}
}

// Exercise the shared adapter's Chat contract independently of server paging.
async function virtualizerChecks(){
  const scroller=document.createElement('div'),host=document.createElement('div');
  scroller.style.cssText='position:fixed;left:-20000px;top:0;width:600px;height:240px;overflow:auto;overflow-anchor:none';
  host.className='message-window';scroller.append(host);document.body.append(scroller);
  let rows=Array.from({length:160},(_,index)=>({id:`anchor-${index}`,viewKey:`anchor-${index}`,role:'user',height:80+index%4*35}));
  let windowed,renderFrame=0;
  windowed=createMessageWindow({scroller,host,requestRender:()=>{if(!renderFrame)renderFrame=requestAnimationFrame(()=>{renderFrame=0;windowed.setMessages(rows);});},render(item,record){
    if(!record){const row=document.createElement('article');row.className='message';row.append(document.createElement('button'));record={row};}
    record.row.style.height=`${item.height}px`;record.row.dataset.messageId=item.id;return record;
  },dispose:record=>record.row.remove()});
  const visible=()=>[...host.querySelectorAll('article')].find(row=>{const box=row.getBoundingClientRect(),top=scroller.getBoundingClientRect().top;return box.bottom>top&&box.top<top+scroller.clientHeight;});
  const offset=row=>row.getBoundingClientRect().top-scroller.getBoundingClientRect().top;
  const move=async top=>{scroller.dispatchEvent(new WheelEvent('wheel',{deltaY:top-scroller.scrollTop||-1}));scroller.scrollTop=top;scroller.dispatchEvent(new Event('scroll'));await settle();};
  try{
    windowed.scrollToLatest();windowed.setMessages(rows);await settle();
    check(scroller.scrollHeight-scroller.clientHeight-scroller.scrollTop<2,'TanStack opens a dynamic-height list at its end');
    await move(scroller.scrollHeight*.45);const saved=visible(),savedOffset=offset(saved);
    rows=[...Array.from({length:30},(_,index)=>({id:`prepend-${index}`,viewKey:`prepend-${index}`,role:'user',height:110+index%3*20})),...rows];
    windowed.setMessages(rows);await settle();
    check(saved.isConnected&&Math.abs(offset(saved)-savedOffset)<2,`historical prepend preserves the keyed visible message (${savedOffset} → ${offset(saved)})`);
    const focused=visible();focused.querySelector('button').focus({preventScroll:true});await move(scroller.scrollTop+5000);
    check(focused.isConnected&&focused.contains(document.activeElement),'focused message remains mounted outside the virtual range');
    rows=[{id:'focused-prepend',viewKey:'focused-prepend',role:'user',height:150},...rows];windowed.setMessages(rows);await settle();
    check(focused.isConnected&&focused.contains(document.activeElement),'prepending history retains focus by message key after indexes shift');
    document.activeElement.blur();windowed.scrollToLatest();windowed.setMessages(rows);await settle();
    rows=rows.map((row,index)=>index===rows.length-1?{...row,height:row.height+300}:row);windowed.setMessages(rows);await settle();
    check(scroller.scrollHeight-scroller.clientHeight-scroller.scrollTop<2,'streaming row growth keeps an end-pinned viewport at the end');
    await move(scroller.scrollTop-1000);const reading=visible(),readingOffset=offset(reading);
    rows=[...rows,{id:'appended',viewKey:'appended',role:'user',height:180}];windowed.setMessages(rows);await settle();
    check(reading.isConnected&&Math.abs(offset(reading)-readingOffset)<2,'appended output does not pull a historical reader to the end');
    const targetID=rows.at(-1).id;
    windowed.locate({id:targetID});windowed.setMessages(rows);await settle();
    rows=[...rows].reverse();windowed.setMessages(rows);await settle();
    check(!!host.querySelector(`[data-message-id="${targetID}"]`),'reordering rows preserves the retained navigation target');
    rows=rows.slice(0,24);windowed.setMessages(rows);await settle();
    check(!!host.querySelector(`[data-message-id="${targetID}"]`),'trimming history resolves a retained navigation target by message identity');
    windowed.scrollToLatest();windowed.setMessages(rows);await settle();
    rows=rows.slice(-2);windowed.setMessages(rows);await settle();
    check(scroller.scrollHeight-scroller.clientHeight-scroller.scrollTop<2,'an end target follows the current list after it shrinks');
    windowed.locate({id:targetID});windowed.setMessages(rows);await settle();
    rows=[];windowed.setMessages(rows);await settle();
    rows=[{id:'replacement',viewKey:'replacement',role:'user',height:180}];windowed.setMessages(rows);await settle();
    check(!!host.querySelector('[data-message-id="replacement"]'),'a removed target cannot break an empty or replacement window');
  }finally{cancelAnimationFrame(renderFrame);windowed.close();scroller.remove();}
}
