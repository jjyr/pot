import {node as n,action,field} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

const known=['mention','team_mention','review_requested','author','comment','subscribed','assign','ci_activity','invitation','manual','security_alert','state_change'];
const split=value=>value.split(',').map(v=>v.trim()).filter(Boolean);
const healthy=new Set(['ready','baseline']);

// The account view owns the single disclosure. This form shares its reconciliation
// so language/status changes preserve live controls, drafts and focus.
export function createPluginNotifications({request,scope,signal,locale,refresh,render}) {
 const t=key=>translate(locale(),key);
 let current=null,draft=null,busy=false,error=false,identity=null,showDetails=false,diagnosticError='';
 const item=()=>current?.items?.find(i=>i.id==='github');
 const fields=['repositories','subject_types','threads'];
 const from=value=>({enabled:value.enabled,assistant:value.assistant,account_id:identity||value.config.account_id||'',...Object.fromEntries(fields.flatMap(key=>[[key,(value.config[key]||[]).join(', ')],[key+'Mode',value.config[key]==null?'all':'selected']])),reasons:[...(value.config.reasons??['mention'])],reasonMode:value.config.reasons===null?'all':'selected',revision:value.revision});
 let requestID=null;

 let dirty=false;
 const edit=change=>{if(busy)return;change();dirty=true;requestID=null;};
 return {
  update(value,account){
   const next=account?.identity.id||null;
   if(identity!==next){identity=next;draft=null;dirty=false;error=false;showDetails=false;requestID=null;}
   current=value;
   if(item()&&(!draft||(!dirty&&!busy)))draft=from(item());
  },
  summary(){
   const value=item();
   if(!value)return {text:t('pluginNotifications.summary.error'),error:true};
   if(value.attention)return {text:t('pluginNotifications.summary.error'),error:true};
   if(!value.enabled)return {text:t('pluginNotifications.summary.disabled')};
   if(value.status==='pending')return {text:t('pluginNotifications.summary.pending')};
   if(value.status==='waiting_assistant')return {text:t('pluginNotifications.summary.waiting')};
   if(!healthy.has(value.status)||value.attention)return {text:t('pluginNotifications.summary.error'),error:true};
   const assistant=current.assistants?.find(a=>a.id===value.assistant);
   return {text:t('pluginNotifications.summary.target').replace('{name}',assistant?.name||value.assistant)};
  },
  form(){
   const projection=n('section','github-notifications');projection.dataset.viewKey='notification-form';
   const value=item();if(!value||!draft){projection.append(n('p','inline-error',t('pluginNotifications.status.plugin_missing')));return projection;}
   projection.append(n('h3','',t('pluginNotifications.title')),n('p','hint',t('pluginNotifications.description')),n('p','hint',t('pluginNotifications.filterHelp')));
   projection.append(n('p','hint',t('pluginNotifications.account').replace('{id}',identity||'')),n('p','hint',t('pluginNotifications.savedFilters')),n('p','hint',t('pluginNotifications.revision').replace('{revision}',String(value.revision))));
   const saved=n('ul','hint');
   for(const [key,label]of [['repositories','repositories'],['reasons','reasons'],['subject_types','subject_types'],['threads','threads']]){
    const values=value.config[key];saved.append(n('li','',t('pluginNotifications.savedField').replace('{field}',t('pluginNotifications.'+label)).replace('{values}',values==null?t('pluginNotifications.any'):values.length?values.join(', '):t('pluginNotifications.none'))));
   }
   projection.append(saved);
   const status=n('p',healthy.has(value.status)||!value.enabled||['pending','waiting_assistant'].includes(value.status)?'hint':'inline-error',t('pluginNotifications.status.'+value.status));status.dataset.viewKey='notification-status';status.setAttribute('role','status');projection.append(status);
   if(value.queued)projection.append(n('p','hint',t('pluginNotifications.queued').replace('{count}',String(value.queued))));
   if(value.attention)projection.append(n('p','inline-error',t('pluginNotifications.attention')));
   if(!healthy.has(value.status)&&value.enabled||value.attention){
    const details=action(t('pluginNotifications.details'),()=>{showDetails=!showDetails;render();});details.setAttribute('aria-expanded',String(showDetails));details.setAttribute('aria-controls','github-notification-diagnostics');projection.append(details);
    const diagnostic=n('pre','notification-diagnostics',JSON.stringify({status:value.status,diagnostic:value.diagnostic,assistant:value.assistant,revision:value.revision,next:value.next,queued:value.queued,attention:value.attention},null,2));diagnostic.id='github-notification-diagnostics';diagnostic.hidden=!showDetails;projection.append(diagnostic);
   }
   const enabled=field(t('pluginNotifications.enabled'),draft.enabled,{kind:'checkbox'});enabled.input.onchange=event=>{edit(()=>{draft.enabled=event.currentTarget.checked;});render();};
   const choices=(current.assistants||[]).map(a=>[a.id,a.name]);
   if(!choices.some(([id])=>id===draft.assistant))choices.unshift([draft.assistant,t(draft.assistant?'pluginNotifications.status.assistant_missing':choices.length?'pluginNotifications.chooseAssistant':'pluginNotifications.noAssistants')]);
   const target=field(t('pluginNotifications.assistant'),draft.assistant,{choices});target.input.onchange=event=>{edit(()=>{draft.assistant=event.currentTarget.value;});render();};
   projection.append(enabled.row,target.row);
   for(const key of fields){
    const mode=field(t('pluginNotifications.'+key),draft[key+'Mode'],{choices:[['all',t('pluginNotifications.any')],['selected',t('pluginNotifications.selectedValues')]]});
    mode.input.onchange=event=>{edit(()=>{draft[key+'Mode']=event.currentTarget.value;});render();};projection.append(mode.row);
    if(draft[key+'Mode']==='selected'){
     const values=field(t('pluginNotifications.values').replace('{field}',t('pluginNotifications.'+key)),draft[key]);values.input.oninput=event=>edit(()=>{draft[key]=event.currentTarget.value;});projection.append(values.row);
    }
   }
   projection.append(n('p','hint',t('pluginNotifications.typeHelp')),n('p','hint',t('pluginNotifications.threadHelp')));
   const reasonMode=field(t('pluginNotifications.scope'),draft.reasonMode,{choices:[['all',t('pluginNotifications.all')],['selected',t('pluginNotifications.selected')]]});
   reasonMode.input.onchange=event=>{edit(()=>{draft.reasonMode=event.currentTarget.value;});render();};projection.append(reasonMode.row);
   if(draft.reasonMode==='selected'){
    const reasons=n('fieldset','notification-reasons');reasons.append(n('legend','form-label',t('pluginNotifications.reasons')),n('p','hint',t('pluginNotifications.reasonHelp')));
    const options=n('div','notification-reason-options');
    for(const reason of [...new Set([...known,...draft.reasons])]){
     const option=field(known.includes(reason)?t('pluginNotifications.reason.'+reason):reason,draft.reasons.includes(reason),{kind:'checkbox'});option.row.dataset.viewKey=reason;
     option.input.onchange=event=>{edit(()=>{const values=new Set(draft.reasons);if(event.currentTarget.checked)values.add(reason);else values.delete(reason);draft.reasons=[...values];});render();};options.append(option.row);
    }
    reasons.append(options);projection.append(reasons);
   }
   const save=action(t('pluginNotifications.save'),async()=>{
    if(busy)return;const savingIdentity=identity,submitted=draft;busy=true;error=false;render();
    const config={account_id:draft.account_id,...Object.fromEntries(fields.map(key=>[key,draft[key+'Mode']==='all'?null:split(draft[key])])),reasons:draft.reasonMode==='all'?null:draft.reasons};
    requestID ||= crypto.randomUUID();
    try{
     const response=await request('/api/v1/plugin-notifications/github',{...scope,signal,method:'POST',body:{request_id:requestID,enabled:draft.enabled,assistant:draft.assistant,revision:draft.revision,config}});
     if(!signal.aborted&&identity===savingIdentity&&draft===submitted){const saved=response.items.find(i=>i.id==='github');if(saved.revision<(item()?.revision||0))throw Error('stale_notification_response');current=response;draft=from(saved);dirty=false;requestID=null;await refresh();}
    }catch(cause){if(identity===savingIdentity){error=true;diagnosticError=cause.message||String(cause);}}finally{busy=false;if(!signal.aborted)render();}
   });save.disabled=busy||(draft.enabled&&!draft.assistant);projection.append(save);
   if(error){const message=n('p','inline-error',t('pluginNotifications.error'));message.dataset.viewKey='save-error';message.setAttribute('role','alert');projection.append(message);if(diagnosticError){const raw=n('pre','notification-diagnostics',diagnosticError);projection.append(raw);}}
   for(const control of projection.querySelectorAll('input,select,button'))control.disabled=busy||control.disabled;
   return projection;
  }
 };
}
