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
  mark.innerHTML='<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.55" stroke-linecap="round" stroke-linejoin="round"><path d="M11.5 2.5H5a1.5 1.5 0 0 0-1.5 1.5v12A1.5 1.5 0 0 0 5 17.5h10a1.5 1.5 0 0 0 1.5-1.5V7.5z"/><path d="M11.5 2.5v5h5M6.7 11h6.6M6.7 14h4.6"/></svg>';
  return mark;
}

export function taskIdentityShort(value, fallback='') {
  const label=document.createElement('span');label.className='task-identity-short';
  label.textContent=taskIdentities.short(value)||fallback;return label;
}
