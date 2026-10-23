'use strict';
(() => {
const $=(s,root=document)=>root.querySelector(s), $$=(s,root=document)=>[...root.querySelectorAll(s)];
let data=JSON.parse($('#pot-data').textContent), lang=data.locale;
let agent='codex',mode='direct',task='research',flow=0,report='research',taskPhase=3,timers=[];
const names={codex:'Codex',claude:'Claude',grok:'Grok bot',devin:'Devin'};
const files={pot:'pot.png',codex:'codex.png',claude:'claude.svg',grok:'grok.svg',devin:'devin.png'};
const keys=Object.keys(names),taskKeys=['research','organize','watch'];
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
function taskData(){return task==='research'?{
 prompt:t('帮我调研建站方案，')+'\n'+t('整理成一份对比报告。'),title:t('建站方案对比报告'),
 steps:[[t('理解目标，建立任务'),t('明确使用场景与比较维度')],[t('收集资料，推进调研'),t('整理方案、成本与维护方式')],[t('整理结论，带回成果'),t('在对话中交付可继续使用的文件')]]
}:d().extraTasks[task];}
function clearTimers(){timers.forEach(clearTimeout);timers=[];}
function renderTask(){
 clearTimers();const a=taskData();$('#task-prompt').innerHTML=esc(a.prompt).replaceAll('\n','<br>');$('#artifact-name').textContent=a.title;$('#task-artifact').dataset.openReport=task;$('#task-artifact').disabled=false;
 $('#task-steps').innerHTML=a.steps.map((s,i)=>`<li class="done"><span>0${i+1}</span><div><b>${esc(s[0])}</b><small>${esc(s[1])}</small></div><i>✓</i></li>`).join('');
 $('#task-status').textContent=t('已完成');$('#run-task').disabled=false;$('#run-task').innerHTML=esc(t('播放任务过程'))+' <span>↗</span>';pressed('[data-task]','task',task);
}
function runTask(start=0){
 clearTimers();$('#run-task').disabled=true;$('#run-task').innerHTML=esc(d().playing)+' <span>↗</span>';$('#task-artifact').disabled=true;
 const steps=$$('#task-steps li'),advance=n=>{taskPhase=n;
 steps.forEach((li,i)=>{li.className=i<n?'done':i===n?'running':'pending';$('i',li).textContent=i<n?'✓':i===n?'↗':'·';});
 $('#task-status').textContent=n<3?[d().statusCreated,d().statusRunning,d().statusFinishing][n]:t('已完成');
 if(n===3){$('#run-task').disabled=false;$('#run-task').innerHTML=esc(d().replay)+' <span>↻</span>';$('#task-artifact').disabled=false;animate($('#task-artifact'));}};
 advance(start);[1,2,3].filter(n=>n>start).forEach(n=>timers.push(setTimeout(()=>advance(n),(n-start)*1050)));
}
function renderFlow(){
 const a=d(),head=`<div class="flow-heading"><span>${esc(a.flowEyebrows[flow])}</span><span>${esc(format(a.flowNumber,{n:'0'+(flow+1)}))}</span></div>`;
 const reply=i=>`<div class="flow-message">${i===2?icon('codex'):pot()}<div><b>${i===2?'Codex → Pot':'Pot'}</b><p>${esc(a.flowReplies[i])}</p></div></div>`;
 const user=i=>`<div class="flow-user">${esc(a.flowUsers[i])}</div>`;
 const route=(x,y)=>`<div class="flow-route"><b>${esc(x)}</b><span>→</span><b>${esc(y)}</b></div>`;
 $('#flow-panel').innerHTML=head+(flow===0?user(0)+reply(0)+route(a.flowRoute[0],a.flowRoute[1]):flow===1?user(1)+reply(1)+'<div class="flow-route"><b>Pot</b><span>→ Agent Link →</span><b>Codex</b></div>'+`<div class="conversation-result">${icon('codex')}<div>${esc(t('产品官网 · 开发任务'))}<small>${esc(a.flowStatus)}</small></div></div>`:reply(2)+`<button class="flow-artifact" data-open-report="site"><span>↳</span><span>${esc(a.siteBundle)}<small>${esc(a.siteBundleSub)}</small></span><span>↗</span></button>`+user(2)+route(a.flowRoute[2],a.flowRoute[3]));
 pressed('[data-flow]','flow',flow);animate($('#flow-panel'));
}
function reportData(){const i=['research','organize','watch','site'].indexOf(report);return {...d().reports[i],title:[t('建站方案对比报告'),d().extraTasks.organize.title,d().extraTasks.watch.title,d().siteReportTitle][i]};}
function reportBody(r){return (r.table?'<table><thead><tr>'+r.table[0].map(v=>'<th scope="col">'+esc(v)+'</th>').join('')+'</tr></thead><tbody>'+r.table.slice(1).map(row=>'<tr>'+row.map(v=>'<td>'+esc(v)+'</td>').join('')+'</tr>').join('')+'</tbody></table>':'')+r.sections.map(s=>'<h3>'+esc(s.title)+'</h3>'+(s.paragraphs||[]).map(v=>'<p>'+esc(v)+'</p>').join('')+(s.items?'<ul>'+s.items.map(v=>'<li>'+esc(v)+'</li>').join('')+'</ul>':'')).join('');}
function openReport(){const r=reportData();$('#report-content').innerHTML='<h2 id="report-title">'+esc(r.title)+'</h2><p>'+esc(r.intro)+'</p>'+reportBody(r);if(!$('#report-dialog').open)$('#report-dialog').showModal();}
function downloadReport(){const r=reportData(),lines=[r.title,d().sample,'',r.intro,''];if(r.table)r.table.forEach(row=>lines.push(row.join(' | ')));r.sections.forEach(s=>lines.push('',s.title,...(s.paragraphs||[]),...(s.items||[]).map(v=>'• '+v)));const url=URL.createObjectURL(new Blob([lines.join('\n')],{type:'text/plain;charset=utf-8'})),a=document.createElement('a');a.href=url;a.download=`Pot-${r.title}-${d().sample}.txt`;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}
function restoreQuery(){const q=new URLSearchParams(location.search);if(keys.includes(q.get('agent')))agent=q.get('agent');if(['direct','delegate'].includes(q.get('mode')))mode=q.get('mode');if(taskKeys.includes(q.get('task')))task=q.get('task');if(['0','1','2'].includes(q.get('flow')))flow=Number(q.get('flow'));if(['0','1','2','3'].includes(q.get('phase')))taskPhase=Number(q.get('phase'));if(['research','organize','watch','site'].includes(q.get('report')))report=q.get('report');return q.has('report');}
function section(){const ids=['main','assistant','agent-link','workflow','start'];return '#'+(ids.filter(id=>$('#'+id).getBoundingClientRect().top<innerHeight/2).pop()||'main');}
function changeLanguage(next,persist=true){
 if(!PotLocale.codes.includes(next)||next===lang)return;
 if(persist)PotLocale.save(next);
 const hash=section(),wasOpen=$('#report-dialog').open;clearTimers();
 if(window.POT_PREVIEW){const page=window.POT_PREVIEW[next];document.body.innerHTML=page.body;document.documentElement.lang=next;document.title=page.title;$('meta[name="description"]').content=page.description;data=page.data;lang=next;renderAll();if(wasOpen)openReport();const target=$(hash);if(hash==='#main')window.scrollTo({top:0,behavior:'instant'});else if(target)target.scrollIntoView({behavior:'instant'});$('#language-select').focus({preventScroll:true});}
 else{const url=new URL(data.assetBase+next+'/',location.href);url.search=new URLSearchParams({agent,mode,task,flow:String(flow),phase:String(taskPhase),...(wasOpen?{report}: {})}).toString();url.hash=hash==='#main'?'':hash;location.assign(url.href);}
}
function renderAll(){renderAgent();renderTask();if(taskPhase<3)runTask(taskPhase);renderFlow();$('#language-select').value=lang;}
document.addEventListener('change',e=>{if(e.target.id==='language-select')changeLanguage(e.target.value);});
document.addEventListener('click',e=>{
 const b=e.target.closest('button');if(!b||b.disabled)return;
 if(b.dataset.agent){agent=b.dataset.agent;renderAgent();}
 else if(b.dataset.mode){mode=b.dataset.mode;renderAgent();}
 else if(b.dataset.task){task=b.dataset.task;taskPhase=3;renderTask();}
 else if(b.dataset.flow!==undefined){flow=Number(b.dataset.flow);renderFlow();}
 else if(b.dataset.openReport){report=b.dataset.openReport;openReport();}
 else if(b.id==='run-task')runTask();else if(b.id==='close-dialog')$('#report-dialog').close();else if(b.id==='download-report')downloadReport();
});
document.addEventListener('click',e=>{if(e.target.id!=='report-dialog')return;const r=e.target.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)e.target.close();});
const showReport=restoreQuery();renderAll();if(showReport)openReport();
if(window.POT_PREVIEW)changeLanguage(PotLocale.preferred(),false);
})();
