import {node as n,action,field} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';
import {saveAllowanceConfiguration} from '../state/provider-save.mjs';

export function allowanceFreshness(account,window,snapshot,now=Date.now()) {
 const sources=[account.as_of,account.read_at].filter(Boolean),times=[snapshot.read_at,...sources].map(value=>Date.parse(value));
 if(!sources.length||times.some(value=>!Number.isFinite(value)))return 'unknownObservation';
 if(times.some(value=>now-value>15*60*1000||value-now>30000)||(window.resets_at&&Date.parse(window.resets_at)<=now))return 'staleUsage';
 return '';
}
const monitorValues=monitor=>({enabled:monitor.enabled,threshold:monitor.threshold,assistant:monitor.assistant||''});

// All mutable reminder state lives in caller-owned, connection-scoped memory.
// Metadata refreshes and locale/page reconstruction never replace a dirty draft.
export function createProviderAllowances({root,request,scope,profileID,locale,current=()=>true,available=()=>true,identity=()=>[profileID],binding=()=>profileID,memory={}}) {
 const t=key=>translate(locale(),'providers.'+key),disclosure=n('details','provider-allowances'),summary=n('summary'),content=n('div','provider-allowance-content'),status=n('p','hint'),accountsRoot=n('div'),reminder=n('div','provider-allowance-reminder'),message=n('p','hint');
 disclosure.dataset.cacheKey='provider-allowances:'+profileID;status.setAttribute('role','status');message.setAttribute('role','status');
 let pending=false,version=0,controller,disposed=false;
 const active=()=>!disposed&&current()&&root.isConnected!==false,date=value=>{const time=new Date(value);return Number.isNaN(time.valueOf())?value:time.toLocaleString(locale());};
 const enabled=field('',false,{kind:'checkbox'}),threshold=field('',90,{kind:'number',min:1,max:100,required:true}),assistant=field('','',{choices:[]});
 const dirty=()=>memory.draft&&JSON.stringify(memory.draft)!==JSON.stringify(memory.baseline);
 function accept(value){memory.needsRead=false;memory.view=value;if(value.monitor&&!dirty()){memory.draft=monitorValues(value.monitor);memory.baseline=structuredClone(memory.draft);memory.revision=value.monitor.revision;}}
 function synchronizeIdentity(){
  const signature=JSON.stringify(identity()),owner=String(binding());
  if(memory.signature===signature)return false;
  const changedBinding=memory.binding!==undefined&&memory.binding!==owner;
  controller?.abort();version++;pending=false;memory.saving=false;delete memory.write;
  memory.signature=signature;memory.binding=owner;memory.needsRead=true;
  if(changedBinding){delete memory.draft;delete memory.baseline;delete memory.revision;delete memory.message;delete memory.view;}
  else if(memory.view){const {monitor,assistants}=memory.view;memory.view={monitor,assistants,status:'pending',snapshot:null};}
  return true;
 }
 function changed(){memory.draft={enabled:enabled.input.checked,threshold:Number(threshold.input.value),assistant:assistant.input.value};memory.message='';renderControls();}
 for(const item of [enabled,threshold,assistant])item.input.addEventListener('input',changed);
 const refresh=action('',()=>load(true)),save=action('',async()=>{
  synchronizeIdentity();if(memory.saving||!memory.draft||!available()||!threshold.input.reportValidity())return;
  const desired=structuredClone(memory.draft),revision=memory.revision,signature=memory.signature,write=memory.write={};memory.saving=true;memory.message='';renderControls();
  try{const value=await saveAllowanceConfiguration({request,scope,profileID,values:desired,revision});if(memory.signature!==signature||memory.write!==write)return;memory.needsRead=false;memory.view=value;memory.baseline=desired;memory.revision=value.monitor.revision;if(JSON.stringify(memory.draft)===JSON.stringify(desired))memory.draft=monitorValues(value.monitor);memory.message='reminderSaved';}
  catch{if(memory.signature===signature&&memory.write===write)memory.message='reminderFailed';}
  finally{if(memory.write===write){memory.saving=false;delete memory.write;memory.render?.();}}
 },{primary:true});
 const reload=action('',()=>{if(dirty()&&!window.confirm(t('discardReminder')))return;delete memory.draft;delete memory.baseline;memory.message='';load();});
 reminder.append(enabled.row,threshold.row,assistant.row,n('p','hint'),save,reload,message);content.append(status,refresh,accountsRoot,reminder);disclosure.append(summary,content);root.append(disclosure);
 function label(item,key){const text=t(key);item.row.querySelector('.form-label').textContent=text;item.input.setAttribute('aria-label',text);}
 function renderControls(){
  reminder.hidden=!memory.view?.monitor;save.textContent=t('saveReminder');reload.textContent=t('reloadReminder');message.textContent=memory.message?t(memory.message):'';message.className=memory.message==='reminderFailed'?'inline-error':'hint';
  for(const [item,key]of [[enabled,'allowanceEnabled'],[threshold,'threshold'],[assistant,'assistant']])label(item,key);
  reminder.querySelector('p.hint').textContent=t('allowanceDefaults');
  if(memory.draft){enabled.input.checked=memory.draft.enabled;if(document.activeElement!==threshold.input)threshold.input.value=memory.draft.threshold;
   const choices=[['',t('waitingAssistant')],...(memory.view?.assistants||[]).map(item=>[item.id,item.name])];if(memory.draft.assistant&&!choices.some(item=>item[0]===memory.draft.assistant))choices.push([memory.draft.assistant,t('status.assistant_missing')]);
   const signature=JSON.stringify(choices);if(assistant.signature!==signature){assistant.signature=signature;assistant.input.replaceChildren();for(const [value,label]of choices){const option=n('option','',label);option.value=value;assistant.input.append(option);}}assistant.input.value=memory.draft.assistant;
  }
  for(const item of [enabled,threshold,assistant])item.input.disabled=!!memory.saving||!available();save.disabled=!!memory.saving||!available()||!dirty();reload.disabled=!!memory.saving||pending||!available();
 }
 function render(){
  if(disposed||!current())return;synchronizeIdentity();summary.textContent=t('allowances');refresh.textContent=t('allowanceRefresh');refresh.disabled=pending||!!memory.saving||!available();accountsRoot.replaceChildren();renderControls();
  const view=memory.view;if(!available()){status.textContent=t('unavailable');return;}if(pending&&!view){status.textContent=t('allowanceLoading');return;}
  const code=view?.status||'unavailable';status.textContent=view?t('status.'+code):'';status.className=code==='ready'?'hint':'inline-error';
  if(!view?.snapshot)return;const accounts=view.snapshot.accounts||[];
  if(!accounts.length)accountsRoot.append(n('p','hint',t('allowanceEmpty')));
  for(const account of accounts){const entry=n('section','provider-allowance-account');entry.append(n('strong','',account.name||account.provider||t('quotaAccount')));if(account.user||account.provider&&account.provider!==account.name)entry.append(n('p','hint',[account.provider,account.user].filter(Boolean).join(' · ')));if(account.plan)entry.append(n('p','hint',t('plan').replace('{plan}',account.plan)));
   for(const window of account.windows||[]){const row=n('div','provider-allowance-window'),used=window.unlimited?t('unlimited'):window.used==null?t('allowanceUnknown'):t('usedPercent').replace('{used}',Number(window.used).toLocaleString(locale(),{maximumFractionDigits:1}));row.append(n('span','',window.name||t('quotaWindow')),n('span','',used));
    const freshness=allowanceFreshness(account,window,view.snapshot);if(freshness)row.append(n('small','hint',t(freshness)));
    if(window.used!=null&&!window.unlimited){const meter=n('progress');meter.max=100;meter.value=Math.min(100,window.used);meter.setAttribute('aria-label',(window.name||t('quotaWindow'))+': '+used);row.append(meter);}if(window.resets_at)row.append(n('small','hint',t('resets').replace('{time}',date(window.resets_at))));if(window.pool||account.pool)row.append(n('small','hint',t('pool').replace('{pool}',window.pool||account.pool)));entry.append(row);
   }
   if(account.until)entry.append(n('p','hint',t(account.renew==='auto'?'renews':'until').replace('{time}',date(account.until))));const observed=account.as_of||account.read_at;if(observed)entry.append(n('small','hint',t('observed').replace('{time}',date(observed))));accountsRoot.append(entry);
  }
 }
 async function load(force=false){synchronizeIdentity();if(pending||!active()||!available())return;pending=true;const captured=++version,signature=memory.signature;controller?.abort();const signal=(controller=new AbortController()).signal;render();try{const value=await request('/api/v1/providers/allowances'+(force?'':'?profile_id='+encodeURIComponent(profileID)),{...scope,signal,...(force?{method:'POST',body:{profile_id:profileID,action:'refresh'}}:{})});if(!active()||captured!==version||signature!==JSON.stringify(identity())||signature!==memory.signature)return;accept(value);}catch(reason){if(!active()||captured!==version||signature!==JSON.stringify(identity())||signature!==memory.signature||reason.name==='AbortError')return;memory.view={...memory.view,status:'unavailable'};}finally{if(captured===version&&active()&&signature===memory.signature){pending=false;render();}}}
 memory.render=render;synchronizeIdentity();disclosure.addEventListener('toggle',()=>{if(disclosure.open&&(!memory.view||memory.needsRead))load();});const update=()=>{synchronizeIdentity();render();if(disclosure.open&&(!memory.view||memory.needsRead))load();};update.dispose=()=>{disposed=true;version++;controller?.abort();};update.remove=()=>{update.dispose();delete memory.view;delete memory.signature;memory.needsRead=true;memory.saving=false;delete memory.write;disclosure.remove();};render();return update;
}
