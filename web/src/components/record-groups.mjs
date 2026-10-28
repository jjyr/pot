import {assistantAddress,addressKey} from '../state/task-identity.mjs';
import {node as n,action} from './primitives.mjs';
import {avatar} from './avatar.mjs';
import {icon} from './icons.mjs';
import {translate} from '../locales/messages.mjs';

export function recordOwner(item,client) {
  const profile=(client.assistants||[]).find(a=>a.id===item._assistant_id&&a.space_id===item._space_id);
  const address=assistantAddress(profile);
  return {profile,address,key:addressKey(address)||'unknown-owner',space:(client.spaces||[]).find(s=>s.id===item._space_id)};
}
export function groupRecords(items,client) {
  const groups=new Map();
  for(const item of items){const owner=recordOwner(item,client);if(!groups.has(owner.key))groups.set(owner.key,{...owner,items:[]});groups.get(owner.key).items.push(item);}
  return [...groups.values()];
}
export function ownerChoices(client,locale) {
  const choices=new Map();
  for(const profile of client.assistants||[]){const address=assistantAddress(profile),key=addressKey(address);if(!key)continue;const space=client.spaces?.find(s=>s.id===profile.space_id);choices.set(key,[key,`${profile.origin?profile.profile_name||profile.name:profile.name} · ${space?.name||address.node_id} · ${address.node_id}`]);}
  return [['',translate(locale,'tasks.allAssistants')],...choices.values(),['unknown-owner',translate(locale,'tasks.ownerUnknown')]];
}
export function groupHeading(group,locale,expanded=new Map()) {
  const t=key=>translate(locale,key),head=n('header','record-group-heading'),photo=avatar(34,group.profile?.avatar);
  const image=photo.querySelector('img');
  if(image)image.onerror=event=>event.currentTarget.replaceWith(icon('person'));
  else photo.append(icon('person'));
  const info=n('div','record-group-info'),title=n('h2','',group.profile?(group.profile.origin?group.profile.profile_name||group.profile.name:group.profile.name):t('tasks.ownerUnknown'));
  title.id='record-owner-'+encodeURIComponent(group.key);
  const source=n('div','record-group-source',group.address?`${group.space?.name||group.address.node_id} · ${t(group.profile.offline?'tasks.sourceOffline':'tasks.sourceOnline')}`:t('tasks.ownerMissing'));
  if(group.address){const identity=n('details','record-owner-identity'),summary=n('summary');summary.title=t('tasks.ownerIdentity');summary.setAttribute('aria-label',t('tasks.ownerIdentityFor').replace('{name}',title.textContent));identity.dataset.cacheKey='record-owner:'+group.key;identity.open=expanded.get(group.key)||false;identity.addEventListener('toggle',()=>expanded.set(group.key,identity.open));summary.append(title,source);identity.append(summary,n('code','',group.key));info.append(identity);}else info.append(title,source);
  head.append(photo,info,n('span','record-group-count',t('tasks.groupCount').replace('{count}',group.items.length.toLocaleString(locale))));return head;
}
// The same component and wording are used above and below both global lists.
export function createRecordPagination({locale,label,move}) {
  const root=n('nav','record-pagination');root.setAttribute('aria-label',label);
  const summary=n('p','record-page-summary');summary.setAttribute('role','status');summary.setAttribute('aria-live','polite');
  const buttons=n('div','record-page-buttons'),previous=action(translate(locale(),'tasks.previous'),()=>move(-1)),next=action(translate(locale(),'tasks.next'),()=>move(1));
  previous.dataset.pageDirection='previous';next.dataset.pageDirection='next';buttons.append(previous,next);root.append(summary,buttons);
  return {root,previous,next,update({offset=0,count=0,total=0,nextOffset=null,loading=false,partial=false}) {
    root.setAttribute('aria-busy',String(loading));
    const key=loading?'tasks.paginationPage':partial?'tasks.paginationPartial':'tasks.paginationSummary';
    if(!loading||!summary.textContent)summary.textContent=translate(locale(),key).replace('{total}',total.toLocaleString(locale())).replace('{page}',(Math.floor(offset/20)+1).toLocaleString(locale())).replace('{count}',count.toLocaleString(locale()));
    previous.disabled=loading||!offset;next.disabled=loading||nextOffset==null;
  }};
}
