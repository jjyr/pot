import {translate} from '../locales/messages.mjs';
import {outboxIdentityKey} from '../state/chat-outbox-store.mjs';
const node=(tag,className,text)=>{const value=document.createElement(tag);value.className=className;if(text!==undefined)value.textContent=text;return value;};
// Construct only inside the owning message window's prepared render. Event
// handlers change intent and request another measured render, never live layout.
export function outboxControls(value,{outbox,editors,locale,changed,notify}){
  const t=key=>translate(locale(),key),key=outboxIdentityKey(value),editing=editors.has(key),host=node('div','outbox-controls');
  host.setAttribute('role','status');host.append(node('span','outbox-status',t('outbox.'+(value.state==='failed'?value.problem||'rejected':value.state))));
  const action=(label,run)=>{const button=node('button','text-button',t(label));button.type='button';button.onclick=()=>Promise.resolve().then(run).catch(()=>notify(t('outbox.storageFailed')));return button;};
  if(value.state==='failed')host.append(action('retry',()=>outbox.retry(value)));
  if(!value.started&&!value.claim&&['queued','failed'].includes(value.state)){
    if(editing){
      const editor=node('textarea','outbox-editor');editor.value=editors.get(key);editor.setAttribute('aria-label',t('outbox.editLabel'));editor.oninput=()=>editors.set(key,editor.value);
      host.append(editor,action('outbox.save',async()=>{const saved=await outbox.edit(value,editors.get(key));if(saved)editors.delete(key);else notify(t('outbox.changed'));changed();}),action('outbox.discardEdit',()=>{editors.delete(key);changed();}));
    }else host.append(action('outbox.edit',()=>{editors.set(key,value.request.text);changed();}),action('outbox.cancel',async()=>{if(!await outbox.cancel(value))notify(t('outbox.started'));}));
  }
  return host;
}
