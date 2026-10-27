import {mountApp} from '../../web/src/components/ui.js';
import {createChatCache} from '../../web/src/state/chat-cache.mjs';

const root=document.querySelector('#app'),output=document.querySelector('#results');
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const settle=async()=>{await pause(100);await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));};
const wait=async(test,label)=>{for(let i=0;i<300;i++){if(await test())return;await pause(20);}throw Error('Timeout '+label);};
const check=(value,label)=>{if(!value)throw Error(label);output.textContent+='\nPASS '+label;};
const errors=[];window.addEventListener('error',event=>errors.push(event.message));window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason?.stack||event.reason)));
localStorage.setItem('pot-system-notifications-browser-v1','false');
document.documentElement.dataset.platform='browser';
const reloadKey='pot-chat-cache-reload-contract'+(window.top===window?'':'-embedded');
const reloadState=JSON.parse(sessionStorage.getItem(reloadKey)||'null');
const name=reloadState?.name||'pot-chat-ui-'+crypto.randomUUID(),cache=createChatCache({name});
let scope={space:'one',owner:'alpha'},subscription,held=false,reads=0,dispose,version=1,tail='Latest reply',holdHistory=false,releaseHistory;
window.addEventListener('pagehide',()=>{dispose?.();cache.close();if(!sessionStorage.getItem(reloadKey))indexedDB.deleteDatabase(name);});
const snapshot=owner=>{
  const count=owner==='alpha'?140:12;
  const messages=Array.from({length:count},(_,i)=>({id:`${owner}-${i+1}`,sequence:String(i+1),version,role:i%2?'assistant':'user',...(i%2?{reply_to:`${owner}-${i}`}:{request_id:`${owner}-turn-${i+1}`}),text:i===count-1?tail:`${owner} message ${i+1}. `+'Synthetic message for persisted reading. '.repeat(10),status:owner==='alpha'&&[90,92].includes(i)?'failed':'complete',...(owner==='alpha'&&[90,92].includes(i)?{error:'Historical synthetic error'}:{})}));
  return {runtime_id:'persistent-ui',owner_id:JSON.stringify(['one',owner]),conversation_id:owner,boot_id:'boot',cursor:String(version),kind:'reset',base_cursor:0,messages,replies:[],running:false,turn_id:null,history:{before:'1',after:String(count),has_more:false,has_newer:false}};
};
const key=owner=>JSON.stringify(['persistent-ui','one',owner]);
const client={chatCacheName:name,production:true,runtime:{runtime_id:'persistent-ui',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},
 subscribe(receive,status){const owner=scope.owner,record={owner,receive};subscription=record;const timer=setTimeout(()=>{if(!held){status('connected');receive(snapshot(owner));record.ready=true;}},250);return()=>clearTimeout(timer);},
 history:async()=>{const owner=scope.owner;reads++;if(holdHistory)await new Promise(resolve=>{releaseHistory=resolve;});return snapshot(owner);},
 async request(path){const url=new URL(path,location.href);
  if(url.pathname==='/api/v1/spaces')return {active_space_id:'one',items:[{id:'one',name:'Fixture',control_id:'persistent-ui',connected:true}]};
  if(url.pathname==='/api/v1/assistants')return {items:[{id:'alpha',name:'Alpha'},{id:'beta',name:'Beta'}].map(item=>({...item,capabilities:{version:1,operations:['chat.read','chat.send','assistant.configure']}}))};
  return {items:[],pending:[],policy:{}};
 }};
const h=()=>root.querySelector('.history');
const shown=()=>h()&&getComputedStyle(h()).visibility!=='hidden'&&!h().hidden&&!root.classList.contains('show-directory');
const last=()=>root.querySelector(`[data-message-id="${scope.owner}-${scope.owner==='alpha'?140:12}"]`);
const atLatest=()=>h().scrollHeight-h().clientHeight-h().scrollTop<2&&!!last();
const visibleError=()=>[...h().querySelectorAll('.reply-bubble.is-failed')].some(node=>{const r=node.getBoundingClientRect(),box=h().getBoundingClientRect();return r.bottom>box.top&&r.top<box.bottom;});
async function select(owner,{target}={}){
 if(matchMedia('(max-width:719px)').matches&&!root.classList.contains('show-directory')){root.querySelector('.titlebar .narrow-navigation').click();await settle();}
 root.querySelector(`.assistant-row[data-view-key='["one","${owner}"]']`).click();await wait(()=>scope.owner===owner,'selected '+owner);
 await wait(()=>shown()&&(target?h().querySelector(`[data-message-id="${target}"]`):last()),'restored '+owner);await settle();
}
function anchor(){const top=h().getBoundingClientRect().top;const node=[...h().querySelectorAll('article.message')].find(row=>{const box=row.getBoundingClientRect();return box.bottom>top&&box.top<top+h().clientHeight;});return {id:node?.dataset.messageId,offset:node?.getBoundingClientRect().top-top};}
async function verifyFrames(action,validate,label){
 let sampling=true,failure='',count=0;
 const tick=()=>{if(!sampling)return;if(shown()){count++;try{validate();}catch(reason){failure ||= reason.message;}}requestAnimationFrame(tick);};requestAnimationFrame(tick);
 try{await action();await settle();}finally{sampling=false;}
 check(count>0&&!failure,label+(failure?' — '+failure:''));
}
async function run(){
 dispose=mountApp(root,client);await wait(()=>shown()&&last(),'initial committed chat');await settle();
 check(atLatest()&&!visibleError(),'initial frame follows latest without showing historical errors');
 // Hold a real read/write transaction open. Backend input may arrive, but the
 // renderer must keep the previous body until the durable write can commit.
 const db=await new Promise((resolve,reject)=>{const open=indexedDB.open(name);open.onsuccess=()=>resolve(open.result);open.onerror=()=>reject(open.error);});
 let release=false;const tx=db.transaction(['conversations','messages','views'],'readwrite');
 const pump=()=>{const request=tx.objectStore('conversations').get(key('alpha'));request.onsuccess=()=>{if(!release)pump();};};pump();
 tail='Committed streaming reply';version++;
 const next=snapshot('alpha');subscription.receive({...next,kind:'delta',base_cursor:'1',messages:[next.messages.at(-1)]});
 await pause(120);check(!h().textContent.includes(tail),'an uncommitted streaming update never reaches the DOM');
 release=true;await new Promise((resolve,reject)=>{tx.oncomplete=resolve;tx.onerror=()=>reject(tx.error);});db.close();
 await wait(()=>h().textContent.includes(tail),'committed streaming reply');
 check((await cache.read(key('alpha'))).snapshot.messages.some(message=>message.text===tail),'displayed streaming output is already persisted');
 await verifyFrames(async()=>{await select('beta');await select('alpha');},()=>{if(!atLatest()||visibleError())throw Error('wrong first viewport');},'assistant return restores latest before every visible frame');
 await wait(()=>shown()&&!h().querySelector('.message-window[aria-busy]')&&last()?.getBoundingClientRect().height>=24,'readable committed viewport before scrollbar input');
 const previousRows=[...h().querySelectorAll('article.message')],started=performance.now();let watching=true,lostPrevious=false;
 const track=()=>{if(!watching)return;if(h().querySelector('.message-window[aria-busy]')&&previousRows.some(row=>!row.isConnected))lostPrevious=true;requestAnimationFrame(track);};requestAnimationFrame(track);
 h().dispatchEvent(new WheelEvent('wheel',{deltaY:-100,bubbles:true}));h().scrollTop=h().scrollHeight*.4;h().dispatchEvent(new Event('scroll'));await settle();
 // A direct scrollbar jump can enter an unmounted range. Wait for its prepared
 // rows instead of treating the old fixed settle delay as rendering readiness.
 await wait(()=>!!anchor().id&&!h().querySelector('.message-window[aria-busy]'),'prepared historical viewport');
 watching=false;window.cacheViewportEvidence={commitMilliseconds:performance.now()-started,lostPrevious};
 const reading=anchor();check(!!reading.id&&!root.querySelector('.latest-button').hidden&&!lostPrevious,'historical reading is paused');
 await select('beta');
 await verifyFrames(()=>select('alpha',{target:reading.id}),()=>{if(scope.owner!=='alpha')return;const node=h().querySelector(`[data-message-id="${reading.id}"]`);if(!node||Math.abs(node.getBoundingClientRect().top-h().getBoundingClientRect().top-reading.offset)>3)throw Error('reading anchor shifted');},'historical reading restores from disk before exposure');
 holdHistory=true;version++;
 await verifyFrames(async()=>{
   const refresh=snapshot('alpha');subscription.receive({...refresh,messages:refresh.messages.slice(-20),history:{before:'121',after:'140',has_more:true,has_newer:false}});
   await wait(()=>!!releaseHistory,'history recovery started');await pause(100);
   holdHistory=false;releaseHistory();
   await wait(async()=>!!(await cache.read(key('alpha'))).snapshot.messages.find(message=>message.id===reading.id),'historical recovery committed');
 },()=>{const node=h().querySelector(`[data-message-id="${reading.id}"]`);if(!node||Math.abs(node.getBoundingClientRect().top-h().getBoundingClientRect().top-reading.offset)>3)throw Error('refresh exposed latest before restoring history');},'a latest refresh preserves the historical viewport while its backend page is delayed');
 await pause(180);dispose();await settle();held=true;
 // A fresh mount has no conversation state or DOM from the previous instance.
 dispose=mountApp(root,client);await wait(()=>shown()&&h().querySelector(`[data-message-id="${reading.id}"]`),'persisted reload without backend');
 const returned=h().querySelector(`[data-message-id="${reading.id}"]`);
 check(Math.abs(returned.getBoundingClientRect().top-h().getBoundingClientRect().top-reading.offset)<3,'a fresh application mount restores its persisted reading position before subscription');
 check(h().textContent.length>0&&!subscription.ready,'cached chat is readable while the backend subscription is held');
 root.querySelector('.latest-button').click();await settle();
 check(atLatest()&&reads===1,'Latest reads persisted history without another backend request');
 held=false;subscription.receive(snapshot('alpha'));await settle();
 check(errors.length===0,'no browser exceptions: '+errors.join('; '));
 await pause(180);sessionStorage.setItem(reloadKey,JSON.stringify({name,log:output.textContent,readiness:window.cacheViewportEvidence}));dispose();await pause(100);window.top.location.reload();
}
async function verifyReload(){
 output.textContent=reloadState.log;window.cacheViewportEvidence=reloadState.readiness;held=true;
 check(!!(await cache.read(key('alpha')))?.snapshot.messages.length,'committed records survive actual document navigation');
 dispose=mountApp(root,client);await wait(()=>shown()&&last(),'browser reload from IndexedDB');await settle();
 check(atLatest()&&h().textContent.includes('Committed streaming reply')&&!subscription.ready,'a real browser reload restores committed output before backend refresh');
 sessionStorage.removeItem(reloadKey);output.textContent+='\nDONE persistent UI';
}
(reloadState?verifyReload():run()).catch(reason=>output.textContent+='\nFAIL '+reason.stack);
