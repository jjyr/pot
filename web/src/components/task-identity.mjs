import {icon} from './icons.mjs';
import {taskAddress, taskColorIndex, taskIdentities} from '../state/task-identity.mjs';

// One display mark for confirmed catalogue Tasks and communication addresses.
// Raw transcript addresses retain a short label, but only a TaskInfo mapping colors them.
export function taskIdentityMark(value) {
  const address=value?.kind==='task'?value:taskAddress(value);
  const confirmed=taskAddress(value)?value:taskIdentities.resolve(address);
  const mark=document.createElement('span');mark.className='task-identity-mark';
  mark.setAttribute('aria-hidden','true');
  const color=confirmed?taskColorIndex(confirmed):-1;
  if(color>=0)mark.dataset.taskColor=String(color);
  mark.append(icon('tasks'));
  return mark;
}

export function taskIdentityShort(value, fallback='') {
  const label=document.createElement('span');label.className='task-identity-short';
  label.textContent=taskIdentities.short(value)||fallback;return label;
}

