import {node as n,action,field} from './primitives.mjs';
import {reconcile} from './reconcile.mjs';
import {translate} from '../locales/messages.mjs';

export function createPluginNotifications({root,request,scope,signal,locale,refresh}) {
 const section=n('section','page-card');root.append(section);
 const t=key=>translate(locale(),key);
 let current=null,draft=null,busy=false,error=false;
 signal.addEventListener('abort',()=>window.removeEventListener('pot:locale',render),{once:true});
 window.addEventListener('pot:locale',render);
 function render(){
 if(signal.aborted)return;
 const projection=n('section','page-card');if(!current){reconcile(section,projection);return;}
 const item=current.items?.find(i=>i.id==='github');if(!item){reconcile(section,projection);return;}
 if(!draft)draft={enabled:item.enabled,assistant:item.assistant,repositories:(item.config.repositories||[]).join(', '),reasons:(item.config.reasons||[]).join(', '),existing:false};
 projection.append(n('h3','',t('pluginNotifications.title')),n('p','hint',t('pluginNotifications.description')));
 const noAssistants=!current.assistants?.length;if(noAssistants)projection.append(n('p','hint',t('pluginNotifications.noAssistants')));
 const status=n('p','hint',t('pluginNotifications.status.'+(!item.enabled?'disabled':item.status)));status.setAttribute('role','status');projection.append(status);
 if(item.queued)projection.append(n('p','hint',t('pluginNotifications.queued').replace('{count}',String(item.queued))));
 if(item.attention)projection.append(n('p','inline-error',t('pluginNotifications.attention')));
 const enabled=field(t('pluginNotifications.enabled'),draft.enabled,{kind:'checkbox'});
 enabled.input.type='checkbox';enabled.input.checked=draft.enabled;enabled.input.onchange=event=>{draft.enabled=event.currentTarget.checked;};
 const target=field(t('pluginNotifications.assistant'),draft.assistant,{choices:(current.assistants||[]).map(a=>[a.id,a.name])});target.input.onchange=event=>{draft.assistant=event.currentTarget.value;};
 const repositories=field(t('pluginNotifications.repositories'),draft.repositories);repositories.input.oninput=event=>{draft.repositories=event.currentTarget.value;};
 const reasons=field(t('pluginNotifications.reasons'),draft.reasons);reasons.input.oninput=event=>{draft.reasons=event.currentTarget.value;};
 const existing=field(t('pluginNotifications.existing'),false,{kind:'checkbox'});existing.input.type='checkbox';existing.input.checked=draft.existing;existing.input.onchange=event=>{draft.existing=event.currentTarget.checked;};
 projection.append(enabled.row,target.row,repositories.row,reasons.row,n('p','hint',t('pluginNotifications.reasonHelp')),existing.row);
 const save=action(t('pluginNotifications.save'),async event=>{
 if(busy)return;busy=true;error=false;event.currentTarget.disabled=true;render();
 const split=value=>value.split(',').map(v=>v.trim()).filter(Boolean);
 try{await request('/api/v1/plugin-notifications/github',{...scope,signal,method:'POST',body:{enabled:draft.enabled,assistant:draft.assistant,revision:item.revision,config:{repositories:split(draft.repositories),reasons:split(draft.reasons),existing:draft.existing?crypto.randomUUID():(item.config.existing||'')}}});if(!signal.aborted){draft=null;await refresh();}}catch{error=true;}finally{busy=false;if(!signal.aborted)render();}
 });save.disabled=busy||noAssistants;projection.append(save);
 if(error){const message=n('p','inline-error',t('pluginNotifications.error'));message.setAttribute('role','alert');projection.append(message);}
 for(const control of projection.querySelectorAll('input,select,button'))control.disabled=busy||(control===save&&noAssistants);
 reconcile(section,projection);
 }
 return value=>{current=value;render();};
}
