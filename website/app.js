'use strict';
(() => {
const $=(s,root=document)=>root.querySelector(s), $$=(s,root=document)=>[...root.querySelectorAll(s)];
let data=JSON.parse($('#pot-data').textContent), lang=data.locale;
let agent='codex',mode='direct',installChannel='macos',currentView='overview';
const views=['overview','security','install','changelog'];
const names={codex:'Codex',claude:'Claude',grok:'Grok bot',devin:'Devin'};
const files={pot:'pot.png',codex:'codex.png',claude:'claude.svg',grok:'grok.svg',devin:'devin.png'};
const keys=Object.keys(names);
const t=key=>{if(!(key in data.page))throw new Error('Missing translation: '+key);return data.page[key];};
const d=()=>data.demo;
const format=(s,values)=>s.replace(/\{(\w+)\}/g,(_,key)=>values[key]??'');
const esc=s=>String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const image=k=>(data.assets&&data.assets[k])||data.assetBase+'assets/'+files[k];
const icon=k=>`<span class="agent-icon ${k}" aria-hidden="true"><img src="${image(k)}" alt=""></span>`;
const pot=()=>`<img src="${image('pot')}" alt="" aria-hidden="true">`;
function pressed(selector,attribute,value){$$(selector).forEach(b=>{const active=b.dataset[attribute]===String(value);b.classList.toggle('active',active);b.setAttribute('aria-pressed',String(active));});}
function animate(el){if(!el)return;el.classList.remove('fade-in');void el.offsetWidth;el.classList.add('fade-in');}
function selectView(next,scrollTarget=null){
 if(!views.includes(next))return;
 currentView=next;
 document.body.classList.add('js-views-ready');
 $$('.site-view').forEach(el=>{
  const active=el.dataset.viewPanel===next;
  el.classList.toggle('active',active);
 });
 $$('[data-view-tab]').forEach(tab=>{
  const active=tab.dataset.viewTab===next;
  tab.classList.toggle('active',active);
  if(active)tab.setAttribute('aria-current','page');
  else tab.removeAttribute('aria-current');
 });
 if(scrollTarget==='top')window.scrollTo({top:0,behavior:'instant'});
 else if(scrollTarget&&$(scrollTarget))$(scrollTarget).scrollIntoView({behavior:'smooth'});
}
function renderAgent(){
 if(!$('#agent-conversation'))return;
 const name=names[agent],a=d().agentTalk[keys.indexOf(agent)];
 $('#preview-name').textContent=name;$('#preview-icon').className='agent-icon '+agent;$('#preview-icon').innerHTML=`<img src="${image(agent)}" alt="">`;
 $('#preview-support').textContent=agent==='codex'?t('Agent Link · 已支持'):d().supportConcept;
 $('#network-note').textContent=agent==='codex'?t('Pot ↔ Codex：已核实支持连接、对话与任务委派。'):format(d().conceptNote,{agent:name});
 $('#agent-conversation').innerHTML=mode==='direct'?`<div class="conversation-user">${esc(a[0])}</div><div class="conversation-reply">${icon(agent)}<div><b>${name}</b><p>{${esc(a[1])}}</p></div></div><div class="conversation-result"><span>↳</span><div>${esc(a[2])}<small>${esc(d().directExample)}</small></div></div>`.replace(`{${esc(a[1])}}`,esc(a[1])):`<div class="conversation-user">${esc(a[4])}</div><div class="conversation-reply">${pot()}<div><b>Pot</b><p>${esc(format(d().delegateReply,{agent:name}))}</p></div></div><div class="conversation-result">${icon(agent)}<div>${esc(a[3])}<small>${esc(format(d().returnExample,{agent:name}))}</small></div></div>`;
 pressed('[data-agent]','agent',agent);pressed('[data-mode]','mode',mode);animate($('#agent-conversation'));
}
async function copyCode(id){
 const code=document.getElementById(id),status=$('#copy-status');
 try{await navigator.clipboard.writeText(code.textContent);status.textContent=t('已复制。');}
 catch{status.textContent=t('无法自动复制，请选择代码后复制。');const range=document.createRange();range.selectNodeContents(code);const selection=window.getSelection();selection.removeAllRanges();selection.addRange(range);}
}
// Truncate compact values so the visible count never overstates the API value.
function compactStars(count){
 const units=[[1e15,'{count}q'],[1e12,'{count}t'],[1e9,'{count}b'],[1e6,'{count}m'],[1e3,'{count}k']];
 const unit=units.find(([size])=>count>=size);
 if(!unit)return new Intl.NumberFormat(lang).format(count);
 const value=count/unit[0],short=value>=100?Math.floor(value):Math.floor(value*10)/10;
 return format(t(unit[1]),{count:new Intl.NumberFormat(lang,{maximumFractionDigits:1}).format(short)});
}
async function showStars(){
 // Offline previews stay offline. The repository link works without this enhancement.
 if(window.POT_PREVIEW)return;
 const link=$('.github-star'),bubble=$('.github-star-count');
 const valid=count=>Number.isSafeInteger(count)&&count>=0;
 const display=count=>{
  if(!valid(count))return;
  const full=new Intl.NumberFormat(lang).format(count);
  $('.github-star-count-full').textContent=full;
  $('.github-star-count-compact').textContent=compactStars(count);
  const label=format(t('在 GitHub 上查看 Pot 仓库，Star 数 {count}'),{count:full});
  link.setAttribute('aria-label',label);link.title=label;bubble.hidden=false;
 };
 const key='pot-github-stars-v1',ttl=6*60*60*1000;
 try{const cached=JSON.parse(localStorage.getItem(key));if(cached&&Number.isFinite(cached.time)&&cached.time<=Date.now()&&Date.now()-cached.time<ttl&&valid(cached.count)){display(cached.count);return;}}catch{}
 const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),3000);
 try{
  const response=await fetch('https://api.github.com/repos/jjyr/pot',{credentials:'omit',referrerPolicy:'no-referrer',signal:controller.signal});
  if(!response.ok)return;
  const {stargazers_count:count}=await response.json();
  display(count);
  if(valid(count))try{localStorage.setItem(key,JSON.stringify({count,time:Date.now()}));}catch{}
 }catch{}finally{clearTimeout(timeout);}
}
function restoreQuery(){
 const q=new URLSearchParams(location.search);
 if(views.includes(q.get('view')))currentView=q.get('view');
 else if(location.hash==='#view-security')currentView='security';
 else if(location.hash==='#view-install'||location.hash==='#start')currentView='install';
 else if(location.hash==='#view-changelog')currentView='changelog';
 if(keys.includes(q.get('agent')))agent=q.get('agent');
 if(['direct','delegate'].includes(q.get('mode')))mode=q.get('mode');
 if(installChannels.includes(q.get('install')))installChannel=q.get('install');
}
function section(){
 if(currentView!=='overview')return '#view-'+currentView;
 const ids=['main','assistant','agent-link','models'];
 return '#'+(ids.filter(id=>{const el=$('#'+id);return el&&el.getBoundingClientRect().top<innerHeight/2;}).pop()||'main');
}
function changeLanguage(next,persist=true){
 if(!PotLocale.codes.includes(next)||next===lang)return;
 if(persist)PotLocale.save(next);
 const hash=section();
 if(window.POT_PREVIEW){
  const page=window.POT_PREVIEW[next];document.body.innerHTML=page.body;document.documentElement.lang=next;document.title=page.title;$('meta[name="description"]').content=page.description;data=page.data;lang=next;renderAll();
  const target=$(hash);if(hash==='#main'||hash.startsWith('#view-'))window.scrollTo({top:0,behavior:'instant'});else if(target)target.scrollIntoView({behavior:'instant'});
  $('#language-select').focus({preventScroll:true});
 }else{
  const url=new URL(data.assetBase+next+'/',location.href);
  url.search=new URLSearchParams({view:currentView,agent,mode,install:installChannel}).toString();url.hash=hash==='#main'||hash.startsWith('#view-')?'':hash;location.assign(url.href);
 }
}
const installChannels=['agent','macos','linux','web'];
function selectInstall(next,focus=false){
 if(!installChannels.includes(next))return;
 installChannel=next;
 $('.install-tabs').hidden=false;
 $('.install-section').classList.add('install-ready');
 document.querySelectorAll('[data-install]').forEach(button=>{
  const selected=button.dataset.install===next;
  button.setAttribute('aria-selected',String(selected));button.tabIndex=selected?0:-1;
  const panel=$('#'+button.getAttribute('aria-controls'));panel.hidden=!selected;
  panel.setAttribute('role','tabpanel');panel.tabIndex=0;
  if(selected&&focus)button.focus();
 });
}
document.addEventListener('keydown',e=>{
 const button=e.target.closest('[data-install]');if(!button)return;
 let index=installChannels.indexOf(button.dataset.install);
 if(e.key==='ArrowRight')index=(index+1)%4;
 else if(e.key==='ArrowLeft')index=(index+3)%4;
 else if(e.key==='Home')index=0;
 else if(e.key==='End')index=3;
 else return;
 e.preventDefault();selectInstall(installChannels[index],true);
});
function renderAll(){selectView(currentView);selectInstall(installChannel);renderAgent();$('#language-select').value=lang;}
document.addEventListener('change',e=>{if(e.target.id==='language-select')changeLanguage(e.target.value);});
document.addEventListener('click',e=>{
 const switchLink=e.target.closest('[data-switch-view],[data-view-tab]');
 if(switchLink){
  e.preventDefault();
  const targetView=switchLink.dataset.switchView||switchLink.dataset.viewTab;
  const href=switchLink.getAttribute('href')||'';
  const scrollTarget=(href.startsWith('#')&&href!=='#view-'+targetView&&href!=='#start')?href:'top';
  selectView(targetView,scrollTarget);
  return;
 }
 const b=e.target.closest('button');if(!b||b.disabled)return;
 if(b.dataset.install){if(currentView!=='install')selectView('install');selectInstall(b.dataset.install);}
 else if(b.dataset.agent){agent=b.dataset.agent;renderAgent();}
 else if(b.dataset.mode){mode=b.dataset.mode;renderAgent();}
 else if(b.dataset.copy)copyCode(b.dataset.copy);
});
restoreQuery();renderAll();showStars();
if(window.POT_PREVIEW)changeLanguage(PotLocale.preferred(),false);
})();
