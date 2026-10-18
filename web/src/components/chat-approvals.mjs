import {node as n} from './primitives.mjs';

// A transient UI projection, deliberately separate from conversation messages.
export function createChatApprovals({host,locale,visible}) {
  host.setAttribute('aria-live','off');
  const notice=n('p','approval-notice'),cards=n('div','approval-list'),announcer=n('span','visually-hidden');
  announcer.setAttribute('role','status');announcer.setAttribute('aria-live','polite');announcer.setAttribute('aria-atomic','true');
  host.append(notice,cards,announcer);host.hidden=true;
  const observed=new Map();
  function update(context,entries) {
    host.hidden=!entries.length;
    host.setAttribute('aria-label',locale()==='zh-CN'?'待授权操作':'Pending approvals');
    const text=locale()==='zh-CN'?`有 ${entries.length} 项操作等待你的授权`:`${entries.length} operation(s) are waiting for your approval`;
    if(notice.textContent!==text)notice.textContent=text;
    const nodes=entries.map(entry=>entry.element);
    for(const node of [...cards.childNodes])if(!nodes.includes(node))node.remove();
    nodes.forEach((node,index)=>{if(cards.childNodes[index]!==node)cards.insertBefore(node,cards.childNodes[index]||null);});
    const seen=observed.get(context)||new Set(),fresh=entries.filter(entry=>!seen.has(entry.id));
    if(fresh.length&&visible()&&host.isConnected) {
      announcer.textContent=text;
      host.dataset.noticeVersion=String(Number(host.dataset.noticeVersion||0)+1);
      if(!matchMedia('(prefers-reduced-motion: reduce)').matches)host.animate([{opacity:.45},{opacity:1}],{duration:450});
      fresh.forEach(entry=>seen.add(entry.id));observed.set(context,seen);
      if(observed.size>100)observed.delete(observed.keys().next().value);
    }
    if(!entries.length)announcer.textContent='';
  }
  return {update};
}
