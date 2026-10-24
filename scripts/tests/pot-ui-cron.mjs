// Run in TestHistoryBrowserFixture's private Control via a browser module host.
// Real editor, HTTP preview and persistence; no model calls or external work.
import {createPages} from '../../web/src/components/pages.mjs';
import {mutationTopics} from '../../web/src/adapters/production.mjs';

export async function runCronRegression(host=document.querySelector('main')) {
 const checks=[],assert=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
 const wait=async read=>{for(let i=0;i<300;i++){if(read())return;await new Promise(resolve=>setTimeout(resolve,20));}throw Error('Timed out: '+read);};
 const api=async(path,options={})=>{
  const url=new URL(path,location.origin);url.searchParams.set('assistant_id',options.owner||'main');
  const response=await fetch(url,{method:options.method||'GET',headers:{'Content-Type':'application/json'},body:options.body?JSON.stringify(options.body):undefined});
  const result=await response.json();if(!response.ok)throw Error(JSON.stringify(result));return result;
 };
 let locale='en',hold=false;const pending=[],writes=[];
 const client={desktop:false,runtime:await api('/api/v1/runtime'),spaces:(await api('/api/v1/spaces')).items,assistants:[{id:'main',name:'Fixture',space_id:'local',capabilities:{version:1,operations:['schedules.manage','tasks.list']}}],scope:()=>({space:'local',owner:'main'}),request:(path,options={})=>{
  if(path==='/api/v1/schedules'&&options.method==='POST')writes.push(options.body);
  if(path==='/api/v1/schedules/preview'&&hold)return new Promise(resolve=>pending.push(async()=>resolve(await api(path,options))));
  return api(path,options);
 }};
 const pages=createPages({client,host,approvalHost:document.createElement('div'),chatApprovalHost:document.createElement('div'),chatVisible:()=>false,locale:()=>locale,notify:()=>{},changed:()=>{},titleChanged:value=>document.querySelector('h1').textContent=value,openChat:()=>{}});
 const button=text=>[...host.querySelectorAll('button')].find(item=>item.textContent===text);
 const input=label=>host.querySelector(`[aria-label="${label}"]`);
 const fill=(label,value)=>{const control=input(label);control.value=value;control.dispatchEvent(new Event('input',{bubbles:true}));};
 const saved=[];
 try {
  await pages.show('tasks');button('Schedules').click();await wait(()=>button('New schedule'));
  const native=Intl.DateTimeFormat;
  Intl.DateTimeFormat=function(...args){const formatter=new native(...args),original=formatter.resolvedOptions.bind(formatter);formatter.resolvedOptions=()=>({...original(),timeZone:'Pacific/Auckland'});return formatter;};
  try{button('New schedule').click();}finally{Intl.DateTimeFormat=native;}
  assert(input('Time zone').value==='Pacific/Auckland','UI initializes the browser system IANA zone once');
  fill('Before-run script (JSON argv, optional)','["python3","/fixture/probe.py"]');fill('Script working directory','/fixture');fill('Script timeout (seconds, 1–300)','12');
  fill('Title','Cron browser regression');fill('Goal','Isolated browser fixture only');
  fill('Cron expression','0 8-20 * * * *');await wait(()=>input('Cron expression').getAttribute('aria-invalid')==='true');
  assert(host.textContent.includes('Check the schedule rule.'),'Six fields produce an error at the Cron field');
  fill('Time zone','Local');await wait(()=>input('Time zone').getAttribute('aria-invalid')==='true');
  locale='zh-CN';window.dispatchEvent(new Event('pot:locale'));
  assert(input('执行前脚本（JSON argv，可选）').value==='["python3","/fixture/probe.py"]'&&input('脚本工作目录').value==='/fixture','Locale refresh preserves the argv/cwd probe draft');
  assert(input('时区').value==='Local'&&host.textContent.includes('请输入有效的 IANA 时区。'),'Pending/error locale refresh keeps draft and translates field errors');
  locale='en';window.dispatchEvent(new Event('pot:locale'));
  fill('Time zone','Europe/Berlin');fill('Cron expression','0 8-20 * * *');await wait(()=>host.querySelectorAll('.schedule-preview li').length===5);
  assert(input('Time zone').value==='Europe/Berlin','Explicit override survives locale refresh');
  hold=true;fill('Cron expression','0 7 * * *');await wait(()=>pending.length===1);
  hold=false;fill('Cron expression','0 8-20 * * *');await wait(()=>host.querySelectorAll('.schedule-preview li').length===5);
  await pending.shift()();await new Promise(resolve=>setTimeout(resolve,30));
  assert(host.querySelector('.schedule-preview code').textContent==='0 8-20 * * *','Late cancelled preview cannot replace the latest rule');
  input('Enabled').click();button('Save').click();await wait(()=>!host.querySelector('form'));
  const write=writes.find(item=>item.action==='create');assert(write.spec.timezone==='Europe/Berlin'&&write.spec.cron==='0 8-20 * * *'&&!('weekdays' in write.spec),'Save submits the explicit IANA zone and only the Cron calendar fields');
  let page=await api('/api/v1/schedules');let info=page.items.find(item=>item.spec.title==='Cron browser regression');saved.push(info.id);
  const detail=await api('/api/v1/schedules?selected='+info.id);assert(JSON.stringify(detail.selected.spec.before_run)===JSON.stringify({command:['python3','/fixture/probe.py'],cwd:'/fixture',timeout_seconds:12}),'UI writes the same probe configuration as Agent/API');
  assert(info.spec.kind==='cron'&&info.spec.timezone==='Europe/Berlin','Real HTTP persistence retains Cron and saved zone');
  await pages.show('tasks',{force:true});await wait(()=>button('Cron browser regression'));button('Cron browser regression').click();await wait(()=>button('Edit'));button('Edit').click();await wait(()=>input('Cron expression'));
  assert(input('Time zone').value==='Europe/Berlin'&&input('Cron expression').value==='0 8-20 * * *','Reload/edit keeps the saved zone despite a different browser system zone');
  fill('Cron expression','15 8-20 * * *');await pages.refresh(['schedules']);locale='zh-CN';window.dispatchEvent(new Event('pot:locale'));
  assert(input('执行前脚本（JSON argv，可选）').value==='["python3","/fixture/probe.py"]','Background refresh preserves the saved probe draft');
  assert(input('Cron 表达式').value==='15 8-20 * * *'&&input('时区').value==='Europe/Berlin','Background refresh and locale switch preserve unsaved Cron and timezone');
  assert(!host.querySelector('[aria-label="Weekdays"]'),'Calendar editor has one Cron input');
  const leaving=pages.back();await wait(()=>document.querySelector('dialog[open]'));
  [...document.querySelectorAll('dialog button')].find(item=>item.textContent==='继续编辑').click();await leaving;
  assert(input('Cron 表达式').value==='15 8-20 * * *','Dirty exit guard retains the unsaved Cron draft');
  if(innerWidth<720){
    const controls=[input('Cron 表达式'),input('时区')];
    assert(controls.every(control=>control.getBoundingClientRect().width>100&&control.getBoundingClientRect().right<=innerWidth),'Mobile Cron and timezone inputs fit the viewport');
  }
  assert(mutationTopics('/api/v1/schedules/preview',{}, {})===null,'Read-only preview never invalidates schedule resources');
  return checks;
 } finally {
  pages.dispose();for(const id of saved)await api('/api/v1/schedules',{method:'POST',body:{request_id:crypto.randomUUID(),action:'delete',schedule_id:id}});
 }
}
