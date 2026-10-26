// Production Chat with independently held historical image reads. No model calls.
import {mountApp} from '../../web/src/components/ui.js';
import {createInitialChatLayout} from '../../web/src/components/initial-chat-layout.mjs';
import {createMessageFile} from '../../web/src/components/message-file.mjs';
const root=document.querySelector('#app'),results=document.querySelector('#results');
const frame=()=>new Promise(resolve=>requestAnimationFrame(resolve));
const wait=async test=>{for(let i=0;i<400;i++){if(test())return;await frame();}throw Error('Timed out: '+test);};
const check=(value,label)=>{if(!value)throw Error(label);results.textContent+='PASS '+label+'\n';};
localStorage.setItem('pot-system-notifications-browser-v1','false');
document.documentElement.dataset.platform='browser';
const canvas=document.createElement('canvas');canvas.width=480;canvas.height=280;canvas.getContext('2d').fillRect(0,0,480,280);
const image=URL.createObjectURL(await new Promise(resolve=>canvas.toBlob(resolve)));
let scope={space:'layout',owner:'alpha'},receive,version=1;
const releases=[];
const snapshot=()=>({kind:'reset',runtime_id:'layout',boot_id:'boot',owner_id:JSON.stringify(['layout',scope.owner]),conversation_id:scope.owner,cursor:String(version),replies:[],running:false,turn_id:null,
  messages:Array.from({length:24},(_,index)=>({id:scope.owner+'-'+index,sequence:String(index+1),role:index%2?'assistant':'user',...(index%2?{reply_to:scope.owner+'-'+(index-1)}:{}),text:index%2?'## Measured reply\n\n'+('A paragraph with **Markdown**. '.repeat(index%5+1))+'\n\n```js\nconst ready = true;\n```':'Short question',status:'complete',...(index===22?{attachments:[{id:'image',name:'image.png',mime_type:'image/png',size:1,width:480,height:280}]}:{})})),history:{before:'1',after:'24',has_more:false,has_newer:false}});
const client={production:true,chatCacheName:'initial-layout-'+crypto.randomUUID(),runtime:{runtime_id:'layout',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},
  subscribe(callback,status){receive=callback;queueMicrotask(()=>{status('connected');callback(snapshot());});return()=>{};},
  attachmentURL:()=>new Promise((resolve,reject)=>{const release=()=>resolve(image);release.fail=()=>reject(Error('Offline fixture'));releases.push(release);}),downloadAttachment:async()=>{},
  async request(path){const url=new URL(path,location.href);
    if(url.pathname==='/api/v1/spaces')return {active_space_id:'layout',items:[{id:'layout',name:'Fixture',control_id:'layout',connected:true}]};
    if(url.pathname==='/api/v1/assistants')return {items:[{id:'alpha',name:'Alpha'},{id:'beta',name:'Beta'}]};
    return {items:[],pending:[],policy:{}};
  },
};
let dispose;
try{
  dispose=mountApp(root,client);
  const history=()=>root.querySelector('.history'),visible=()=>getComputedStyle(history()).visibility!=='hidden'&&!history().hidden;
  const select=async owner=>{if(matchMedia('(max-width:719px)').matches&&!root.classList.contains('show-directory'))root.querySelector('.narrow-navigation').click();root.querySelector(`[data-view-key='["layout","${owner}"]']`).click();await wait(()=>scope.owner===owner);};
  await wait(()=>releases.length===1);for(let i=0;i<8;i++)await frame();
  check(!visible(),'Initial history remains hidden while its image has no final dimensions');
  releases.shift()();await wait(visible);
  const geometry=()=>{const h=history();return JSON.stringify([h.scrollTop,h.scrollHeight,...[...h.querySelectorAll('article.message')].map(row=>[row.dataset.messageId,row.getBoundingClientRect().y,row.getBoundingClientRect().height])]);};
  const first=geometry();for(let i=0;i<12;i++)await frame();
  check(geometry()===first&&history().scrollHeight-history().clientHeight-history().scrollTop<2,'First visible history keeps its measured rows and final Latest offset');
  await select('alpha');await wait(()=>releases.length===1);for(let i=0;i<4;i++)await frame();
  check(!visible(),'Returning through the directory prepares newly mounted historical images again');
  releases.shift()();await wait(visible);
  history().dispatchEvent(new WheelEvent('wheel',{deltaY:-300,bubbles:true}));history().scrollTop-=300;history().dispatchEvent(new Event('scroll'));for(let i=0;i<4;i++)await frame();
  const top=history().getBoundingClientRect().top,anchor=[...history().querySelectorAll('article.message')].find(row=>row.getBoundingClientRect().bottom>top),anchorID=anchor.dataset.messageId,anchorY=anchor.getBoundingClientRect().y;
  await select('alpha');await wait(()=>releases.length===1);releases.shift()();await wait(visible);
  const restored=[...history().querySelectorAll('article.message')].find(row=>row.dataset.messageId===anchorID);
  check(restored&&Math.abs(restored.getBoundingClientRect().y-anchorY)<1,'Historical reading keeps its original anchor through provisional image heights');
  root.querySelector('.latest-button').click();await frame();await frame();
  await select('beta');await wait(()=>releases.length===1);await select('alpha');await wait(()=>releases.length===2);
  releases.shift()();for(let i=0;i<8;i++)await frame();
  check(!visible(),'A retired assistant image cannot reveal the next assistant');
  releases.shift()();await wait(visible);
  // A new streaming update must remain visible after the initial preparation.
  version++;const stream=snapshot();stream.messages.at(-1).text+='\n\nStreaming tokens';stream.messages.at(-1).status='streaming';receive(stream);await wait(()=>{if(!visible())throw Error('Streaming hid the conversation');return history().textContent.includes('Streaming tokens');});
  check(visible()&&history().textContent.includes('Streaming tokens'),'Live streaming continues visibly after the initial history');
  await select('beta');await wait(()=>releases.length===1);releases.shift().fail();await wait(visible);
  check(!!history().querySelector('pot-resource[data-state="error"]'),'An offline historical image reveals its recoverable error state');
  await select('alpha');await wait(()=>releases.length===1);dispose();dispose=null;releases.shift()();await frame();await frame();
  check(root.childElementCount===0,'Disposal cancels pending preparation and late resource completion');
  // Short deadline exercises the production timeout policy without a slow test.
  // Hold the real file binding AND its subsequent resource read past reveal.
  // This covers the inline-to-block transition, not just a resource's contents.
  const h=document.createElement('div'),row=document.createElement('article'),body=document.createElement('div'),block=document.createElement('p'),inline=document.createElement('p'),tail=document.createElement('div');
  h.style.cssText='width:min(360px,100%);height:300px;overflow:auto;visibility:hidden';row.className='message';body.className='message-body';tail.textContent='Final history anchor';
  const earlier=document.createElement('div');earlier.style.height='480px';earlier.textContent='Earlier history';
  let bind,read;const binding=new Promise(resolve=>bind=resolve),bytes=new Promise(resolve=>read=resolve);
  const resource={id:'delayed',name:'notes.txt',entry:'notes.txt',size:100,mime_type:'text/plain',files:[],download:{path:'notes.txt'}};
  const options={messageFiles:()=>binding,previewClaims:new Map(),acquireResource:()=>({read:()=>bytes,release(){}})};
  const file=createMessageFile({href:'notes.txt',label:'notes.txt',block:true},'delayed',options),link=createMessageFile({href:'inline.txt',label:'inline.txt',block:false},'delayed',options);
  block.append(file);inline.append('Inline file: ',link);body.append(block,inline);row.append(body);h.append(earlier,row,tail);document.body.append(h);
  const metrics=()=>({row:row.getBoundingClientRect().height,bodyWidth:body.getBoundingClientRect().width,scroll:h.scrollTop,height:h.scrollHeight,tail:tail.getBoundingClientRect().y,file:file.getBoundingClientRect().height,fileWidth:file.getBoundingClientRect().width,linkHeight:link.getBoundingClientRect().height,linkWidth:link.getBoundingClientRect().width});
  let firstLayout,sampling=true;const samples=[];
  const sample=()=>{if(!sampling)return;if(firstLayout)samples.push(metrics());requestAnimationFrame(sample);};requestAnimationFrame(sample);
  const gate=createInitialChatLayout({history:h,layout:()=>{},position:()=>{h.scrollTop=h.scrollHeight;},ready:()=>true,fontFallback:()=>{},timeout:30,reveal:()=>{h.style.visibility='';firstLayout=metrics();}});gate.start();await wait(()=>firstLayout);
  check(firstLayout.file>=240&&firstLayout.linkHeight<40&&getComputedStyle(file).display==='block','Deadline reserves a block preview while keeping inline file links compact');
  bind([{href:'notes.txt',state:'ready',resource},{href:'inline.txt',state:'ready',resource:{...resource,id:'inline'}}]);await wait(()=>file.querySelector('pot-resource')?.dataset.state==='loading');await frame();await frame();
  const bound=metrics();read(new Blob(['Late file content\n'.repeat(200)]));await wait(()=>file.querySelector('pot-resource')?.dataset.state==='ready');for(let i=0;i<4;i++)await frame();
  sampling=false;const ready=metrics();
  check(samples.length>2&&samples.every(value=>JSON.stringify(value)===JSON.stringify(firstLayout))&&JSON.stringify(ready)===JSON.stringify(firstLayout),'Every visible sampled frame keeps row height, scroll and anchor through late binding and resource readiness');
  const expand=file.querySelector('.resource-actions button');expand.click();await frame();await frame();const expanded=metrics();
  check(expanded.file>firstLayout.file&&expanded.row>firstLayout.row,'Explicit Expand releases the containing file-reference slot');
  expand.click();await frame();await frame();const collapsed=metrics();
  check(collapsed.file===firstLayout.file&&collapsed.row===firstLayout.row,'Collapse restores the original reserved file-reference height');
  link.querySelector('a').click();check(!!document.querySelector('dialog[open] pot-resource'),'Late inline file binding remains openable in its dialog');document.querySelector('dialog[open]').close();
  window.initialFileLayoutEvidence={firstLayout,bound,ready,expanded,collapsed,samples};
  gate.cancel();h.remove();results.textContent+='DONE initial Chat layout';
}catch(error){results.textContent+='FAIL '+error.stack;}finally{dispose?.();URL.revokeObjectURL(image);}
