import {node} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

// Execution wins over an earlier unread reply. Read receipts remain the sole
// source of the ready state; remounts/reconnects never invent completion events.
export function assistantActivity(item,unread) {
  return !item.offline&&item.executing?'cooking':unread>0?'ready':'';
}

export function assistantActivityIndicator(state,locale) {
  const label=translate(locale,state==='cooking'?'assistant.cooking':'assistant.ready');
  const root=node('span','assistant-activity');root.dataset.state=state;root.dataset.viewKey='activity';root.setAttribute('role','img');root.setAttribute('aria-label',label);root.title=label;
  const pot=node('span','activity-pot'),rig=node('span','activity-pot-rig');pot.setAttribute('aria-hidden','true');
  for(const part of ['steam steam-one','steam steam-two','steam steam-three','handle handle-left','handle handle-right','body','lid'])rig.append(node('span',`activity-pot-${part}`));
  pot.append(rig,node('span','activity-pot-check'));root.append(pot);return root;
}
