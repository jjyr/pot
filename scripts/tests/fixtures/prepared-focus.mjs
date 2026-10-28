import {createMessageWindow} from '/components/message-window.mjs';
import {createReplyBubble} from '/components/reply.mjs';
import {retirePreparedTree} from '/components/prepared-render.mjs';
await document.fonts.ready;
const app=document.querySelector('#app');app.className='pot-app';app.style.cssText='display:block;height:650px';
const scroller=document.createElement('div');scroller.className='history';scroller.style.cssText='height:600px;width:700px';
const column=document.createElement('div');column.className='message-column';const host=document.createElement('div');host.className='message-window';column.append(host);scroller.append(column);app.append(scroller);
const before=document.createElement('button'),after=document.createElement('button');before.id='before';before.textContent='Before live';after.id='after';after.textContent='After live';document.body.prepend(before);app.after(after);
let acquired=0,released=0,loads=0,resolveFiles,phase='initial';const events=[],frames=[],messages=[],boot=[],errors=[],pending=[];
addEventListener('error',e=>errors.push({message:e.message,stack:e.error?.stack}));
const resource={id:'native',name:'native.html',entry:'native.html',mime_type:'text/html',size:100,files:[]};
const options={t:k=>k,downloadResource:async()=>{},messageFiles:()=>new Promise(resolve=>{resolveFiles=resolve;pending.push(resolve);}),acquireResource:()=>{acquired++;let done=false;return {renderURL:token=>'/scripts/tests/fixtures/prepared-focus-document.html?token='+encodeURIComponent(token),release:()=>{if(done)throw Error('Lease released twice');done=true;released++;}};}};
const heights=new Map();let rows=[{id:'native-row',viewKey:'native-row',role:'assistant',text:'',phase:'complete',status:'complete',presentation:{resource,mode:'preview'}}],ui;
function render(message,old){if(!old){const row=document.createElement('article');row.className='message role-assistant';row.dataset.messageId=message.id;const body=document.createElement('div');body.className='message-body';const candidateControl=document.createElement('button');candidateControl.className='candidate-focus-sentinel';candidateControl.textContent='Candidate control';row.append(body,candidateControl);old={row,reply:createReplyBubble(body)};}old.reply.update(message,options);return old;}
function draw(){ui.setMessages(rows);}function mount(){ui=createMessageWindow({scroller,host,heights,render,requestRender:draw,dispose:record=>retirePreparedTree(record.row)});}mount();
const original=()=>host.querySelector('iframe');
function snapshot(label){const active=document.activeElement;return {label,phase,at:performance.now(),prepared:ui.prepared,outerFocus:active===original(),active:active?.id||active?.localName,hiddenFocus:!!active?.closest('[data-preparation-surface]'),surfaces:document.querySelectorAll('[data-preparation-surface]').length,staged:!!original()?.nativePreparation,allSurfacesInert:[...document.querySelectorAll('[data-preparation-surface]')].every(n=>n.closest('[inert]')),nativeInert:original()?.inert,acquired,released,loads};}
addEventListener('focusin',()=>events.push(snapshot('focusin')),true);
addEventListener('message',e=>{if(e.source!==original()?.contentWindow)return;if(e.data?.type==='fixture-native')messages.push({...e.data,outerFocus:document.activeElement===original(),phase,at:performance.now()});if(e.data?.type==='fixture-boot')boot.push(e.data);});
let running=true;function tick(){if(!running)return;frames.push(snapshot('frame'));requestAnimationFrame(tick);}requestAnimationFrame(tick);
window.nativeCase={get ready(){return ui.prepared&&host.querySelector('pot-resource')?.dataset.state==='ready';},get staged(){return !!original()?.nativePreparation&&document.querySelector('[data-preparation-surface] pot-message-file[data-state="loading"]');},messages,boot,errors,events,frames,snapshot,
get measured(){const row=host.querySelector('article'),style=row&&getComputedStyle(row);return row&&{registered:heights.get('native-row'),actual:row.getBoundingClientRect().height+parseFloat(style.marginTop||0)+parseFloat(style.marginBottom||0)};},
start(){phase='pending';window.originalNative=original();original().addEventListener('load',()=>loads++);const inputReturn=new URLSearchParams(location.search).has('input-return');if(inputReturn){scroller.style.height='1600px';app.style.height='1700px';}rows=[{...rows[0],text:inputReturn?'':'[Held synthetic file](pending.md)'}];scroller.style.width='340px';draw();},
resize(){phase='resize-cancel';scroller.style.width='420px';draw();},
release(){phase='released';for(const resolve of pending.splice(0))resolve([]);},
probe(label){original()?.contentWindow.postMessage({type:'fixture-probe',label},'*');},
reopen(){phase='new-instance';heights.clear();mount();running=true;requestAnimationFrame(tick);draw();},
close(){phase='owner-retired';ui.close();running=false;return {snapshot:snapshot('closed'),messages,boot,frames,events,errors,acquired,released,loads,sameNode:original()===window.originalNative};}};
draw();
