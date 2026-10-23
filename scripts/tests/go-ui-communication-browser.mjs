import {createToolCall} from '../../web/src/components/tool-call.mjs';
import {messageIdentities} from '../../web/src/state/message-identities.mjs';
import {translate} from '../../web/src/locales/messages.mjs';
const params=new URLSearchParams(location.search),host=document.querySelector('#cases'),output=document.querySelector('#result');
let locale=params.get('locale')||'en';document.documentElement.lang=locale;document.documentElement.dataset.theme=params.get('theme')||'light';
if(params.has('phone'))document.body.classList.add('fixture-phone');
const t=key=>translate(locale,'tool.'+key),check=(ok,why)=>{if(!ok)throw Error(why);},frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
const address=(node,id='same',kind='assistant')=>({node_id:node,kind,agent_id:id}),actor=address('local'),remote=address('remote'),third=address('third');
const client={runtime:{runtime_id:'local',boot_id:'boot'},assistants:[{id:'same',control_id:'local',name:'Pot'},{id:'linked',control_id:'local',profile_name:'Code reviewer',origin:{kind:'codex',node_id:'remote',assistant_id:'same',name:'Codex'},offline:true},{id:'third',control_id:'local',profile_name:'设计师 · Design assistant',origin:{kind:'pot',node_id:'third',assistant_id:'same'}}]};
const identities=messageIdentities(client),memory=new Map(),cards=[];let openedTask;
function card(id,name,args,result,phase='completed'){
  const value={tool:{id,name,args,result,phase,turn_id:'fixture'}},view=createToolCall({key:id,memory,locale:()=>locale,messageActor:()=>actor,resolveMessageIdentity:address=>identities.resolve(address),onOpenTask:task=>openedTask=task});
  host.append(view.element);view.update(value);cards.push({view,value});return {view,value,root:view.element};
}
async function expand(entry){entry.root.querySelector('summary').click();await frame();}
try{
  const send=card('send','send_message',{to:remote,text:'**Review requested.**\n\n```js\nconst safe = true;\n```'},{accepted:true,message_id:'mail'});
  check(send.root.querySelector('.tool-status').textContent===t('messageAccepted'),'durable acceptance label');
  check(send.root.querySelector('.communication-name').textContent==='Pot','actual sender');
  check(send.root.querySelectorAll('.communication-mark .connection-brand').length===2,'known real Pot and Codex brands');
  check(send.root.querySelector('.communication-offline')?.textContent===t('endpointOffline'),'offline belongs to endpoint');
  await expand(send);
  check(send.root.querySelector('.communication-text strong')?.textContent==='Review requested.','safe production Markdown');
  check(send.root.querySelector('.tool-raw').open===false,'raw is second-level collapsed');
  check(send.root.querySelector('.tool-body>.tool-metadata')===null,'raw metadata stays secondary');
  check(send.root.querySelectorAll('.tool-section:not([hidden])').length===0,'no duplicate message section');
  const textNode=send.root.querySelector('.communication-text strong').firstChild;
  send.view.update(send.value);check(send.root.querySelector('.communication-text strong').firstChild===textNode,'refresh preserves body selection nodes');
  send.root.querySelector('.tool-raw>summary').click();await frame();
  check(send.root.querySelector('.tool-raw').textContent.includes('send_message')&&send.root.querySelector('.tool-raw').textContent.includes('node_id'),'raw full identity and actual tool');
  const raw=send.root.querySelector('.tool-raw');
  const unsafe='<img src=x onerror="window.executed=true"> [unsafe](javascript:alert(1))';
  const batch=card('batch','receive_message',{messages:[{from:remote,to:actor,text:unsafe},{from:third,to:actor,text:'Second source · 完整正文'},{from:remote,to:actor,text:'Third message'}]});
  check(batch.root.querySelector('.tool-title').textContent===t('receiveMessageSources').replace('{count}','2'),'multi-source title counts real full addresses');
  check(batch.root.querySelector('.tool-status').textContent===t('messageReceived'),'received is never processed');
  await expand(batch);
  check(batch.root.querySelectorAll('.communication-entry').length===3,'each receipt has own body');
  check(batch.root.querySelectorAll('.communication-body .communication-flow').length===3,'each receipt retains its own from/to');
  check(!batch.root.querySelector('img[src=x]')&&!batch.root.querySelector('a[href^="javascript:"]')&&!window.executed,'untrusted body safe');
  check(!batch.root.querySelector('time'),'no invented sender timestamps');
  const one=card('single-source','receive_message',{messages:[{from:third,to:actor,text:'First'},{from:third,to:actor,text:'Second'}]});
  check(one.root.querySelector('.tool-title').textContent.endsWith(t('messageCount').replace('{count}','2')),'single-source message count');
  const local=card('local','send_message',{to:actor,text:'Local note'},{completed:true});
  check(local.root.querySelector('.communication-channel').textContent===t('channelLocal'),'local mail is not Agent Link');
  check(local.root.querySelector('.tool-status').textContent!==t('messageAccepted'),'completion without acceptance stays unconfirmed');
  const partial=card('partial','send_message','{"to":',undefined,'preparing');await expand(partial);
  check(partial.root.querySelector('.communication-fallback').textContent===t('messagePreparing'),'partial args accurate preparation');
  partial.value.tool={...partial.value.tool,args:JSON.stringify({to:third,text:'Complete parsed text'}),phase:'running'};partial.view.update(partial.value);
  check(partial.root.querySelector('.communication-text').textContent==='Complete parsed text','only complete JSON yields body');
  const failed=card('failed','send_message',{to:remote},{error:'Synthetic transport failure'},'failed');await expand(failed);
  check(!failed.root.querySelector('.tool-failure').hidden,'failure distinct from message');
  check(failed.root.querySelector('.communication-fallback').textContent===t('messageUnavailable'),'missing message has raw fallback');
  for(const kind of ['task','user']){const item=card(kind,'send_message',{to:address('remote','same',kind),text:'Guidance says complete'},{accepted:true});check(!item.root.querySelector('.assistant-mention'),'guidance and user mail retain kind');check(!item.root.querySelector('.communication-task'),'ordinary mail never becomes task completion');}
  const task=card('delegate','create_task',{target:remote,text:'Review the change\nRead the patch and report findings.'},{id:'job',title:'Review the change',phase:'queued',agent_link:{kind:'codex',node_id:'remote',assistant_id:'same'}});await expand(task);
  check(task.root.querySelector('.tool-title').textContent===t('delegateTaskTo').replace('{name}','@Code reviewer'),'actual delegate name');
  task.root.querySelector('.communication-task').click();check(openedTask.id==='job','delegate links to existing Task detail');
  check(task.root.querySelector('.tool-status').textContent===t('messageTaskCreated'),'tool completion does not complete Task');
  const unknown=card('unknown','send_message',{to:address('unlisted'),text:'Unknown target'});
  check(unknown.root.querySelectorAll('.communication-mark .connection-brand').length===1,'unknown remote cannot borrow same-id brand');
  client.assistants=client.assistants.map(item=>item.origin?.node_id==='remote'?{...item,profile_name:'<b>Renamed</b> · 超长助手名称 '.repeat(4)}:item);
  for(const item of cards)item.view.update(item.value);
  check(send.root.open&&raw.open,'directory refresh retains both native disclosure levels');
  check(send.root.querySelector('.communication-text strong').firstChild===textNode,'rename preserves rendered body');
  const originalTheme=document.documentElement.dataset.theme;
  for(const theme of ['light','dark','system']){
    if(theme==='system')delete document.documentElement.dataset.theme;else document.documentElement.dataset.theme=theme;
    const dark=theme==='dark'||theme==='system'&&matchMedia('(prefers-color-scheme: dark)').matches;
    check(getComputedStyle(document.documentElement).getPropertyValue('--canvas').trim()===(dark?'#22231f':'#f8f6f1'),'shared '+theme+' canvas');
    check(send.root.open&&raw.open&&send.root.querySelector('.communication-text strong').firstChild===textNode,'theme preserves disclosure and selection nodes');
  }
  document.documentElement.dataset.theme=originalTheme;
  const phase=send.value.tool.phase;
  locale=locale==='en'?'zh-CN':'en';for(const item of cards)item.view.update(item.value);
  check(send.root.querySelector('.tool-status').textContent===t('messageAccepted')&&send.value.tool.phase===phase,'live locale never changes lifecycle');
  locale=params.get('locale')||'en';for(const item of cards)item.view.update(item.value);
  check(host.scrollWidth<=host.clientWidth+1,'names and bodies fit available width');
  send.root.querySelector('.tool-raw>summary').click();await frame();
  output.textContent='PASS: send, batches, brands, partial args, errors, safe Markdown, raw details, Task navigation, selection, rename, disclosure and locale';
  window.communicationResults={passed:true};
  if(!params.has('phone')){
    const phone=document.createElement('iframe');phone.title='390px communication';phone.src='/scripts/tests/fixtures/communication.html?phone=1&locale=zh-CN&theme=dark';document.body.prepend(phone);
    await new Promise(resolve=>phone.addEventListener('load',resolve,{once:true}));
    for(let i=0;i<120&&!phone.contentWindow.communicationResults;i++)await frame();
    check(phone.contentWindow.communicationResults?.passed,'390px iframe: '+phone.contentDocument.querySelector('#result')?.textContent);
    output.textContent+=' · 390px zh-CN/dark PASS';
  }
  if(params.has('results')){host.hidden=true;document.querySelector('iframe')?.setAttribute('hidden','');}
}catch(error){output.textContent='FAIL: '+error.message;window.communicationResults={passed:false,error:error.message};throw error;}
