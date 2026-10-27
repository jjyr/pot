import {node as n} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

// A transient UI projection, deliberately separate from conversation messages.
export function createChatApprovals({host,locale,visible,deferred=false}) {
  host.setAttribute('aria-live','off');
  const notice=n('p','approval-notice'),cards=n('div','approval-list'),announcer=n('span','visually-hidden');
  announcer.setAttribute('role','status');announcer.setAttribute('aria-live','polite');announcer.setAttribute('aria-atomic','true');
  host.append(notice,cards,announcer);host.hidden=true;
  const observed=new Map();
  let pending={context:'',entries:[]},generation=0;
  const revision=value=>JSON.stringify([value.context,locale(),value.entries.map(entry=>[entry.id,entry.revision])]);
  function update(context,entries) {
    const next={context,entries};if(revision(next)!==revision(pending))generation++;pending=next;
    if(!deferred)commit(context,entries);
  }
  function commit(context,entries) {
    host.hidden=!entries.length;
    host.setAttribute('aria-label',translate(locale(),'tool.pendingApprovals'));
    const text=translate(locale(),'tool.pendingApprovalCount').replace('{count}',String(entries.length));
    if(notice.textContent!==text)notice.textContent=text;
    const nodes=entries.map(entry=>entry.element);
    for(const node of [...cards.childNodes])if(!nodes.includes(node))node.remove();
    nodes.forEach((node,index)=>{if(cards.childNodes[index]!==node)cards.insertBefore(node,cards.childNodes[index]||null);});
    const seen=observed.get(context)||new Set(),fresh=entries.filter(entry=>!seen.has(entry.id));
    if(announcer.textContent&&entries.length)announcer.textContent=text;
    if(fresh.length&&visible()&&host.isConnected) {
      announcer.textContent=text;
      host.dataset.noticeVersion=String(Number(host.dataset.noticeVersion||0)+1);
      if(!matchMedia('(prefers-reduced-motion: reduce)').matches)host.animate([{opacity:.45},{opacity:1}],{duration:450});
      fresh.forEach(entry=>seen.add(entry.id));observed.set(context,seen);
      if(observed.size>100)observed.delete(observed.keys().next().value);
    }
    if(!entries.length)announcer.textContent='';
  }
  function region(){
    const {context,entries}=pending,stamp=revision(pending),epoch=generation;
    return {key:'chat-approvals',host,revision:JSON.stringify([epoch,stamp]),current:()=>generation===epoch&&revision(pending)===stamp,latest:region,render:target=>{
      if(target===host){commit(context,entries.map(entry=>({id:entry.id,element:entry.commit()})));return;}
      target.hidden=!entries.length;target.setAttribute('aria-label',translate(locale(),'tool.pendingApprovals'));
      const label=n('p','approval-notice',translate(locale(),'tool.pendingApprovalCount').replace('{count}',String(entries.length))),list=n('div','approval-list');
      for(const entry of entries)list.append(entry.prepare());target.replaceChildren(label,list,n('span','visually-hidden'));
    }};
  }
  return {update,region};
}
