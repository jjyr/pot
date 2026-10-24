// Browser-side regression checks shared by the computer-use fixture and browser runner.
// Uses production pages, geometry and native focus/scroll behavior with synthetic data.
export async function verifyScroll(f) {
 const checks=[],params=new URLSearchParams(location.search);
 const status=document.createElement('output');status.id='scroll-verification';status.setAttribute('aria-live','polite');
 Object.assign(status.style,{position:'fixed',right:'4px',bottom:'4px',zIndex:'10000',background:'#fff',color:'#111',font:'12px monospace',maxWidth:'90vw',padding:'4px'});document.body.append(status);
 const assert=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
 const frame=()=>new Promise(resolve=>requestAnimationFrame(resolve));
 const settle=async()=>{for(let i=0;i<4;i++)await frame();};
 const until=async test=>{const start=performance.now();while(!test()){if(performance.now()-start>10000)throw Error('Timed out waiting for page');await frame();}await settle();};
 const host=()=>document.querySelector('.page-host');
 const ready=()=>document.querySelector('.task-list')?.getAttribute('aria-busy')==='false';
 const tab=name=>document.querySelector('.tasks-content>.segments').children[name==='tasks'?0:1].click();
 const visible=node=>{const r=node.getBoundingClientRect(),h=host().getBoundingClientRect();return r.top>=h.top-1&&r.bottom<=h.bottom+1;};
 const geometry=selector=>{
  const list=document.querySelector('.record-group-list'),rows=[...document.querySelectorAll(selector)],pager=document.querySelector('[data-view-key="pagination-bottom"]');
  list.scrollTop=9999;
  assert(list.scrollHeight<=list.clientHeight+1&&list.scrollTop===0,'list has no independent scroll range');
  assert(Math.abs(pager.offsetTop-list.offsetTop-list.offsetHeight)<=12,'bottom pager follows the list');
  host().scrollTop=host().scrollHeight;
  assert(visible(rows.at(-1))&&visible(pager),'page scroll reaches final record and bottom pager');
  assert(host().scrollHeight<=host().clientHeight||host().scrollTop>0,'overflow scrolls the page host');
  assert(host().scrollWidth<=host().clientWidth,'page fits its viewport');
  return rows;
 };
 const refresh=async selector=>{
  const row=document.querySelector(selector),group=row.closest('.record-group'),detail=group.querySelector('details');detail.open=true;
  host().scrollTop=Math.min(300,host().scrollHeight-host().clientHeight);row.focus({preventScroll:true});
  const top=host().scrollTop,calls=f.calls.length;f.change();await until(()=>f.calls.length>calls);await until(()=>host().querySelector('[aria-busy="true"]')===null);
  assert(row===document.querySelector(selector)&&group===row.closest('.record-group'),'refresh retains keyed rows and groups');
  assert(detail.open&&document.activeElement===row,'refresh retains disclosure and focus');
  assert(Math.abs(host().scrollTop-top)<=1,'refresh retains page reading position');
  detail.open=false;
 };
 const pagination=async selector=>{
  const bottom=document.querySelector('[data-view-key="pagination-bottom"] [data-page-direction="next"]');
  if(bottom.disabled)return;
  host().scrollTop=host().scrollHeight;bottom.focus({preventScroll:true});bottom.click();
  await until(()=>document.querySelector('.record-page-summary')?.textContent.match(/(?:Page|第)\s*2/)&&!document.querySelector('[aria-busy="true"]'));
  assert(document.activeElement.closest('[data-view-key="pagination-bottom"]')&&visible(document.activeElement),'bottom pagination focus stays visible');
  geometry(selector);
  document.querySelector('[data-view-key="pagination-top"] [data-page-direction="previous"]').click();
  await until(()=>document.querySelector('.record-page-summary')?.textContent.match(/(?:Page|第)\s*1/)&&!document.querySelector('[aria-busy="true"]'));
  assert(document.activeElement.closest('[data-view-key="pagination-top"]')&&visible(document.activeElement),'top pagination focus stays visible');
 };
 const search=async(selector,listSelector)=>{
  const input=document.querySelector(selector);input.value='no matching scroll fixture';input.dispatchEvent(new Event('input',{bubbles:true}));
  await until(()=>document.querySelector('.page-empty')&&!document.querySelector('[aria-busy="true"]'));
  assert(!document.querySelector(listSelector)&&host().scrollTop===0,'empty search returns to page top');
  input.value='';input.dispatchEvent(new Event('input',{bubbles:true}));await until(()=>document.querySelector(listSelector)&&!document.querySelector('[aria-busy="true"]'));
 };
 try{
  document.querySelector('.rail-button[aria-label="Tasks"],.rail-button[aria-label="任务"]').click();await until(ready);
  if(params.has('small'))assert(document.querySelectorAll('.task-row').length===3&&document.querySelectorAll('.record-group').length===2,'three tasks in two assistant groups');
  geometry('.task-row');await refresh('.task-row');await pagination('.task-row');await search('.task-search input','.task-row');
  host().scrollTop=Math.min(180,host().scrollHeight-host().clientHeight);const taskTop=host().scrollTop;
  tab('schedules');await until(()=>document.querySelector('.schedule-record')&&!document.querySelector('[aria-busy="true"]'));
  if(params.has('small'))assert(document.querySelectorAll('.schedule-record').length===3&&document.querySelectorAll('.record-group').length===2,'three schedules in two assistant groups');
  geometry('.schedule-record');await refresh('.schedule-record');await pagination('.schedule-record');await search('.task-search input','.schedule-record');
  tab('tasks');await until(ready);
  assert(Math.abs(host().scrollTop-taskTop)<=1,'returning to tasks restores the page position');
  assert(f.errors.length===0,'no component errors');
  f.scrollVerification={passed:true,checks};status.textContent=`PASS ${checks.length} scroll checks (${innerWidth}px)`;
 }catch(error){f.scrollVerification={passed:false,checks,error:error.stack};status.textContent=`FAIL: ${error.message}`;console.error(error);}
 parent.postMessage({type:'tasks-scroll-result',...f.scrollVerification,width:innerWidth},location.origin);
}
