import {node as n,action,field} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';
const split=value=>value.split(',').map(v=>v.trim()).filter(Boolean);
// Generic list-filter subscription editor. Field names/options and all service
// semantics come from the plugin's MCP projection, not a host-side rules model.
export function createPluginNotifications({request,scope,signal,locale,onProjection=()=>{},render,pluginID}){
 const t=key=>translate(locale(),key);
 let current=null,identity=null,generation=0,error='',adding=false;
 const pending=new Set();
 const drafts=new Map();
 const settings=()=>current?.items?.find(item=>item.id===pluginID)?.settings;
 const subscriptions=()=>settings()?.subscriptions||[];
 const from=sub=>({id:sub.id,revision:sub.revision,enabled:sub.enabled,assistant:sub.assistant,filters:structuredClone(sub.filters),dirty:false,version:0,requestID:null});
 const draftFor=sub=>{if(!drafts.has(sub.id))drafts.set(sub.id,from(sub));return drafts.get(sub.id);};
 const edit=(draft,change)=>{change();draft.version++;draft.dirty=true;draft.requestID=null;};
 const save=async(draft,remove=false)=>{
 if(pending.has(draft)||draft.acceptedUnknown||!identity||signal.aborted)return;
 const account=identity,epoch=generation;draft.requestID ||= crypto.randomUUID();
 const attempt=draft.retry||{submitted:structuredClone({...draft,retry:undefined}),version:draft.version,remove};draft.retry=attempt;
 const {submitted,version}=attempt;remove=attempt.remove;
 pending.add(draft);error='';render();
 const tool=remove?'delete_subscription':submitted.id==='new'?'create_subscription':'update_subscription';
 const args=remove?{id:submitted.id,revision:submitted.revision,request_id:submitted.requestID}:{...(submitted.id==='new'?{}:{id:submitted.id,revision:submitted.revision}),request_id:submitted.requestID,account_id:account,assistant:submitted.assistant,enabled:submitted.enabled,filters:submitted.filters};
 try{
 const value=await request('/api/v1/plugin-notifications/'+pluginID,{...scope,signal,method:'POST',body:{tool,arguments:args}});
 if(!signal.aborted&&identity===account&&generation===epoch){
   const incoming=value.items?.find(i=>i.id===pluginID)?.settings;
   // Concurrent independent rows can finish out of order. Only the newer full
   // projection replaces current, but each exact write acknowledges its draft.
   const older=(incoming?.revision||0)<(settings()?.revision||0),sameAccount=!incoming?.account_id||incoming.account_id===account;
   // A complete display list can aid reconciliation even when the command
   // receipt is missing; it never identifies or acknowledges this local draft.
   if(incoming&&sameAccount&&!older){current=value;onProjection(value);}
   const savedID=value.operation?.subscription_id;
   if(!incoming||!sameAccount||!savedID||submitted.id!=='new'&&savedID!==submitted.id){draft.acceptedUnknown=true;throw Error('missing_subscription_receipt');}
   const acknowledgedRevision=submitted.id==='new'?1:submitted.revision+1,latest=subscriptions().find(sub=>sub.id===savedID),superseded=older&&(!latest||latest.revision>acknowledgedRevision);
   if(superseded)error='stale_notification_response';draft.retry=null;
   if(draft.version===version&&!superseded){drafts.delete(submitted.id);if(submitted.id==='new')adding=false;}
   else{draft.dirty=true;drafts.delete(submitted.id);draft.id=savedID;draft.revision=acknowledgedRevision;draft.requestID=null;drafts.set(savedID,draft);if(submitted.id==='new')adding=false;}
   // This response is the complete subscription projection; render it once.
 }
 }catch(cause){if(!signal.aborted&&identity===account&&generation===epoch){if(cause.status>=400&&cause.status<500)draft.retry=null;else if(draft.retry)draft.retry.uncertain=true;error=cause.message||String(cause);}}
 finally{pending.delete(draft);if(!signal.aborted)render();}
 };
 return {
 update(value,account){
 const next=account?.identity.id||null;if(next!==identity){identity=next;generation++;drafts.clear();adding=false;error='';current=null;}
 const incoming=value?.items?.find(item=>item.id===pluginID)?.settings;
 if(!incoming){current=value;return;}
 if(incoming?.account_id&&incoming.account_id!==identity)return;
 if((incoming?.revision||0)<(settings()?.revision||0))return;current=value;
 for(const sub of subscriptions()){const draft=drafts.get(sub.id);if(!draft||(!draft.dirty&&!pending.has(draft)))drafts.set(sub.id,from(sub));}
 },
 summary(){const values=subscriptions().filter(sub=>sub.enabled);return {text:t('pluginNotifications.subscriptionSummary').replace('{count}',String(values.length)),error:subscriptions().some(s=>s.enabled&&!['ready','baseline','pending'].includes(s.status))};},
 form(){
 const root=n('section','github-notifications');root.dataset.viewKey='notification-form';
 root.append(n('h3','',t('pluginNotifications.title')),n('p','hint',t('pluginNotifications.description')),n('p','hint',t('pluginNotifications.filterHelp')));
 if(!settings()){root.append(n('p','inline-error',t('pluginNotifications.status.plugin_missing')));return root;}
 if(!subscriptions().length)root.append(n('p','hint',t('pluginNotifications.noSubscriptions')));
 const all=[...subscriptions(),...[...drafts.values()].filter(draft=>draft.id!=='new'&&!subscriptions().some(sub=>sub.id===draft.id))];if(adding)all.push({id:'new',revision:0,enabled:true,assistant:current.assistants?.[0]?.id||'',filters:Object.fromEntries((settings().fields||[]).map(f=>[f.key,null]))});
 for(const sub of all){
 const draft=draftFor(sub),section=n('section','page-section');section.dataset.viewKey='subscription-'+sub.id;
 const name=current.assistants?.find(a=>a.id===sub.assistant)?.name||sub.assistant;
 section.append(n('h4','',sub.id==='new'?t('pluginNotifications.newSubscription'):t('pluginNotifications.subscriptionTarget').replace('{name}',name)),n('code','hint',sub.id==='new'?'':sub.id));
 if(sub.id!=='new')section.append(n('p','hint',t('pluginNotifications.revision').replace('{revision}',String(sub.revision))));
 if(sub.status)section.append(n('p',['ready','baseline','pending','disabled'].includes(sub.status)?'hint':'inline-error',t('pluginNotifications.status.'+sub.status)));
 if(sub.account_id)section.append(n('p','hint',t('pluginNotifications.account').replace('{id}',sub.account_id)));
 const enabled=field(t('pluginNotifications.enabled'),draft.enabled,{kind:'checkbox'});enabled.input.onchange=event=>{edit(draft,()=>{draft.enabled=event.currentTarget.checked;});render();};section.append(enabled.row);
 const choices=(current.assistants||[]).map(a=>[a.id,a.name]);if(!choices.some(([id])=>id===draft.assistant))choices.unshift([draft.assistant,t('pluginNotifications.chooseAssistant')]);
 const receiver=field(t('pluginNotifications.assistant'),draft.assistant,{choices});receiver.input.onchange=event=>edit(draft,()=>{draft.assistant=event.currentTarget.value;});section.append(receiver.row);
 for(const descriptor of settings().fields||[]){
 const key=descriptor.key,values=draft.filters[key],mode=field(t(descriptor.label),values===null?'all':'selected',{choices:[['all',t('pluginNotifications.any')],['selected',t('pluginNotifications.selectedValues')]]});
 mode.input.onchange=event=>{edit(draft,()=>{draft.filters[key]=event.currentTarget.value==='all'?null:[];});render();};section.append(mode.row);
 if(values!==null){
 if(descriptor.options){const options=n('fieldset','notification-reasons');options.append(n('legend','form-label',t(descriptor.label)));const list=n('div','notification-reason-options');
 for(const option of descriptor.options){const checkbox=field(option.label?t(option.label):option.value,values.includes(option.value),{kind:'checkbox'});checkbox.row.dataset.viewKey=sub.id+'-'+key+'-'+option.value;checkbox.input.onchange=event=>{edit(draft,()=>{const values=new Set(draft.filters[key]);if(event.currentTarget.checked)values.add(option.value);else values.delete(option.value);draft.filters[key]=[...values];});render();};list.append(checkbox.row);}options.append(list);section.append(options);
 }else{const input=field(t('pluginNotifications.values').replace('{field}',t(descriptor.label)),values.join(', '));input.input.oninput=event=>edit(draft,()=>{draft.filters[key]=split(event.currentTarget.value);});section.append(input.row);}
 }
 }
 section.append(n('p','hint',t('pluginNotifications.reasonHelp')),n('p','hint',t('pluginNotifications.threadHelp')));
 const missing=sub.id!=='new'&&!subscriptions().some(item=>item.id===sub.id);
 if(draft.acceptedUnknown)section.append(n('p','inline-error',t('pluginNotifications.receiptMissing')));
 else if(draft.retry&&!pending.has(draft))section.append(n('p','hint',t('pluginNotifications.retryDraft')));
 if(missing)section.append(n('p','inline-error',t('pluginNotifications.removedDraft')));
 const submit=action(t(draft.acceptedUnknown?'pluginNotifications.awaitingConfirmation':draft.retry?.uncertain?'pluginNotifications.'+(draft.retry.remove?'retryDelete':'retryPrevious'):'pluginNotifications.save'),()=>save(draft));submit.disabled=pending.has(draft)||draft.acceptedUnknown||missing&&!draft.retry||!draft.assistant;section.append(submit);
 section.append(action(t('pluginNotifications.reset'),()=>{if(pending.has(draft))return;drafts.delete(sub.id);if(sub.id==='new')adding=false;error='';render();}));
 if(sub.id!=='new'){const remove=action(t('pluginNotifications.delete'),()=>save(draft,true),{danger:true});remove.disabled=pending.has(draft)||!!draft.retry||missing;section.append(remove);}if(pending.has(draft))section.querySelectorAll('button').forEach(button=>button.disabled=true);
 root.append(section);
 }
 const add=action(t('pluginNotifications.add'),()=>{adding=true;render();});add.disabled=adding||!identity;root.append(add);
 if(error)root.append(n('p','inline-error',t('pluginNotifications.error')),n('pre','notification-diagnostics',error));
 return root;
 }
 };
}
