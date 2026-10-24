import {translate} from '../locales/messages.mjs';
import {communicationKey} from '../state/communication.mjs';
import {renderMarkdown} from './markdown.mjs';
import {connectionBrand} from './connection-visuals.mjs';
import {avatar} from './avatar.mjs';
import {icon} from './icons.mjs';
import {taskIdentities} from '../state/task-identity.mjs';
import {taskIdentityMark,taskIdentityShort} from './task-identity.mjs';
import {reconcile} from './reconcile.mjs';
const n=(tag,cls='',text)=>{const node=document.createElement(tag);node.className=cls;if(text!==undefined)node.textContent=text;return node;};
const project=(target,children)=>{const next=target.cloneNode(false);next.append(...children);reconcile(target,next);};

// Pure presentation, shared by Chat and Task tool disclosures. No directory reads
// or transport actions: callers supply their existing full-address identity cache.
export function createCommunication({locale,resolveIdentity,downloadFile,acquireFileImage,onOpenTask,resolveTask=()=>undefined}){
  const summary=n('div','communication-summary'),body=n('div','communication-body'),entries=new Map();
  const t=key=>translate(locale(),'tool.'+key);
  let summarySignature='',summaryProfiles=[];
  const displayName=(address,profile,fallback='messageRecipientUnavailable')=>profile?.name||t(address?.kind==='task'?'messageTask':address?.kind==='user'?'messageUser':address?.kind==='assistant'?'messageAssistantUnavailable':fallback);
  function endpoint(address,{label,brand,fallback='messageRecipientUnavailable'}={}){
    const profile=resolveIdentity(address),task=address?.kind==='task'?taskIdentities.resolve(address):undefined;
    const box=n(task&&onOpenTask?'button':'span','communication-endpoint'),mark=address?.kind==='task'?taskIdentityMark(address):n('span','communication-mark');
    if(address?.kind==='task'){
      mark.classList.add('communication-mark');
    }
    else if(profile?.avatar?.startsWith('data:image/png;base64,'))mark.append(avatar(23,profile.avatar));
    else if(['pot','codex'].includes(profile?.brand||brand))mark.append(connectionBrand(profile?.brand||brand));
    else mark.append(icon(address?.kind==='task'?'tasks':'topology'));
    const name=address?.kind==='task'?displayName(address,profile):label||displayName(address,profile,fallback);
    box.append(mark,n('span','communication-name',name));
    if(address?.kind==='task'){box.classList.add('communication-task-endpoint');const short=taskIdentityShort(address,t('messageTaskIDMissing'));short.classList.add('communication-short');box.append(short);}
    box.title=[name,profile?.origin?.name,...(address?.kind==='task'?[address.node_id,address.agent_id]:[])].filter(Boolean).join(' · ');
    if(task&&onOpenTask){
      box.type='button';box.setAttribute('aria-label',t('viewMessageTask').replace('{title}',name));
      box.onclick=event=>{event.preventDefault();event.stopPropagation();onOpenTask(taskIdentities.resolve(address)||task);};
    }
    if(profile?.offline){const state=n('span','communication-offline',t('endpointOffline'));box.append(state);}
    return box;
  }
  function flow(mail,{fromLabel,toLabel,toBrand}={}){
    const graph=n('div','communication-flow'),wire=n('span','communication-wire');
    wire.setAttribute('aria-hidden','true');
    graph.append(endpoint(mail.from,{label:fromLabel,fallback:'messageSenderUnavailable'}),wire,endpoint(mail.to,{label:toLabel,brand:toBrand}));return graph;
  }
  function taskTarget(model){
    const task=model.task,source=task?.agent_link;
    const name=resolveIdentity(model.messages[0]?.to)?.name||(['pot','codex'].includes(source?.kind||task?.executor_icon)?translate(locale(),'agentLinks.source'+((source?.kind||task?.executor_icon)==='pot'?'Pot':'Codex')):null);
    return {label:name||t('messageTask'),named:!!name,brand:source?.kind||task?.executor_icon};
  }
  function update(model,{expanded=false,preparing=false,summaryOnly=false}={}){
    summary.hidden=!model||expanded;body.hidden=!model;if(!model)return;
    const profiles=model.messages.map(mail=>[resolveIdentity(mail.from),resolveIdentity(mail.to)]),target=taskTarget(model);
    const signature=JSON.stringify([model.kind,model.messages.map(mail=>[mail.from,mail.to,...[mail.from,mail.to].map(address=>address?.kind==='task'?taskIdentities.short(address):'')]),model.count,target,locale()]),profileRefs=profiles.flat();
    if(summarySignature!==signature||profileRefs.some((profile,index)=>profile!==summaryProfiles[index])){
      summarySignature=signature;summaryProfiles=profileRefs;const flows=[];
      const multiple=model.sources.length>1,toKeys=new Set(model.messages.map(mail=>communicationKey(mail.to)));
      const mail=model.messages[0]||{};
      if(model.messages.some(mail=>[mail.from,mail.to].some(address=>address?.kind==='task'))){
        const seen=new Set();for(const mail of model.messages){const key=JSON.stringify([mail.from,mail.to]);if(!seen.has(key)){flows.push(flow(mail));seen.add(key);}}
      }else flows.push(flow({...mail,from:multiple?undefined:mail.from,to:toKeys.size>1?undefined:mail.to},{fromLabel:multiple?t('messageSources').replace('{count}',model.sources.length.toLocaleString(locale())):undefined,toLabel:toKeys.size>1?t('messageRecipients'):model.kind==='delegate'?target.label:undefined,toBrand:model.kind==='delegate'?target.brand:undefined}));
      project(summary,flows);
    }
    if(!expanded||summaryOnly)return;
    for(const entry of entries.values())entry.box.hidden=true;
    for(const [index,mail] of model.messages.entries()){
      let entry=entries.get(index);
      if(!entry){const box=n('section','communication-entry'),relation=n('div','communication-identity'),content=n('div','message-body communication-text'),fallback=n('p','communication-fallback');box.append(relation,content,fallback);entry={box,relation,content,fallback};entries.set(index,entry);body.append(box);}
      entry.box.hidden=false;
      const relationSignature=JSON.stringify([mail.from,mail.to,...[mail.from,mail.to].map(address=>address?.kind==='task'?taskIdentities.short(address):''),locale()]);
      if(entry.relationSignature!==relationSignature||entry.fromProfile!==profiles[index][0]||entry.toProfile!==profiles[index][1]){
        entry.relationSignature=relationSignature;[entry.fromProfile,entry.toProfile]=profiles[index];
        const context=n('span','communication-recipient',t('messageAddressedTo').replace('{name}',displayName(mail.to,entry.toProfile)));
        const relation=[...([mail.from,mail.to].some(address=>address?.kind==='task')?[flow(mail)]:[endpoint(mail.from,{fallback:'messageSenderUnavailable'}),context])];
        const source=entry.fromProfile?.origin?.name;
        if(source&&mail.from?.node_id!==mail.to?.node_id)relation.push(n('span','communication-origin',t('messageFromSource').replace('{source}',source)));
        project(entry.relation,relation);
      }
      const text=mail.text;
      entry.content.hidden=text===null;
      if(entry.text!==text){entry.text=text;entry.content.replaceChildren(...(text===null?[]:[renderMarkdown(text,{downloadFile,acquireFileImage})]));}
      entry.fallback.hidden=text!==null;entry.fallback.textContent=t(preparing?'messagePreparing':'messageUnavailable');
    }
    if(!model.messages.length){let empty=entries.get('empty');if(!empty){empty={box:n('p','communication-fallback')};entries.set('empty',empty);body.append(empty.box);}empty.box.hidden=false;empty.box.textContent=t('messageUnavailable');}
    let taskEntry=entries.get('task');
    if(model.task&&onOpenTask){
      if(!taskEntry){taskEntry={box:n('button','communication-task tool-action')};taskEntry.box.type='button';entries.set('task',taskEntry);body.append(taskEntry.box);}
      const current=resolveTask(model.task.id);
      taskEntry.box.hidden=false;taskEntry.box.textContent=t('viewMessageTask').replace('{title}',current?.title||model.task.title||model.task.id);
      // Only the real Task detail owns live phase, approvals and completion.
      taskEntry.box.onclick=()=>onOpenTask(current||model.task);
    }
  }
  return {summary,body,update,taskTarget};
}
