// Shared production Chat with synthetic directories and receipts. No model or
// product data: these checks qualify the browser's composer/draft behavior.
import {createResourceReads} from '../../web/src/state/resource-cache.mjs';
import {messageIdentities} from '../../web/src/state/message-identities.mjs';
import {mountApp} from '../../web/src/components/ui.js';
import {createTaskConversation} from '../../web/src/components/task-conversation.mjs';
import {createTaskTranscript} from '../../web/src/state/task-transcript.mjs';
import {composerMentionValue} from '../../web/src/components/composer-mentions.mjs';
import {renderMarkdown} from '../../web/src/components/markdown.mjs';
const parameters=new URLSearchParams(location.search);
if(parameters.has('mobile'))Object.defineProperty(navigator,'userAgent',{value:'iPhone Mobile fixture'});
const mobile=parameters.has('mobile');
const root=document.querySelector('#app'),errors=[],checks=[],sends=[];
window.addEventListener('error',event=>errors.push(event.message));window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason?.stack||event.reason)));
localStorage.setItem('pot-system-notifications-browser-v1','false');
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const wait=async test=>{for(let i=0;i<250;i++){if(test())return;await pause(20);}throw Error('Timed out: '+test);};
const check=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
function photo(color,letter){const canvas=document.createElement('canvas');canvas.width=canvas.height=128;const ctx=canvas.getContext('2d');ctx.fillStyle=color;ctx.fillRect(0,0,128,128);ctx.fillStyle='#fff';ctx.font='64px sans-serif';ctx.textAlign='center';ctx.fillText(letter,64,87);return canvas.toDataURL('image/png');}
const images=[photo('#466c52','研'),photo('#7c5c37','码'),photo('#68567c','写')];
const skills=[{token:'/skill:code-review',label:'code-review',detail:'检查代码质量、潜在问题与测试覆盖'},{token:'/skill:research-note',label:'research-note',detail:'整理研究材料，生成结构化笔记'},{token:'/skill:playwright',label:'playwright',detail:'操作浏览器，验证页面交互'},{token:'/machine:forbidden',label:'Machine',detail:'Must not appear'},{token:'/tag:forbidden',label:'Label',detail:'Must not appear'}];
const assistants=[{token:'@assistant:local/research',label:'研究助手',detail:'本机 · Pot',avatar:images[0]},{token:'@assistant:workstation/code',label:'代码助手',detail:'工作站 · Codex',avatar:images[1]},{token:'@assistant:cached/research',label:'研究助手',detail:'缓存节点 · Pot',avatar:images[2],offline:true},{token:'@assistant:remote/research',label:'研究助手',detail:'远程节点 · Pot',avatar:images[2]},{token:'@assistant:remote/unicode',label:'设计 师 [托尼] 🙂',detail:'远程节点 · Pot',avatar:images[2]}];
let scope={space:'one',owner:'alpha'},cursor=1,receive,release,reject=false,mentionGate;
const frame=()=>({kind:'reset',runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify(['one',scope.owner]),conversation_id:scope.owner,cursor:String(cursor),replies:[],running:false,turn_id:null,messages:[{id:'welcome',sequence:'1',role:'assistant',text:'我会结合代码变更，整理需要关注的问题。',status:'complete'}],history:{has_more:false,has_newer:false}});
const client={chatCacheName:'pot-composer-selectors-'+crypto.randomUUID(),production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},
 subscribe(callback,status){receive=callback;const timer=setTimeout(()=>{status('connected');receive(frame());},10);return()=>clearTimeout(timer);},
 async send(request,options){sends.push({request,scope:options});if(reject)throw Object.assign(Error('Synthetic rejection'),{status:400});if(release)await new Promise(resolve=>release=resolve);return {message_id:'accepted-'+sends.length,duplicate:false};},
 async request(path){const url=new URL(path,location.href);
  if(url.pathname==='/api/v1/spaces')return {active_space_id:'one',items:[{id:'one',name:'Fixture',control_id:'fixture',connected:true}]};
  if(url.pathname==='/api/v1/assistants')return {items:[{id:'alpha',name:'工作助手',avatar:images[1]},{id:'beta',name:'写作助手',avatar:images[2]}].map(item=>({...item,capabilities:{version:1,scope:'owner',operations:['chat.read','chat.send','chat.completions']}}))};
  if(url.pathname==='/api/v1/completions'){const q=url.searchParams.get('q');if(q==='/slow'){await new Promise(resolve=>release=resolve);return {items:skills};}const items=q.startsWith('@')?assistants:skills;const result={items:structuredClone(items.filter(item=>q==='/'||q==='@'||(item.token+' '+item.label+' '+item.detail).includes(q.slice(1))))};if(q.startsWith('@')&&mentionGate)await new Promise(resolve=>mentionGate=resolve);return result;}
  return {items:[],pending:[],policy:{}};
 }};
client.resources=createResourceReads(client);client.read=(path,options)=>client.resources.read(path,options);
mountApp(root,client);
const input=()=>root.querySelector('#pot-composer'),menu=()=>root.querySelector('.completion-list'),options=()=>menu()?.hidden?[]:[...root.querySelectorAll('.completion-option')],chips=()=>[...root.querySelectorAll('.composer-selector')];
function type(value,caret=value.length){input().focus();input().value=value;input().setSelectionRange(caret,caret);input().dispatchEvent(new Event('input',{bubbles:true}));}
function press(key){const event=new KeyboardEvent('keydown',{key,bubbles:true,cancelable:true});input().dispatchEvent(event);input().dispatchEvent(new KeyboardEvent('keyup',{key,bubbles:true}));return event;}
async function select(owner){if(matchMedia('(max-width:719px)').matches&&!root.classList.contains('show-directory')){root.querySelector('.narrow-navigation').click();await pause(30);}root.querySelector(`.assistant-row[data-view-key='["one","${owner}"]']`).click();await wait(()=>scope.owner===owner&&!input().disabled);await pause(70);}
const code='[@代码助手](pot-assistant:workstation/code)',local='[@研究助手](pot-assistant:local/research)',remote='[@研究助手](pot-assistant:remote/research)';
function insert(text){input().setRangeText(text,input().selectionStart,input().selectionEnd,'end');input().dispatchEvent(new Event('input',{bubbles:true}));}
async function choose(index=0){if(mobile)options()[index].click();else{while(options()[index].getAttribute('aria-selected')!=='true')press('ArrowDown');press('Enter');}await pause(30);}
async function taskChecks(){
 const host=document.createElement('main');host.style.cssText='position:fixed;inset:0;background:var(--background);z-index:100';document.body.append(host);
 let saved='',sent='';const record={info:{phase:'running'},request:{text:'Synthetic task goal'},capabilities:{version:1,operations:['task.read','task.input']}};
 const view=createTaskConversation({root:host,task:{id:'mention-fixture',phase:'running'},record,locale:()=>document.documentElement.lang,client,scope:client.scope(),draft:'/skill:code-review\n'+local+'\n原有指导',onDraft:text=>saved=text,toolMemory:new Map(),approvalFor:()=>null,onSend:async text=>sent=text});
 view.update({record,state:createTaskTranscript(),inputs:[],approvals:[]});view.start();
 const editor=host.querySelector('textarea');
 check(composerMentionValue(editor)===local+'\n原有指导'&&host.querySelectorAll('.composer-selector').length===1,'Task guidance restores Skills above inline assistant references');
 editor.focus();editor.value='指导 @代 结束';editor.setSelectionRange(5,5);editor.dispatchEvent(new Event('input',{bubbles:true}));
 await wait(()=>host.querySelectorAll('.completion-option').length===1);host.querySelector('.completion-option').click();
 check(composerMentionValue(editor)==='指导 '+code+'  结束'&&document.activeElement===editor,'Task guidance replaces only the query and retains focus');
 editor.setRangeText('继续',editor.selectionStart,editor.selectionEnd,'end');editor.dispatchEvent(new Event('input',{bubbles:true}));
 check(saved==='/skill:code-review\n指导 '+code+' 继续 结束','Task draft serializes the complete inline address with unchanged Skill prefix');
 check(host.querySelector('.send-button').disabled,'Retained Task guidance cannot send before fresh confirmation');
 view.update({record,state:createTaskTranscript(),inputs:[],approvals:[],fresh:true});
 host.querySelector('.send-button').click();await wait(()=>sent==='/skill:code-review\n指导 '+code+' 继续 结束');
 check(sent==='/skill:code-review\n指导 '+code+' 继续 结束','Task send receives the inline mention in place');
 await pause(30);
 editor.focus();editor.value='@';editor.setSelectionRange(1,1);editor.dispatchEvent(new Event('input',{bubbles:true}));
 await wait(()=>!host.querySelector('.completion-list').hidden&&host.querySelectorAll('.completion-option').length===5);
 client.resources.invalidate(['assistants'],{space:'unrelated'});
 check(!host.querySelector('.completion-list').hidden,'Task guidance ignores invalidation from another scope');
 const deleted=assistants.splice(1,1)[0];client.resources.invalidate(['agent-links'],scope);
 check(host.querySelector('.completion-list').hidden,'Task guidance immediately retires obsolete options on directory invalidation');
 await wait(()=>host.querySelectorAll('.completion-option').length===4&&!host.querySelector('.completion-list').hidden);
 check(!host.querySelector('.completion-list').textContent.includes(deleted.label),'Task guidance shares the current directory after deletion');
 assistants.splice(1,0,deleted);client.resources.invalidate(['agent-links'],scope);
 await wait(()=>!host.querySelector('.completion-list').hidden&&host.querySelectorAll('.completion-option').length===5);
 view.dispose();host.remove();
}
async function directoryChecks(){
 const original=structuredClone(assistants),profile=messageIdentities(client);
 client.assistants=[...(client.assistants||[]),{id:'research',control_id:'local',name:'研究助手',avatar:images[0]}];
 check(profile.resolve({node_id:'local',kind:'assistant',agent_id:'research'}).name==='研究助手','History learns the original complete identity');
 type('@研究');await wait(()=>options().length===3);
 const obsoleteOption=options()[0];assistants.splice(0,1);mentionGate=true;
 client.resources.invalidate(['assistants'],{space:'one'});
 check(menu().hidden,'Local delete immediately hides the old options before the replacement read');
 obsoleteOption.click();check(composerMentionValue(input())==='@研究','A retired option cannot insert through a detached click handler');
 await wait(()=>typeof mentionGate==='function');const stale=mentionGate;mentionGate=null;
 assistants.splice(assistants.findIndex(item=>item.token==='@assistant:remote/research'),1);
 client.resources.invalidate(['agent-links'],{space:'one'});stale();
 await wait(()=>!menu().hidden&&options().length===1);
 check(options()[0].getAttribute('aria-disabled')==='true'&&options()[0].title.includes('@assistant:cached/research'),'Late pre-delete search cannot revive either node; unrelated offline same-ID assistant remains');
 type('');type('@研究');await wait(()=>!menu().hidden&&options().length===1);
 check(options()[0].title.includes('@assistant:cached/research'),'Reopening the same cached query does not revive deleted definitions');
 client.assistants=client.assistants.filter(item=>!(item.control_id==='local'&&item.id==='research'));
 check(profile.resolve({node_id:'local',kind:'assistant',agent_id:'research'}).name==='研究助手','History keeps a known author name after deletion');
 check(renderMarkdown(local).querySelector('.assistant-mention').title==='local · research','Historical inline reference retains its original full address after deletion');
 client.resources.invalidate([],{space:'one',reset:true});
 await wait(()=>!menu().hidden&&options().length===1);
 check(!menu().textContent.includes('代码助手'),'Reconnect refresh retains only current directory candidates');
 client.resources.invalidate(['usage'],{space:'one',boot:'next-boot'});
 check(menu().hidden,'A new Runtime boot retires candidates even on an unrelated topic');
 await wait(()=>!menu().hidden&&options().length===1);
 type(local+' 旧引用');await select('beta');await select('alpha');
 check(composerMentionValue(input())===local+' 旧引用','Existing references survive draft switches after target deletion');
 assistants.splice(0,assistants.length,...original);client.resources.invalidate(['assistants','agent-links'],scope);
}
async function run(){
 await wait(()=>input()&&!input().disabled&&!root.querySelector('.history:not([data-preparation-surface])').hidden);await pause(100);
 type('/');await wait(()=>options().length===3);check(!menu().querySelector('.avatar,svg'),'Skills have no icons');check(options().every(option=>option.getBoundingClientRect().height<=44),'Compact rows remain at most 44px');check(!menu().textContent.includes('forbidden'),'Slash excludes machines and labels');
 press('Escape');await pause(30);check(menu().hidden,'Escape stays dismissed after keyup');
 type('/nothing-matches');await wait(()=>!menu().hidden&&options().length===0);press('Enter');check(sends.length===0,'Empty results cannot accidentally send a slash query');
 type('person@example.com');check(menu().hidden,'Email addresses do not open mentions');type('/tmp/project');check(menu().hidden,'Paths do not open skill completion');
 type('/code');await wait(()=>options().length===1);const before=sends.length;await choose();check(chips().length===1&&composerMentionValue(input())===''&&sends.length===before,'Choosing a Skill inserts a removable chip without sending');
 type('@');await wait(()=>options().length===5);if(mobile){const enter=press('Enter');check(!enter.defaultPrevented&&sends.length===before,'Mobile Enter with an open picker remains native and cannot select or send');}check(options().every(option=>option.querySelector('.avatar img')),'Assistants use saved avatars with names');check(options()[2].getAttribute('aria-disabled')==='true','Offline assistant is visibly unavailable');
 options()[2].click();check(composerMentionValue(input())==='@'&&chips().length===1,'Offline selection cannot add a mention');
 type('前文 @代 后文',5);await wait(()=>options().length===1);await choose();
 check(composerMentionValue(input())==='前文 '+code+'  后文'&&chips().length===1,'Assistant replaces the current query in the middle with a compact inline label');
 check(input().value==='前文 @代码助手  后文'&&root.querySelector('.composer-mention-layer .assistant-mention')?.textContent==='@代码助手','Composer displays a tinted inline label without a raw address');
 check(document.activeElement===input()&&input().selectionStart==='前文 @代码助手 '.length,'Selection returns focus and places the caret after the inline reference');
 insert('继续');check(composerMentionValue(input())==='前文 '+code+' 继续 后文','Typing after selection preserves both sides');
 const body=composerMentionValue(input());await select('beta');check(chips().length===0&&composerMentionValue(input())==='','Switching owner does not leak Skills or mentions');await select('alpha');check(chips().length===1&&composerMentionValue(input())===body,'Returning restores Skills and inline references without duplication');
 type(body+' @');await wait(()=>options().length===5);press('ArrowDown');check(options()[1].getAttribute('aria-selected')==='true','ArrowDown selects the next available assistant');press('ArrowUp');check(options()[0].getAttribute('aria-selected')==='true','ArrowUp returns to the previous available assistant');await choose();insert('@');await wait(()=>options().length===5);await choose(3);
 check(composerMentionValue(input())===body+' '+local+' '+remote+' '&&chips().length===1,'Multiple same-name mentions preserve distinct full node identities in place');
 insert('@设计');await wait(()=>options().length===1);await choose();
 check(composerMentionValue(input()).endsWith('[@设计 师 \\[托尼\\] 🙂](pot-assistant:remote/unicode) '),'Space, Chinese, brackets and Unicode names retain the complete serialized identity');
 insert('@');await wait(()=>options().length===5);await choose();check(composerMentionValue(input()).split(local).length===3,'Selecting the same assistant again inserts another reference at the caret');
 const fullBody=composerMentionValue(input());
 await select('beta');await select('alpha');check(composerMentionValue(input())===fullBody&&chips().length===1,'Multimention draft restores without extracting links into chips');
 type('@');await wait(()=>options().length===5);const old=input();input().dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));check(menu().hidden,'IME preedit hides completion');const ime=press('Enter');check(!ime.defaultPrevented&&sends.length===before,'IME confirmation cannot select or send');input().dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));const confirmation=press('Enter');check(!confirmation.defaultPrevented&&sends.length===before,'Compositionend immediately followed by Enter cannot select or send');check(input()===old,'IME retains the native editor');await pause(100);
 type(fullBody);reject=true;root.querySelector('.composer .send-button').click();await wait(()=>sends.length===before+1);await pause(50);check(chips().length===1&&composerMentionValue(input())===fullBody,'Rejected send keeps Skills and inline mentions');reject=false;
 release=true;root.querySelector('.composer .send-button').click();await wait(()=>typeof release==='function');const sent=sends.at(-1);check(sent.scope.owner==='alpha'&&sent.request.conversation_id==='alpha','Mention keeps current assistant as the sole send target');check(sent.request.text==='/skill:code-review\n'+fullBody,'Send preserves inline positions and the complete assistant addresses');
 type('稍后继续');release();release=null;await pause(80);check(composerMentionValue(input())==='稍后继续'&&chips().length===1,'Late acceptance retains the edited draft and Skills');
 root.querySelector('.composer-selector-remove').click();check(chips().length===0,'Removing a Skill keeps the body');
 const rendered=renderMarkdown(sent.request.text),mentions=[...rendered.querySelectorAll('.assistant-mention')];check(mentions.length===5&&mentions[0].textContent==='@代码助手'&&mentions[1].title==='local · research'&&mentions[2].title==='remote · research'&&!rendered.querySelector('a'),'History renders readable assistant identities with full address titles');
 type('/slow');await pause(10);type('@');await wait(()=>options().length===5);release();release=null;await pause(30);check(menu().textContent.includes('代码助手')&&!menu().textContent.includes('code-review'),'Late slash result cannot overwrite mention results');
 const locale=parameters.get('locale')||'en',alternate=locale==='en'?'zh-CN':'en';window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:alternate}}));await pause(30);check(menu().textContent.includes(alternate==='en'?'Assistants':'助手'),'Open picker updates locale without losing its query');window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:locale}}));await pause(30);
 const box=menu().getBoundingClientRect(),composer=root.querySelector('.composer').getBoundingClientRect();check(box.left>=0&&box.right<=innerWidth&&box.top>=0&&box.bottom<=composer.top,'Picker stays above the composer within the viewport');
 await taskChecks();await directoryChecks();
 check(document.documentElement.scrollWidth<=innerWidth,'No horizontal overflow at the current width');check(errors.length===0,errors.join('\n'));
 type('/code');await wait(()=>options().length===1);await choose();type('请 @代 帮忙检查',4);await wait(()=>options().length===1);await choose();insert('一起');
 check(chips().length===1&&composerMentionValue(input())==='请 '+code+' 一起 帮忙检查','Final interactive composer retains the Skill above the inline mention');
 window.composerSelectorValue=()=>composerMentionValue(input());
 window.composerSelectorResults={passed:true,checks,errors};document.title='PASS: '+checks.length+' composer checks ('+innerWidth+'px, '+(mobile?'mobile':'desktop')+', '+locale+')';
 const result=document.createElement('output');result.textContent=document.title;result.style.cssText='position:fixed;top:0;right:0;background:#fff;color:#000;font-size:11px;z-index:999';document.body.append(result);
}
async function review(){
 await wait(()=>input()&&!input().disabled&&!root.querySelector('.history:not([data-preparation-surface])').hidden);await pause(100);
 type('请 @');await wait(()=>options().length===5);
 // Retire only local/research in the fake source after its search result is
 // visible. The exact same fixture runs against the before and after sources.
 assistants.splice(0,1);client.resources.invalidate(['assistants'],{space:'one'});
 await pause(200);
 window.mentionDirectoryReview={ready:true,candidates:options().map(option=>option.title),draft:input().value};
}
(parameters.has('review')?review():run()).catch(error=>{window.composerSelectorResults={passed:false,checks,errors:[...errors,String(error.stack||error)]};document.title='FAIL: '+error.message;const result=document.createElement('pre');result.textContent=JSON.stringify(window.composerSelectorResults,null,2);result.style.cssText='position:fixed;inset:0;background:white;color:black;z-index:999;overflow:auto';document.body.append(result);});
