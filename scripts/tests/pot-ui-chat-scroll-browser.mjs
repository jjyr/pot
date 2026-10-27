// Production Chat, synthetic long tool output and delayed bidirectional pages.
// Serve the repository over loopback; all assertions run in the browser itself.
import {mountApp} from '../../web/src/components/ui.js';
import {createMessageWindow} from '../../web/src/components/message-window.mjs';
import {createToolCall} from '../../web/src/components/tool-call.mjs';

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
if(new URLSearchParams(location.search).get('contract')!=='window')mountApp(root,client);
const history=()=>root.querySelector('.history:not([data-preparation-surface])');
function scroll(top){const h=history();h.scrollTop=top;h.dispatchEvent(new Event('scroll'));}
const check=(value,label)=>{results.textContent+=`${value?'PASS':'FAIL'} ${label}\n`;};
async function run(){
  results.textContent='Running…\n';
  try{
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
    for(let i=0;i<3;i++){
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
  const settled=async()=>{for(let attempt=0;attempt<90;attempt++){await frame();if(windowed.prepared&&!renderFrame){await frame();if(windowed.prepared&&!renderFrame)return;}}throw Error('Prepared virtual range did not settle');};
  const visible=()=>[...host.querySelectorAll('article')].find(row=>{const box=row.getBoundingClientRect(),top=scroller.getBoundingClientRect().top;return box.bottom>top&&box.top<top+scroller.clientHeight;});
  const offset=row=>row.getBoundingClientRect().top-scroller.getBoundingClientRect().top;
  const move=async top=>{scroller.dispatchEvent(new KeyboardEvent('keydown',{key:'PageUp',bubbles:true}));scroller.scrollTop=top;scroller.dispatchEvent(new Event('scroll'));await settled();};
  try{
    windowed.scrollToLatest();windowed.setMessages(rows);await settled();
    check(scroller.scrollHeight-scroller.clientHeight-scroller.scrollTop<2,'TanStack opens a dynamic-height list at its end');
    await move(scroller.scrollHeight*.45);const saved=visible(),savedOffset=offset(saved);
    rows=[...Array.from({length:30},(_,index)=>({id:`prepend-${index}`,viewKey:`prepend-${index}`,role:'user',height:110+index%3*20})),...rows];
    windowed.setMessages(rows);await settled();
    check(saved.isConnected&&Math.abs(offset(saved)-savedOffset)<2,`historical prepend preserves the keyed visible message (${savedOffset} → ${offset(saved)})`);
    const focused=visible();focused.querySelector('button').focus({preventScroll:true});await move(scroller.scrollTop+5000);
    check(focused.isConnected&&focused.contains(document.activeElement),'focused message remains mounted outside the virtual range');
    rows=[{id:'focused-prepend',viewKey:'focused-prepend',role:'user',height:150},...rows];windowed.setMessages(rows);await settled();
    check(focused.isConnected&&focused.contains(document.activeElement),'prepending history retains focus by message key after indexes shift');
    document.activeElement.blur();windowed.scrollToLatest();windowed.setMessages(rows);await settled();
    rows=rows.map((row,index)=>index===rows.length-1?{...row,height:row.height+300}:row);windowed.setMessages(rows);await settled();
    check(scroller.scrollHeight-scroller.clientHeight-scroller.scrollTop<2,'streaming row growth keeps an end-pinned viewport at the end');
    await move(scroller.scrollTop-1000);const reading=visible(),readingOffset=offset(reading);
    rows=[...rows,{id:'appended',viewKey:'appended',role:'user',height:180}];windowed.setMessages(rows);await settled();
    check(reading.isConnected&&Math.abs(offset(reading)-readingOffset)<2,'appended output does not pull a historical reader to the end');
    const targetID=rows.at(-1).id;
    windowed.setMessages(rows);windowed.locate({id:targetID});await settled();
    rows=[...rows].reverse();windowed.setMessages(rows);await settled();
    check(!!host.querySelector(`[data-message-id="${targetID}"]`),'reordering rows preserves the retained navigation target');
    rows=rows.slice(0,24);windowed.setMessages(rows);await settled();
    check(!!host.querySelector(`[data-message-id="${targetID}"]`),'trimming history resolves a retained navigation target by message identity');
    windowed.scrollToLatest();windowed.setMessages(rows);await settled();
    rows=rows.slice(-2);windowed.setMessages(rows);await settled();
    check(scroller.scrollHeight-scroller.clientHeight-scroller.scrollTop<2,'an end target follows the current list after it shrinks');
    windowed.setMessages(rows);windowed.locate({id:targetID});await settled();
    rows=[];windowed.setMessages(rows);await settled();
    rows=[{id:'replacement',viewKey:'replacement',role:'user',height:180}];windowed.setMessages(rows);await settled();
    check(!!host.querySelector('[data-message-id="replacement"]'),'a removed target cannot break an empty or replacement window');
  }finally{cancelAnimationFrame(renderFrame);windowed.close();scroller.remove();}
}
