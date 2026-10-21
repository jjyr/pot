// Production settings DOM with controlled failures; transport is covered by the
// authenticated browser-to-peer integration test in integrations/codex/agent.
import {createPages} from '../../web/src/components/pages.mjs';
import {translate} from '../../web/src/locales/messages.mjs';

const params=new URLSearchParams(location.search),host=document.querySelector('main'),checks=[],calls=[];
let locale=params.get('locale')||'en',pending=[],fail=true,opened=0;
document.documentElement.dataset.theme=params.get('theme')||'light';
const assistant={id:'agent-link:peer:codex',space_id:'local',name:'Remote fixture',capabilities:{version:1,operations:['assistant.profile']}};
let context={space:'local',owner:assistant.id};
const profile={id:'codex',name:assistant.name,avatar:'',revision:1};
const client={runtime:{boot_id:'fixture'},assistants:[assistant],scope:()=>({...context}),request(path,options){
  calls.push({path,...options});
  if(path==='/api/v1/assistants/profile')return new Promise((resolve,reject)=>pending.push(()=>fail?reject(Error('Fixture HTTP 404')):resolve({...profile})));
  if(path==='/api/v1/assistants/default-avatar')return Promise.resolve({avatar:''});
  if(options.method==='POST'){Object.assign(profile,options.body);return Promise.resolve({...profile,revision:++profile.revision});}
  throw Error('Unexpected settings request: '+path);
}};
const pages=createPages({client,host,approvalHost:document.createElement('div'),chatApprovalHost:document.createElement('div'),chatVisible:()=>false,locale:()=>locale,notify:()=>{},changed:()=>{},titleChanged:()=>{document.querySelector('h1').textContent=pages.title;},openChat:()=>{opened++;}});
const check=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
const wait=async read=>{for(let i=0;i<150;i++){if(read())return;await new Promise(resolve=>setTimeout(resolve,20));}throw Error('Timed out: '+read);};
const t=key=>translate(locale,key),button=key=>[...host.querySelectorAll('button')].find(node=>node.textContent===t(key));
const switchLocale=value=>{locale=value;document.documentElement.lang=value;window.dispatchEvent(new Event('pot:locale'));};
async function run(){
  const loading=pages.assistant(assistant);await wait(()=>pending.length);
  check(host.textContent.includes(t('loading.pending'))&&pages.hasParent,'Pending settings expose loading and a Back action');
  const readCount=calls.length;
  switchLocale(locale==='en'?'zh-CN':'en');
  check(host.textContent.includes(t('loading.pending'))&&calls.length===readCount,'Language switch updates pending text without restarting IO');
  pending.shift()();await loading;
  check(!host.textContent.includes(t('loading.pending'))&&host.querySelector('[role=alert]')&&button('retry'),'Failure replaces loading with an actionable error');
  check(host.textContent.includes(t('assistant.settingsLoadFailed')),'Load failure has a localized explanation');
  switchLocale(params.get('locale')||'en');
  check(button('retry')&&host.textContent.includes(t('assistant.settingsLoadFailed')),'Load error and Retry follow live language changes');
  context={space:'other-space',owner:'other-assistant'};fail=false;button('retry').click();await wait(()=>pending.length);pending.shift()();await wait(()=>pages.editing);
  check(calls.at(-1).owner===assistant.id&&calls.at(-1).space==='local','Retry retains the original full target after selection changes');
  check(host.querySelectorAll('.form-row').length===1,'Profile capability opens only the name and avatar editor');
  button('assistant.resetAvatar').click();await wait(()=>calls.at(-1).path.endsWith('default-avatar'));await Promise.resolve();
  check(calls.at(-1).owner===assistant.id,'Default avatar uses the captured remote target');
  const name=host.querySelector('input[aria-label="'+t('assistant.name')+'"]');name.value='Edited remote';name.dispatchEvent(new Event('input',{bubbles:true}));
  switchLocale(locale==='en'?'zh-CN':'en');
  check(name.value==='Edited remote'&&name.getAttribute('aria-label')===t('assistant.name'),'Language switch preserves profile draft and refreshes accessible labels');
  button('assistant.save').click();await wait(()=>opened===1);
  const save=calls.find(call=>call.method==='POST');
  check(save.path==='/api/v1/assistants/'+encodeURIComponent(assistant.id)&&save.owner===assistant.id&&save.space==='local'&&save.body.name==='Edited remote','Save keeps the original remote route and returns to chat');
  check(calls.every(call=>call.path.startsWith('/api/v1/assistants/')),'Profile settings never load model configuration');
  const late=pages.assistant(assistant);await wait(()=>pending.length);const abandoned=calls.at(-1);await pages.back();
  check(abandoned.signal.aborted,'Back aborts the retired profile request');pending.shift()();await late;
  check(!host.querySelector('form')&&opened===2,'Late settings response cannot reopen a departed editor');
  context={space:'local',owner:assistant.id};switchLocale(params.get('locale')||'en');
  const final=pages.assistant(assistant);await wait(()=>pending.length);pending.shift()();await final;
  window.assistantSettingsResults={passed:true,checks};document.querySelector('#result').textContent=checks.join('\n');
  if(params.has('capture'))document.querySelector('#result').hidden=true;
}
run().catch(error=>{window.assistantSettingsResults={passed:false,error:error.stack,checks};document.querySelector('#result').textContent=error.stack;});
