// Production Chat, synthetic long tool output and delayed bidirectional pages.
// Serve the repository over loopback; all assertions run in the browser itself.
import {mountApp} from '../../web/src/components/ui.js';
import {createMessageWindow} from '../../web/src/components/message-window.mjs';
import {createToolCall} from '../../web/src/components/tool-call.mjs';
import {resolveReadingPosition} from '../../web/src/components/reading-position.mjs';

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
  const rows=Array.from({length:24},(_,i)=>({id:`bounded-${i}`,viewKey:`bounded-${i}`,role:'user'})),heights=new Map(rows.map(row=>[row.viewKey,136]));
  const windowed=createMessageWindow({scroller,host,heights,requestRender:()=>{},render(item,existing){if(existing)return existing;const row=document.createElement('article');row.className='message';row.style.cssText='height:120px;overflow:hidden;white-space:pre';row.textContent='Synthetic retained text. '.repeat(120);const entry={row};live.add(entry);return entry;},dispose(entry){entry.row.remove();live.delete(entry);}});
  const prepared=async()=>{for(let n=0;n<120;n++){await frame();if(windowed.prepared)return;}throw Error('Component rows did not prepare');};
  try{
    windowed.setMessages(rows);await prepared();
    for(let i=0;i<3;i++){
      scroller.scrollTop=i*1300;windowed.setMessages(rows);await prepared();
      check(live.size===rows.length,'all existing rows remain mounted during traversal');
    }
    windowed.reset();check(live.size===0,'explicit reset disposes every mounted row');
    const payload='x'.repeat(11000),heavy=rows.map(row=>({...row,text:payload}));
    windowed.setMessages(heavy);await prepared();scroller.scrollTop=8000;windowed.setMessages(heavy);await prepared();
    check([...live].every(entry=>entry.row.isConnected),'large deferred message payloads do not retain detached DOM');
    windowed.reset();
    windowed.setMessages(rows);await prepared();windowed.close();check(live.size===0,'closing the window releases every mounted row');
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

// Regression: stable append/prepend mounting must never detach existing rows.
async function virtualizerChecks(){
  const scroller=document.createElement('div'),host=document.createElement('div');
  scroller.style.cssText='position:fixed;left:-20000px;top:0;width:600px;height:240px;overflow:auto;overflow-anchor:none';
  host.className='message-window';scroller.append(host);document.body.append(scroller);
  const makeRow=index=>({id:`anchor-${index}`,viewKey:`anchor-${index}`,role:'user',sequence:String(index+100),height:80+index%4*35});
  let rows=Array.from({length:160},(_,index)=>makeRow(index));
  let windowed,renderFrame=0;
  const originals=new Set();
  let orderBefore=[];
  const originalParents=new Map();
  const removed=[];
  const observer=new MutationObserver(mutations=>{
    for(const mutation of mutations){
      for(const node of mutation.removedNodes){
        if(node.nodeType!==1)continue;
        if(originals.has(node)){removed.push(node);continue;}
        if(node.querySelector&&[...originals].some(row=>node.contains(row)))removed.push(node);
      }
    }
  });
  observer.observe(host,{childList:true,subtree:true});
  const anchorIndex=80;
  let anchorToken;
  windowed=createMessageWindow({scroller,host,requestRender:()=>{if(!renderFrame)renderFrame=requestAnimationFrame(()=>{renderFrame=0;windowed.setMessages(rows);});},render(item,record){
    if(!record){
      const row=document.createElement('article');row.className='message';row.dataset.messageId=item.id;
      const button=document.createElement('button');button.type='button';button.textContent='focus';
      const span=document.createElement('span');span.textContent=`row ${item.id} ${item.sequence}`;
      row.append(button,span);
      if(item.id===`anchor-${anchorIndex}`){
        anchorToken=crypto.randomUUID();
        const frameEl=document.createElement('iframe');
        frameEl.srcdoc=`<input id="probe" value="kept"><script>window.token=${JSON.stringify(anchorToken)};</script>`;
        row.append(frameEl);
      }
      record={row,span,button};
    }
    record.row.style.height=`${item.height}px`;
    record.row.dataset.messageId=item.id;
    return record;
  },dispose:record=>record.row.remove()});
  const assertOriginals=()=>{
    for(const row of originals){const parent=row.parentElement;check(!!parent&&parent.isConnected,'original row has connected parent');check(parent===originalParents.get(row),'original row retains original parent');const id=row.dataset.messageId;check(host.querySelector(`[data-message-id="${id}"]`)===row,'original row identity preserved via host query');}
    const current=[...host.querySelectorAll('article')];
    const filtered=current.filter(row=>originals.has(row));
    check(filtered.length===orderBefore.length,'all originals present after mutation');
    check(filtered.every((row,index)=>row===orderBefore[index]),'original relative row order preserved');
    check(removed.length===0,'no original row subtree removed');
  };
  const settled=async()=>{for(let attempt=0;attempt<90;attempt++){await frame();if(windowed.prepared&&!renderFrame){await frame();if(windowed.prepared&&!renderFrame)return;}}throw Error('Prepared virtual range did not settle');};
  const offset=row=>row.getBoundingClientRect().top-scroller.getBoundingClientRect().top;
  const endDistance=()=>scroller.scrollHeight-scroller.clientHeight-scroller.scrollTop;
  try{
    windowed.locate({id:`anchor-${anchorIndex}`});windowed.setMessages(rows);await settled();
    const anchor=host.querySelector(`[data-message-id="anchor-${anchorIndex}"]`);
    check(!!anchor,'initial historical anchor is mounted');
    const scrollerRect=scroller.getBoundingClientRect(),anchorRect=anchor.getBoundingClientRect();
    check(anchorRect.top<scrollerRect.bottom&&anchorRect.bottom>scrollerRect.top&&anchorRect.left<scrollerRect.right&&anchorRect.right>scrollerRect.left,'initial historical anchor is visible in scroller');
    const anchorOffset=offset(anchor);
    const parent=anchor.parentElement;
  anchor.dataset.scrollAnchor = 'message:' + anchor.dataset.messageId;
  const result = resolveReadingPosition(scroller, {
    top: 0,
    anchors: [{key: 'reply:obsolete-input', offset: 7}],
    anchor: {id: anchor.dataset.messageId, sequence: '180', offset: 7},
  });
check(result.anchor?.key===anchor.dataset.scrollAnchor,'saved legacy key resolves by durable message id');
check(Math.abs(result.top-(scroller.scrollTop+offset(anchor)-7))<=1,'saved native id restores original offset');
    orderBefore=[...host.querySelectorAll('article')];
    for(const row of orderBefore){originals.add(row);originalParents.set(row,row.parentElement);}

    const prepend=Array.from({length:30},(_,index)=>({id:`prepend-${index}`,viewKey:`prepend-${index}`,role:'user',sequence:String(70+index),height:110}));
    rows=[...prepend,...rows];windowed.setMessages(rows);await settled();
    check(anchor.isConnected&&anchor.parentElement===parent,'prepend retains anchor identity and parent');
    check(Math.abs(offset(anchor)-anchorOffset)<=2,`prepend preserves anchor viewport offset (${anchorOffset} → ${offset(anchor)})`);
    assertOriginals();

    const button=anchor.querySelector('button');
    button.focus({preventScroll:true});
    const span=anchor.querySelector('span');
    const range=document.createRange();range.selectNodeContents(span);
    const selection=window.getSelection();selection.removeAllRanges();selection.addRange(range);
    const selectedText=selection.toString();
    check(selectedText.length>0,'selection text is nonempty');
    const iframe=anchor.querySelector('iframe');
    for(let attempt=0;attempt<90&&!(iframe.contentWindow?.token&&iframe.contentDocument?.querySelector('#probe'));attempt++)await frame();
    const tokenBefore=iframe.contentWindow?.token;
    const inputBefore=iframe.contentDocument?.querySelector('#probe')?.value;
    check(tokenBefore===anchorToken,'iframe token initialized before mutation');
    check(inputBefore==='kept','iframe input initialized before mutation');
    iframe.contentDocument.querySelector('#probe').value='edited';

    rows=rows.map(row=>row.id===`anchor-${anchorIndex}`?{...row,height:row.height+120,phase:'streaming'}:row);
    windowed.setMessages(rows);await settled();
    check(anchor.isConnected&&anchor.parentElement===parent,'stream height update retains anchor identity');
    check(selection.toString()===selectedText,'stream update preserves selected text');
    check(document.activeElement===button,'stream update preserves focused button');
    check(iframe.contentWindow?.token===tokenBefore,'stream update preserves iframe token');
    check(iframe.contentDocument?.querySelector('#probe')?.value==='edited','stream update preserves edited iframe input value');
    assertOriginals();

    scroller.dispatchEvent(new KeyboardEvent('keydown',{key:'PageUp',bubbles:true}));
    scroller.scrollTop=Math.max(0,scroller.scrollTop-500);
    scroller.dispatchEvent(new Event('scroll'));await settled();
    const reader=anchor;const readerOffset=offset(reader);
    rows=[...rows,{id:'anchor-300',viewKey:'anchor-300',role:'user',sequence:'300',height:180}];windowed.setMessages(rows);await settled();
    check(reader.isConnected&&Math.abs(offset(reader)-readerOffset)<=2,'paused append preserves historical reader offset');
    assertOriginals();

    windowed.scrollToLatest();windowed.setMessages(rows);await settled();
    rows=[...rows,{id:'anchor-301',viewKey:'anchor-301',role:'user',sequence:'301',height:180}];windowed.setMessages(rows);await settled();
    rows=rows.map((row,index)=>index===rows.length-1?{...row,height:row.height+200}:row);windowed.setMessages(rows);await settled();
    check(endDistance()<=2,'latest follows append and stream row growth');
    assertOriginals();

    for(const top of [0,scroller.scrollHeight/2,scroller.scrollHeight,0]){scroller.scrollTop=top;scroller.dispatchEvent(new Event('scroll'));await settled();}
    check(host.querySelectorAll('article').length===rows.length,'far scrolling keeps every existing row mounted');
    assertOriginals();

    observer.disconnect();
    windowed.reset();
    rows=[{id:'replacement',viewKey:'replacement',role:'user',sequence:'999',height:180}];windowed.setMessages(rows);await settled();
    check(!!host.querySelector('[data-message-id="replacement"]'),'replacement after explicit reset mounts fresh rows');
    check(!anchor.isConnected,'explicit reset disconnects previous rows');
  }finally{observer.disconnect();cancelAnimationFrame(renderFrame);windowed.close();scroller.remove();}
}
