import {createAgentLinks} from './components/agent-links.mjs';
import {translate} from './locales/messages.mjs';

const token=location.hash.slice(1)||sessionStorage.getItem('pot-agent-page')||'';
if(location.hash){sessionStorage.setItem('pot-agent-page',token);history.replaceState(null,'',location.pathname+location.search);}
const parameters=new URLSearchParams(location.search);
const locale=()=>{const value=parameters.get('locale')||localStorage.getItem('pot-go-preview-locale')||(navigator.language.startsWith('zh')?'zh-CN':'en');return value==='zh-CN'?'zh-CN':'en';};
document.documentElement.lang=locale();document.documentElement.dataset.platform='browser';
document.documentElement.dataset.theme=parameters.get('theme')||localStorage.getItem('pot-go-preview-theme')||'system';
const root=document.getElementById('connections');
root.style.cssText='width:100%;max-width:820px;height:100%;overflow-y:auto;margin:0 auto;padding:32px 24px;box-sizing:border-box';
const heading=document.createElement('h1');heading.textContent=translate(locale(),'agentLinks.title');root.append(heading);
const notice=document.createElement('p');notice.className='hint';notice.setAttribute('role','status');notice.hidden=true;root.append(notice);
const content=document.createElement('div');root.append(content);
const lifecycle=new AbortController();
let noticeTimer,noticeKey='agentLinks.connected';
function notify(value){clearTimeout(noticeTimer);noticeKey='agentLinks.connected';notice.textContent=value;notice.hidden=false;noticeTimer=setTimeout(()=>{notice.hidden=true;},4000);}
async function request(path,options={}){
 const headers={...options.headers,Authorization:`Bearer ${token}`,'Content-Type':'application/json'};
 const timeout=options.method==='POST'?45000:15000;
 const response=await fetch(path,{...options,signal:AbortSignal.any([lifecycle.signal,options.signal,AbortSignal.timeout(timeout)].filter(Boolean)),headers,body:options.body&&typeof options.body!=='string'?JSON.stringify(options.body):options.body});
 const data=await response.json();if(!response.ok){if(response.status===401){clearTimeout(noticeTimer);noticeKey='agentLinks.pageExpired';notice.textContent=translate(locale(),noticeKey);notice.hidden=false;}throw Object.assign(new Error(data.error),{code:data.error});}return data;
}
const component=createAgentLinks({root:content,request,locale,notify,showConnections:true,discoverAddresses:true,signal:lifecycle.signal});
let stopped=false,pending=false,timer;
async function refresh(){if(stopped||pending||document.hidden)return;pending=true;try{await component.load();}finally{pending=false;if(!stopped)timer=setTimeout(refresh,3000);}}
await refresh();
function visibility(){clearTimeout(timer);if(!document.hidden)refresh();}
document.addEventListener('visibilitychange',visibility);
window.addEventListener('pagehide',()=>{stopped=true;clearTimeout(timer);clearTimeout(noticeTimer);lifecycle.abort();document.removeEventListener('visibilitychange',visibility);component.dispose();},{once:true});
window.addEventListener('pot:locale',()=>{document.documentElement.lang=locale();heading.textContent=translate(locale(),'agentLinks.title');if(!notice.hidden)notice.textContent=translate(locale(),noticeKey);component.load();});
