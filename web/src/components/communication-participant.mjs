import {avatar} from './avatar.mjs';
import {icon} from './icons.mjs';
import {taskIdentityMark,taskIdentityShort} from './task-identity.mjs';
import {addressKey} from '../state/task-identity.mjs';
import {translate} from '../locales/messages.mjs';

// The full address owns identity. No directory hash, name-based avatar or brand
// fallback; taskIdentityMark alone decides whether a catalogue color is known.
export function communicationParticipant(address,{resolveIdentity=()=>undefined,locale=()=> 'en'}={}){
  const profile=resolveIdentity(address),task=address?.kind==='task';
  const label=profile?.name||translate(locale(),`tool.${task?'messageTask':address?.kind==='user'?'messageUser':address?.kind==='assistant'?'messageAssistantUnavailable':'messageRecipientUnavailable'}`);
  const box=document.createElement('span');box.className='work-participant';
  const mark=task?taskIdentityMark(address):avatar(22,profile?.avatar);
  if(!task&&!mark.childElementCount)mark.append(icon('person'));
  box.append(mark);
  const name=task?taskIdentityShort(address,translate(locale(),'tool.messageTaskIDMissing')):Object.assign(document.createElement('span'),{className:'work-participant-name',textContent:label});
  box.append(name);box.dataset.identity=addressKey(address)||'';
  box.title=[label,task?name.textContent:'',address?.node_id,address?.kind,address?.agent_id].filter(Boolean).join(' · ');
  box.setAttribute('role','img');box.setAttribute('aria-label',box.title);
  return box;
}
