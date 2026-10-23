// Real shared DOM, synthetic identities only; runs at desktop and iframe phone widths.
import {createToolCall} from '../../web/src/components/tool-call.mjs';
import {createTaskConversation} from '../../web/src/components/task-conversation.mjs';
import {createTaskTranscript} from '../../web/src/state/task-transcript.mjs';
import {messageIdentities} from '../../web/src/state/message-identities.mjs';
import {translate} from '../../web/src/locales/messages.mjs';
const check=(value,message)=>{if(!value)throw Error(message);};
const frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
const host=document.querySelector('#cases'),result=document.querySelector('#result'),params=new URLSearchParams(location.search);
let locale=params.get('locale')||'en';document.documentElement.lang=locale;
const address=(node,kind='assistant')=>({node_id:node,kind,agent_id:'same'});
const local=address('local'),remote=address('remote');
const client={runtime:{runtime_id:'local',boot_id:'boot'},scope:()=>({space:'local',owner:'same'}),request:async()=>({items:[]}),assistants:[{id:'same',control_id:'local',name:'Chef'},{id:'agent-link:peer:same',name:'Chef',profile_name:'Chef',origin:{node_id:'remote',assistant_id:'same',kind:'pot',name:'Other kitchen'}}]};
const profile=messageIdentities(client),cards=[];
const message=(id,name,args)=>({id,sequence:cards.length+1,role:'tool',text:'',tool:{id,turn_id:'fixture',name,args,phase:'completed'}});
function card(value){const view=createToolCall({locale:()=>locale,resolveMessageIdentity:address=>profile.resolve(address)});host.append(view.element);view.update(value);cards.push({view,value});return view;}
const title=view=>view.element.querySelector('.tool-title').textContent;
const expected=(key,name)=>translate(locale,'tool.'+key).replace('{name}',name);
try{
  const incoming=card(message('incoming','receive_message',{messages:[{from:remote,text:'<script>window.injected=true</script>'},{from:remote,text:'Second receipt'}]}));
  const outbound=card(message('outbound','send_message',{to:local,text:'Synthetic message'}));
  check(title(incoming)===expected('receiveMessageFrom','@Chef')+translate(locale,'tool.messageCount').replace('{count}','2'),'incoming real sender name, deduplicated by address');
  check(title(outbound)===expected('sendMessageTo','@Chef'),'outgoing actual recipient (including self)');
  check(incoming.element.querySelector('.agent-source')?.getAttribute('aria-label').includes('Other kitchen'),'duplicate names retain existing linked source identity');
  const composed=card({...message('composed','exec',{}),tool:{id:'composed',name:'exec',phase:'completed',script:{focus:{name:'send_message',args:{to:remote}},calls:[]}}});
  check(title(composed)===expected('sendMessageTo','@Chef'),'composed send tool focus uses the same identity title');
  const unknown=card(message('unknown','send_message',{to:address('third')}));
  check(title(unknown)===expected('sendMessageTo',translate(locale,'tool.messageAssistantUnavailable')),'same ID on unknown node does not borrow a name');
  for(const kind of ['task','user']){
    const view=card(message(kind,'send_message',{to:address('local',kind)}));
    check(!view.element.querySelector('.assistant-mention'),'non-assistant never becomes an assistant mention');
    check(title(view)===expected('sendMessageTo',translate(locale,kind==='task'?'tool.messageTask':'tool.messageUser')),'kind-specific fallback');
  }
  const streaming=card(message('streaming','send_message',{}));
  for(const args of [null,'{"to":',{}, {to:{kind:'assistant'}}, {to:remote}])streaming.update(message('streaming','send_message',args));
  check(title(streaming)===expected('sendMessageTo','@Chef'),'streaming partial arguments recover');
  incoming.element.querySelector('summary').click();await frame();
  check(incoming.element.open&&incoming.element.querySelector('.communication-text').textContent.includes('<script>'),'expanded receive shows the message directly');
  check(incoming.element.textContent.includes('<script>')&&!window.injected,'message HTML stays literal');
  outbound.element.querySelector('summary').click();await frame();
  check(!outbound.element.querySelector('.tool-raw').open,'call details start collapsed');
  outbound.element.querySelector('.tool-raw>summary').click();await frame();
  check(outbound.element.textContent.includes('send_message')&&outbound.element.textContent.includes('node_id'),'raw tool name and arguments remain in expanded details');
  const literal='<img src=x onerror="window.injected=true"> [名字] 🙂';
  client.assistants=client.assistants.map(item=>item.origin?{...item,name:literal,profile_name:literal}:item);
  incoming.update(cards[0].value);
  check(title(incoming)===expected('receiveMessageFrom','@'+literal)+translate(locale,'tool.messageCount').replace('{count}','2')&&!incoming.element.querySelector('img[src=x]'),'renamed display name is literal DOM text');
  check(incoming.element.open,'identity refresh preserves native disclosure');
  const wrapped=incoming.element.querySelector('.tool-title');
  check(wrapped.scrollWidth<=wrapped.clientWidth+1,'literal long display name wraps inside the summary');
  const taskRoot=document.querySelector('#task'),state=createTaskTranscript();
  state.messages=[message('task-send','send_message',{to:remote,text:'Task fixture'}),message('task-receive','receive_message',{messages:[{from:local,text:'Synthetic incoming tool record'}]})];
  const task=createTaskConversation({root:taskRoot,task:{id:'fixture-task',assistant_id:'same'},record:{info:{phase:'complete'},request:{text:'Synthetic task'},capabilities:{version:1,operations:['task.input']}},locale:()=>locale,client,scope:client.scope(),draft:'Unsent task draft',onDraft:()=>{},toolMemory:new Map(),approvalFor:()=>null,assistantName:'Chef'});
  task.update({state});task.start();await frame();
  check(taskRoot.querySelector('[data-call-id=task-send] .tool-title')?.textContent===expected('sendMessageTo','@'+literal),'Task conversation shares complete target resolution');
  check(taskRoot.querySelector('[data-call-id=task-receive] .tool-title')?.textContent===expected('receiveMessageFrom','@Chef'),'Task receive tool record shares sender identity');
  const taskSend=taskRoot.querySelector('[data-call-id=task-send]'),taskReceive=taskRoot.querySelector('[data-call-id=task-receive]');
  check(!taskSend.open&&!taskReceive.open,'Task send and receive start collapsed');
  taskSend.querySelector('summary').click();await frame();
  check(taskSend.querySelector('.communication-summary').hidden&&taskSend.querySelector('.communication-identity'),'Task expanded identity replaces compact graph');
  const editor=task.input;editor.dispatchEvent(new Event('input',{bubbles:true}));await frame();
  check(taskSend.open&&!taskReceive.open,'Task typing retains disclosure choices');client.assistants=client.assistants.map(item=>item.origin?{...item,name:'Remote chef updated',profile_name:'Remote chef updated'}:item);
  task.update({});await frame();
  check(taskRoot.querySelector('.tool-title')?.textContent===expected('sendMessageTo','@Remote chef updated'),'Task directory refresh updates existing title');
  check(taskSend.open&&!taskReceive.open,'Task directory refresh retains disclosure choices');
  check(task.input===editor&&editor.value==='Unsent task draft','Task draft/editor survive identity update');
  locale=locale==='en'?'zh-CN':'en';document.documentElement.lang=locale;
  for(const entry of cards)entry.view.update(entry.value);task.update({});await frame();
  check(title(outbound)===expected('sendMessageTo','@Chef'),'mounted title follows locale accessor');
  check(taskRoot.querySelector('.tool-title')?.textContent===expected('sendMessageTo','@Remote chef updated'),'Task title follows locale accessor');
  check(taskSend.open&&!taskReceive.open,'Task locale refresh retains disclosure choices');
  check(host.scrollWidth<=host.clientWidth+1,'long names fit the available width');
  result.textContent=`PASS (${innerWidth}px): incoming/outgoing, full address, self, duplicate source, fallback, streaming, escaping, raw details, disclosure, Task, drafts, rename and live i18n`;
  if(!params.has('embedded')){const phone=document.createElement('iframe');phone.title='Phone message title checks';phone.src='/scripts/tests/fixtures/message-titles.html?embedded=1&locale=zh-CN';document.body.append(phone);}
}catch(error){result.textContent='FAIL: '+error.message;throw error;}
