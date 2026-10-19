import {createAgentLinks} from './components/agent-links.mjs';
import {translate} from './locales/messages.mjs';

const token=location.hash.slice(1)||sessionStorage.getItem('pot-agent-owner')||'';
if(location.hash){sessionStorage.setItem('pot-agent-owner',token);history.replaceState(null,'',location.pathname);}
const parameters=new URLSearchParams(location.search);
const locale=()=>{const value=parameters.get('locale')||localStorage.getItem('pot-go-preview-locale')||(navigator.language.startsWith('zh')?'zh-CN':'en');return value==='zh-CN'?'zh-CN':'en';};
document.documentElement.lang=locale();document.documentElement.dataset.platform='browser';
document.documentElement.dataset.theme=parameters.get('theme')||localStorage.getItem('pot-go-preview-theme')||'system';
const root=document.getElementById('connections');
root.style.cssText='max-width:820px;margin:32px auto;padding:0 24px';
const heading=document.createElement('h1');heading.textContent=translate(locale(),'agentLinks.title');root.append(heading);
const content=document.createElement('div');root.append(content);
const lifecycle=new AbortController();
async function request(path,options={}){
 const headers={...options.headers,Authorization:`Bearer ${token}`,'Content-Type':'application/json'};
 const response=await fetch(path,{...options,signal:AbortSignal.any([lifecycle.signal,AbortSignal.timeout(15000)]),headers,body:options.body&&typeof options.body!=='string'?JSON.stringify(options.body):options.body});
 const data=await response.json();if(!response.ok)throw Object.assign(new Error(data.error),{code:data.error});return data;
}
const component=createAgentLinks({root:content,request,locale,currentAssistant:()=> 'codex',assistants:()=>[{id:'codex',name:'Codex'}],fixedAssistant:true,showConnections:true,signal:lifecycle.signal});
let stopped=false,pending=false,timer;
async function refresh(){if(stopped||pending||document.hidden)return;pending=true;try{await component.load();}finally{pending=false;if(!stopped)timer=setTimeout(refresh,3000);}}
await refresh();
function visibility(){clearTimeout(timer);if(!document.hidden)refresh();}
document.addEventListener('visibilitychange',visibility);
window.addEventListener('pagehide',()=>{stopped=true;clearTimeout(timer);lifecycle.abort();document.removeEventListener('visibilitychange',visibility);component.dispose();},{once:true});
window.addEventListener('pot:locale',()=>{heading.textContent=translate(locale(),'agentLinks.title');component.load();});
