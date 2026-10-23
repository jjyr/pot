// Shared production Chat with synthetic directories and receipts. No model or
// product data: these checks qualify the browser's composer/draft behavior.
import {mountApp} from '../../web/src/components/ui.js';
import {renderMarkdown} from '../../web/src/components/markdown.mjs';
const root=document.querySelector('#app'),errors=[],checks=[],sends=[];
window.addEventListener('error',event=>errors.push(event.message));window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason?.stack||event.reason)));
localStorage.setItem('pot-system-notifications-browser-v1','false');
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const wait=async test=>{for(let i=0;i<250;i++){if(test())return;await pause(20);}throw Error('Timed out: '+test);};
const check=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
function photo(color,letter){const canvas=document.createElement('canvas');canvas.width=canvas.height=128;const ctx=canvas.getContext('2d');ctx.fillStyle=color;ctx.fillRect(0,0,128,128);ctx.fillStyle='#fff';ctx.font='64px sans-serif';ctx.textAlign='center';ctx.fillText(letter,64,87);return canvas.toDataURL('image/png');}
const images=[photo('#466c52','研'),photo('#7c5c37','码'),photo('#68567c','写')];
const skills=[{token:'/skill:code-review',label:'code-review',detail:'检查代码质量、潜在问题与测试覆盖'},{token:'/skill:research-note',label:'research-note',detail:'整理研究材料，生成结构化笔记'},{token:'/skill:playwright',label:'playwright',detail:'操作浏览器，验证页面交互'},{token:'/machine:forbidden',label:'Machine',detail:'Must not appear'},{token:'/tag:forbidden',label:'Label',detail:'Must not appear'}];
const assistants=[{token:'@assistant:local/research',label:'研究助手',detail:'本机 · Pot',avatar:images[0]},{token:'@assistant:workstation/code',label:'代码助手',detail:'工作站 · Codex',avatar:images[1]},{token:'@assistant:remote/research',label:'研究助手',detail:'远程节点 · Pot',avatar:images[2],offline:true}];
let scope={space:'one',owner:'alpha'},cursor=1,receive,release,reject=false;
const frame=()=>({kind:'reset',runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify(['one',scope.owner]),conversation_id:scope.owner,cursor:String(cursor),replies:[],running:false,turn_id:null,messages:[{id:'welcome',sequence:'1',role:'assistant',text:'我会结合代码变更，整理需要关注的问题。',status:'complete'}],history:{has_more:false,has_newer:false}});
const client={production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},
 subscribe(callback,status){receive=callback;const timer=setTimeout(()=>{status('connected');receive(frame());},10);return()=>clearTimeout(timer);},
 async send(request,options){sends.push({request,scope:options});if(reject)throw Object.assign(Error('Synthetic rejection'),{status:400});if(release)await new Promise(resolve=>release=resolve);return {message_id:'accepted-'+sends.length,duplicate:false};},
 async request(path){const url=new URL(path,location.href);
  if(url.pathname==='/api/v1/spaces')return {active_space_id:'one',items:[{id:'one',name:'Fixture',control_id:'fixture',connected:true}]};
  if(url.pathname==='/api/v1/assistants')return {items:[{id:'alpha',name:'工作助手',avatar:images[1]},{id:'beta',name:'写作助手',avatar:images[2]}].map(item=>({...item,capabilities:{version:1,scope:'owner',operations:['chat.read','chat.send','chat.completions']}}))};
  if(url.pathname==='/api/v1/completions'){const q=url.searchParams.get('q');if(q==='/slow'){await new Promise(resolve=>release=resolve);return {items:skills};}const items=q.startsWith('@')?assistants:skills;return {items:items.filter(item=>q==='/'||q==='@'||(item.token+' '+item.label+' '+item.detail).includes(q.slice(1)))};}
  return {items:[],pending:[],policy:{}};
 }};
mountApp(root,client);
const input=()=>root.querySelector('#pot-composer'),menu=()=>root.querySelector('.completion-list'),options=()=>[...root.querySelectorAll('.completion-option')],chips=()=>[...root.querySelectorAll('.composer-selector')];
function type(value){input().focus();input().value=value;input().setSelectionRange(value.length,value.length);input().dispatchEvent(new Event('input',{bubbles:true}));}
function press(key){const event=new KeyboardEvent('keydown',{key,bubbles:true,cancelable:true});input().dispatchEvent(event);input().dispatchEvent(new KeyboardEvent('keyup',{key,bubbles:true}));return event;}
async function select(owner){if(matchMedia('(max-width:719px)').matches&&!root.classList.contains('show-directory')){root.querySelector('.narrow-navigation').click();await pause(30);}root.querySelector(`.assistant-row[data-view-key='["one","${owner}"]']`).click();await wait(()=>scope.owner===owner&&!input().disabled);await pause(70);}
async function run(){
 await wait(()=>input()&&!input().disabled&&!root.querySelector('.history').hidden);await pause(100);
 type('/');await wait(()=>options().length===3);check(!menu().querySelector('.avatar,svg'),'Skills have no icons');check(options().every(option=>option.getBoundingClientRect().height<=44),'Compact rows remain at most 44px');check(!menu().textContent.includes('forbidden'),'Slash excludes machines and labels');
 press('Escape');await pause(30);check(menu().hidden,'Escape stays dismissed after keyup');
 type('/nothing-matches');await wait(()=>!menu().hidden&&options().length===0);press('Enter');check(sends.length===0,'Empty results cannot accidentally send a slash query');
 type('person@example.com');check(menu().hidden,'Email addresses do not open mentions');type('/tmp/project');check(menu().hidden,'Paths do not open skill completion');
 type('/code');await wait(()=>options().length===1);const before=sends.length;press('Enter');await wait(()=>chips().length===1);check(input().value===''&&sends.length===before,'Enter inserts a removable skill chip without sending');
 type('@');await wait(()=>options().length===3);check(options().every(option=>option.querySelector('.avatar img')),'Assistants use saved avatars with names');check(options()[2].getAttribute('aria-disabled')==='true','Offline assistant is visibly unavailable');
 options()[2].click();check(chips().length===1,'Offline selection cannot add a mention');press('ArrowDown');check(options()[1].getAttribute('aria-selected')==='true','Keyboard navigation selects the next available assistant');press('Enter');await wait(()=>chips().length===2);check(chips()[1].textContent.includes('代码助手'),'Selected assistant keeps its display name');
 type('请一起检查这个 PR');await select('beta');check(chips().length===0&&input().value==='','Switching owner does not leak selectors');await select('alpha');check(chips().length===2&&input().value==='请一起检查这个 PR','Returning restores both selector types and draft text');
 type('@');await wait(()=>options().length===3);const old=input();input().dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));check(menu().hidden,'IME preedit hides completion');const ime=press('Enter');check(!ime.defaultPrevented&&sends.length===before,'IME confirmation cannot select or send');input().dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));check(input()===old,'IME retains the native editor');await pause(100);type('请一起检查这个 PR');
 reject=true;root.querySelector('.composer .send-button').click();await wait(()=>sends.length===before+1);await pause(50);check(chips().length===2&&input().value==='请一起检查这个 PR','Rejected send keeps selector chips and draft');reject=false;
 release=true;root.querySelector('.composer .send-button').click();await wait(()=>typeof release==='function');const sent=sends.at(-1);check(sent.scope.owner==='alpha'&&sent.request.conversation_id==='alpha','Mention keeps current assistant as the sole send target');check(sent.request.text==='/skill:code-review\n[@代码助手](pot-assistant:workstation/code)\n请一起检查这个 PR'||sent.request.text==='/skill:code-review [@代码助手](pot-assistant:workstation/code)\n请一起检查这个 PR','Send includes skill and complete assistant identity');
 type('稍后继续');release();release=null;await pause(80);check(input().value==='稍后继续'&&chips().length===2,'Late acceptance retains edited draft and selectors');
 root.querySelector('.composer-selector-remove').click();check(chips().length===1,'Remove only affects the chosen selector');
 const rendered=renderMarkdown(sent.request.text),mention=rendered.querySelector('.assistant-mention');check(mention?.textContent==='@代码助手'&&!mention.querySelector('a'),'History renders a non-navigable assistant mention');
 chips()[0].querySelector('button').click();type('');type('/slow');await pause(10);type('@');await wait(()=>options().length===3);release();release=null;await pause(30);check(menu().textContent.includes('代码助手')&&!menu().textContent.includes('code-review'),'Late slash result cannot overwrite mention results');
 const locale=new URLSearchParams(location.search).get('locale')||'en',alternate=locale==='en'?'zh-CN':'en';window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value:alternate}}));await pause(30);check(menu().textContent.includes(alternate==='en'?'Assistants':'助手'),'Open picker updates locale without losing its query');window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value:locale}}));await pause(30);
 check(document.documentElement.scrollWidth<=innerWidth,'No horizontal overflow at the current width');check(errors.length===0,errors.join('\n'));
 const mode=new URLSearchParams(location.search).get('capture')||'skills';type(mode==='assistants'?'@':'/');await wait(()=>options().length===3);
 const box=menu().getBoundingClientRect(),composer=root.querySelector('.composer').getBoundingClientRect();check(box.left>=0&&box.right<=innerWidth&&box.top>=0&&box.bottom<=composer.top,'Picker stays above the composer and within the viewport');
 window.composerSelectorResults={passed:true,checks,errors};
}
run().catch(error=>window.composerSelectorResults={passed:false,checks,errors:[...errors,String(error.stack||error)]});
