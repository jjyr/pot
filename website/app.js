'use strict';
(() => {
const $=(s,root=document)=>root.querySelector(s), $$=(s,root=document)=>[...root.querySelectorAll(s)];
let data=JSON.parse($('#pot-data').textContent), lang=data.locale;
let installChannel='macos',currentView='overview';
const views=['overview','security','install','changelog'];
const t=key=>{if(!(key in data.page))throw new Error('Missing translation: '+key);return data.page[key];};
const format=(s,values)=>s.replace(/\{(\w+)\}/g,(_,key)=>values[key]??'');
function selectView(next){
 if(!views.includes(next))return;
 currentView=next;
 document.body.classList.add('js-views-ready');
 $$('.site-view').forEach(el=>el.classList.toggle('active',el.dataset.viewPanel===next));
 $$('[data-view-tab]').forEach(tab=>{
  const active=tab.dataset.viewTab===next;
  tab.classList.toggle('active',active);
  if(active)tab.setAttribute('aria-current','page');else tab.removeAttribute('aria-current');
 });
}
function viewForHash(){
 const target=document.getElementById(location.hash.slice(1));
 return target?.closest('[data-view-panel]')?.dataset.viewPanel||'overview';
}
function navigateView(next,hash){
 const url=new URL(location.href);url.searchParams.delete('view');url.hash=hash;
 history.pushState(null,'',url);
 selectView(next);scrollToHash();
}
function scrollToHash(){
 const target=document.getElementById(location.hash.slice(1));
 if(location.hash.startsWith('#view-')||location.hash==='#start'||location.hash==='#main'){window.scrollTo({top:0,behavior:'instant'});return;}
 if(target)target.scrollIntoView({behavior:'instant'});else window.scrollTo({top:0,behavior:'instant'});
}
function restoreLocation(){
 const query=new URLSearchParams(location.search);
 selectView(location.hash?viewForHash():(views.includes(query.get('view'))?query.get('view'):'overview'));
 if(installChannels.includes(query.get('install')))installChannel=query.get('install');
 selectInstall(installChannel);
}
window.addEventListener('popstate',()=>{restoreLocation();scrollToHash();});
window.addEventListener('hashchange',()=>{restoreLocation();scrollToHash();});
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
const choices=['codex','claude','gemini','antigravity','deepseek','qwen','grok','glm'];
let selectedChoice=new URL(location.href).searchParams.get('character');
if(!choices.includes(selectedChoice))selectedChoice='codex';
function selectCharacter(id){
 if(!choices.includes(id))return;
 selectedChoice=id;
 const board=$('.agent-select'),tile=$(`[data-choice="${id}"]`);
 if(!board||!tile)return;
 board.classList.add('selection-ready');
 $$('.agent-tile').forEach(el=>{
  const selected=el.dataset.choice===id;
  el.setAttribute('role','button');el.setAttribute('aria-pressed',String(selected));
  el.setAttribute('aria-controls','selection-info');
 });
 $$('.choice-panel').forEach(el=>{
  const active=el.dataset.choicePanel===id;
  el.classList.toggle('is-selected',active);el.inert=!active;
  el.setAttribute('aria-hidden',String(!active));
 });
 $('.pot-portrait').dataset.facing=tile.dataset.direction;
}
// Delegate events so offline locale changes use the new localized DOM.
document.addEventListener('pointerover',e=>{
 if(e.pointerType==='touch')return;
 const tile=e.target.closest('[data-choice]');if(tile)selectCharacter(tile.dataset.choice);
});
document.addEventListener('focusin',e=>{
 const tile=e.target.closest('[data-choice]');if(tile)selectCharacter(tile.dataset.choice);
});
document.addEventListener('click',e=>{
 const tile=e.target.closest('[data-choice]');if(!tile)return;
 e.preventDefault();selectCharacter(tile.dataset.choice);
});
document.addEventListener('keydown',e=>{
 const tile=e.target.closest('[data-choice]');if(!tile)return;
 if(e.key===' '||e.key==='Enter'){e.preventDefault();selectCharacter(tile.dataset.choice);return;}
 const grid=['codex','claude','gemini','antigravity',null,'deepseek','qwen','grok','glm'];
 const steps={ArrowLeft:-1,ArrowRight:1,ArrowUp:-3,ArrowDown:3};
 let index=grid.indexOf(tile.dataset.choice);
 if(e.key==='Home')index=0;
 else if(e.key==='End')index=8;
 else if(e.key in steps){
  const step=steps[e.key];let next=index+step;
  if(grid[next]===null)next+=step;
  if(next<0||next>8||(Math.abs(step)===1&&Math.floor(next/3)!==Math.floor(index/3)))return;
  index=next;
 }else return;
 e.preventDefault();$(`[data-choice="${grid[index]}"]`).focus();
});

function section(){
 if(currentView!=='overview')return '#view-'+currentView;
 const ids=['main','agent-link','philosophy','workflow','mcp','models','assistant','project'];
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
  url.search=new URLSearchParams({install:installChannel,character:selectedChoice}).toString();url.hash=hash;location.assign(url.href);
 }
}
const installChannels=['agent','macos','linux','web'];
function selectInstall(next,focus=false,persist=false){
 if(!installChannels.includes(next))return;
 installChannel=next;
 if(persist){const url=new URL(location.href);url.searchParams.set('install',next);history.replaceState(null,'',url);}
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
 e.preventDefault();selectInstall(installChannels[index],true,true);
});
function renderAll(){selectCharacter(selectedChoice);selectView(currentView);selectInstall(installChannel);$('#language-select').value=lang;}
document.addEventListener('change',e=>{if(e.target.id==='language-select')changeLanguage(e.target.value);});
document.addEventListener('click',e=>{
 const switchLink=e.target.closest('[data-switch-view],[data-view-tab]');
 if(switchLink){
  if(e.metaKey||e.ctrlKey||e.shiftKey||e.altKey||e.button!==0)return;
  e.preventDefault();
  const next=switchLink.dataset.switchView||switchLink.dataset.viewTab;
  const hash=switchLink.getAttribute('href')||'#view-'+next;
  navigateView(next,hash==='#'?'#view-overview':hash);
  return;
 }
 const b=e.target.closest('button');if(!b||b.disabled)return;
 if(b.dataset.install){if(currentView!=='install')selectView('install');selectInstall(b.dataset.install,false,true);}
 else if(b.dataset.copy)copyCode(b.dataset.copy);
});
restoreLocation();renderAll();scrollToHash();showStars();
if(window.POT_PREVIEW)changeLanguage(PotLocale.preferred(),false);
})();
