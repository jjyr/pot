'use strict';
(() => {
const $=(s,root=document)=>root.querySelector(s), $$=(s,root=document)=>[...root.querySelectorAll(s)];
let data=JSON.parse($('#pot-data').textContent), lang=data.locale;
let agent='codex',mode='direct',flow=0,report='site',installChannel='macos';
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
function animate(el){el.classList.remove('fade-in');void el.offsetWidth;el.classList.add('fade-in');}
function renderAgent(){
 const name=names[agent],a=d().agentTalk[keys.indexOf(agent)];
 $('#preview-name').textContent=name;$('#preview-icon').className='agent-icon '+agent;$('#preview-icon').innerHTML=`<img src="${image(agent)}" alt="">`;
 $('#preview-support').textContent=agent==='codex'?t('Agent Link · 已支持'):d().supportConcept;
 $('#network-note').textContent=agent==='codex'?t('Pot ↔ Codex：已核实支持连接、对话与任务委派。'):format(d().conceptNote,{agent:name});
 $('#agent-conversation').innerHTML=mode==='direct'?`<div class="conversation-user">${esc(a[0])}</div><div class="conversation-reply">${icon(agent)}<div><b>${name}</b><p>${esc(a[1])}</p></div></div><div class="conversation-result"><span>↳</span><div>${esc(a[2])}<small>${esc(d().directExample)}</small></div></div>`:`<div class="conversation-user">${esc(a[4])}</div><div class="conversation-reply">${pot()}<div><b>Pot</b><p>${esc(format(d().delegateReply,{agent:name}))}</p></div></div><div class="conversation-result">${icon(agent)}<div>${esc(a[3])}<small>${esc(format(d().returnExample,{agent:name}))}</small></div></div>`;
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
function renderFlow(){
 const a=d(),head=`<div class="flow-heading"><span>${esc(a.flowEyebrows[flow])}</span><span>${esc(format(a.flowNumber,{n:'0'+(flow+1)}))}</span></div>`;
 const reply=i=>`<div class="flow-message">${i===2?icon('codex'):pot()}<div><b>${i===2?'Codex → Pot':'Pot'}</b><p>${esc(a.flowReplies[i])}</p></div></div>`;
 const user=i=>`<div class="flow-user">${esc(a.flowUsers[i])}</div>`;
 const route=(x,y)=>`<div class="flow-route"><b>${esc(x)}</b><span>→</span><b>${esc(y)}</b></div>`;
 $('#flow-panel').innerHTML=head+(flow===0?user(0)+reply(0)+route(a.flowRoute[0],a.flowRoute[1]):flow===1?user(1)+reply(1)+'<div class="flow-route"><b>Pot</b><span>→ Agent Link →</span><b>Codex</b></div>'+`<div class="conversation-result">${icon('codex')}<div>${esc(t('产品官网 · 开发任务'))}<small>${esc(a.flowStatus)}</small></div></div>`:reply(2)+`<button class="flow-artifact" data-open-report="site"><span>↳</span><span>${esc(a.siteBundle)}<small>${esc(a.siteBundleSub)}</small></span><span>↗</span></button>`+user(2)+route(a.flowRoute[2],a.flowRoute[3]));
 pressed('[data-flow]','flow',flow);animate($('#flow-panel'));
}
function reportData(){return {...d().siteReport,title:d().siteReportTitle};}
function reportBody(r){return (r.table?'<table><thead><tr>'+r.table[0].map(v=>'<th scope="col">'+esc(v)+'</th>').join('')+'</tr></thead><tbody>'+r.table.slice(1).map(row=>'<tr>'+row.map(v=>'<td>'+esc(v)+'</td>').join('')+'</tr>').join('')+'</tbody></table>':'')+r.sections.map(s=>'<h3>'+esc(s.title)+'</h3>'+(s.paragraphs||[]).map(v=>'<p>'+esc(v)+'</p>').join('')+(s.items?'<ul>'+s.items.map(v=>'<li>'+esc(v)+'</li>').join('')+'</ul>':'')).join('');}
function openReport(){const r=reportData();$('#report-content').innerHTML='<h2 id="report-title">'+esc(r.title)+'</h2><p>'+esc(r.intro)+'</p>'+reportBody(r);if(!$('#report-dialog').open)$('#report-dialog').showModal();}
function downloadReport(){const r=reportData(),lines=[r.title,d().sample,'',r.intro,''];if(r.table)r.table.forEach(row=>lines.push(row.join(' | ')));r.sections.forEach(s=>lines.push('',s.title,...(s.paragraphs||[]),...(s.items||[]).map(v=>'• '+v)));const url=URL.createObjectURL(new Blob([lines.join('\n')],{type:'text/plain;charset=utf-8'})),a=document.createElement('a');a.href=url;a.download=`Pot-${r.title}-${d().sample}.txt`;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}
function restoreQuery(){const q=new URLSearchParams(location.search);if(keys.includes(q.get('agent')))agent=q.get('agent');if(['direct','delegate'].includes(q.get('mode')))mode=q.get('mode');if(['0','1','2'].includes(q.get('flow')))flow=Number(q.get('flow'));if(installChannels.includes(q.get('install')))installChannel=q.get('install');return q.get('report')==='site';}
function section(){const ids=['main','assistant','agent-link','workflow','start'];return '#'+(ids.filter(id=>$('#'+id).getBoundingClientRect().top<innerHeight/2).pop()||'main');}
// Native details and dialogs keep this story local; no agent or model requests.
function assistantDetails(){return $$('.a01-communication, .a01-call-details');}
function assistantPreview(){return $('#a01-originalDialog').open?'original':$('#a01-resultDialog').open?'result':'';}
function openAssistantPreview(kind){
 const dialog=$('#a01-'+(kind==='original'?'originalDialog':'resultDialog'));
 if(kind==='original'){
  const source=$(matchMedia('(max-width:720px)').matches?'.a01-mobile-shot':'.a01-desktop-shot');
  $('#a01-originalImage').src=source.src;$('#a01-originalImage').alt=source.alt;
 }
 if(!dialog.open)dialog.showModal();
}
function changeLanguage(next,persist=true){
 if(!PotLocale.codes.includes(next)||next===lang)return;
 if(persist)PotLocale.save(next);
 const hash=section(),wasOpen=$('#report-dialog').open,preview=assistantPreview();
 const communication=assistantDetails().map(el=>el.open?'1':'0').join('');
 if(window.POT_PREVIEW){
  const page=window.POT_PREVIEW[next];document.body.innerHTML=page.body;document.documentElement.lang=next;document.title=page.title;$('meta[name="description"]').content=page.description;data=page.data;lang=next;renderAll();
  assistantDetails().forEach((el,i)=>el.open=communication[i]==='1');
  const target=$(hash);if(hash==='#main')window.scrollTo({top:0,behavior:'instant'});else if(target)target.scrollIntoView({behavior:'instant'});
  $('#language-select').focus({preventScroll:true});
  if(wasOpen)openReport();
  if(preview){$('#a01-'+(preview==='original'?'openOriginal':'openResult')).focus({preventScroll:true});openAssistantPreview(preview);}
 }else{
  const url=new URL(data.assetBase+next+'/',location.href);
  url.search=new URLSearchParams({agent,mode,flow:String(flow),install:installChannel,communication,...(preview?{assistantPreview:preview}:{}),...(wasOpen?{report}:{})}).toString();url.hash=hash==='#main'?'':hash;location.assign(url.href);
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
function renderAll(){selectInstall(installChannel);renderAgent();renderFlow();$('#language-select').value=lang;}
document.addEventListener('change',e=>{if(e.target.id==='language-select')changeLanguage(e.target.value);});
document.addEventListener('click',e=>{
 const b=e.target.closest('button');if(!b||b.disabled)return;
 if(b.id==='a01-openOriginal')openAssistantPreview('original');
 else if(b.id==='a01-openResult')openAssistantPreview('result');
 else if(b.dataset.close)document.getElementById(b.dataset.close).close();
 else if(b.dataset.install){selectInstall(b.dataset.install);}
 else if(b.dataset.agent){agent=b.dataset.agent;renderAgent();}
 else if(b.dataset.mode){mode=b.dataset.mode;renderAgent();}
  else if(b.dataset.flow!==undefined){flow=Number(b.dataset.flow);renderFlow();}
 else if(b.dataset.openReport){report=b.dataset.openReport;openReport();}
 else if(b.dataset.copy)copyCode(b.dataset.copy);else if(b.id==='close-dialog')$('#report-dialog').close();else if(b.id==='download-report')downloadReport();
});
document.addEventListener('click',e=>{if(!e.target.matches('dialog'))return;const r=e.target.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)e.target.close();});
const showReport=restoreQuery();renderAll();showStars();if(showReport)openReport();
const query=new URLSearchParams(location.search),communication=query.get('communication');
if(/^[01]{4}$/.test(communication))assistantDetails().forEach((el,i)=>el.open=communication[i]==='1');
const preview=query.get('assistantPreview');
if(['original','result'].includes(preview)){$('#a01-'+(preview==='original'?'openOriginal':'openResult')).focus({preventScroll:true});openAssistantPreview(preview);}
if(window.POT_PREVIEW)changeLanguage(PotLocale.preferred(),false);
})();
