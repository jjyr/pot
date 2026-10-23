import {translate} from '../locales/messages.mjs';
import {communicationKey} from '../state/communication.mjs';
import {renderMarkdown} from './markdown.mjs';
import {connectionBrand} from './connection-visuals.mjs';
import {avatar} from './avatar.mjs';
import {icon} from './icons.mjs';
const n=(tag,cls='',text)=>{const node=document.createElement(tag);node.className=cls;if(text!==undefined)node.textContent=text;return node;};

// Pure presentation, shared by Chat and Task tool disclosures. No directory reads
// or transport actions: callers supply their existing full-address identity cache.
export function createCommunication({locale,resolveIdentity,downloadFile,acquireFileImage,onOpenTask,resolveTask=()=>undefined}){
  const summary=n('div','communication-summary'),body=n('div','communication-body'),entries=new Map();
  const t=key=>translate(locale(),'tool.'+key);
  let summarySignature='',summaryProfiles=[];
  function endpoint(address,{label,brand,fallback='messageRecipientUnavailable'}={}){
    const profile=resolveIdentity(address),box=n('span','communication-endpoint'),mark=n('span','communication-mark');
    if(profile?.avatar?.startsWith('data:image/png;base64,'))mark.append(avatar(23,profile.avatar));
    else if(['pot','codex'].includes(profile?.brand||brand))mark.append(connectionBrand(profile?.brand||brand));
    else mark.append(icon(address?.kind==='task'?'tasks':'topology'));
    const name=label||profile?.name||t(address?.kind==='task'?'messageTask':address?.kind==='user'?'messageUser':address?.kind==='assistant'?'messageAssistantUnavailable':fallback);
    box.append(mark,n('span','communication-name',name));
    box.title=[name,address?.node_id,address?.kind,address?.agent_id,profile?.origin?.name].filter(Boolean).join(' · ');
    if(profile?.offline){const state=n('span','communication-offline',t('endpointOffline'));box.append(state);}
    return box;
  }
  function flow(mail,{fromLabel,toLabel,toBrand}={}){
    const graph=n('div','communication-flow'),wire=n('span','communication-wire');
    const known=mail.from?.node_id&&mail.to?.node_id;
    const channel=known?(mail.from.node_id===mail.to.node_id?'channelLocal':'channelLink'):'channelUnknown';
    wire.append(n('span','communication-channel',t(channel)));wire.setAttribute('aria-label',t('messageDirection').replace('{channel}',t(channel)));graph.classList.toggle('is-local',channel==='channelLocal');
    graph.append(endpoint(mail.from,{label:fromLabel,fallback:'messageSenderUnavailable'}),wire,endpoint(mail.to,{label:toLabel,brand:toBrand}));return graph;
  }
  function taskTarget(model){
    const task=model.task,source=task?.agent_link;
    const name=resolveIdentity(model.messages[0]?.to)?.name||(['pot','codex'].includes(source?.kind||task?.executor_icon)?translate(locale(),'agentLinks.source'+((source?.kind||task?.executor_icon)==='pot'?'Pot':'Codex')):null);
    return {label:name||t('messageTask'),named:!!name,brand:source?.kind||task?.executor_icon};
  }
  function update(model,{expanded=false,preparing=false}={}){
    summary.hidden=!model;body.hidden=!model;if(!model)return;
    const profiles=model.messages.map(mail=>[resolveIdentity(mail.from),resolveIdentity(mail.to)]),target=taskTarget(model);
    const signature=JSON.stringify([model.kind,model.messages.map(mail=>[mail.from,mail.to]),model.count,target,locale()]),profileRefs=profiles.flat();
    if(summarySignature!==signature||profileRefs.some((profile,index)=>profile!==summaryProfiles[index])){
      summarySignature=signature;summaryProfiles=profileRefs;summary.replaceChildren();
      const multiple=model.sources.length>1,toKeys=new Set(model.messages.map(mail=>communicationKey(mail.to)));
      const mail=model.messages[0]||{};
      summary.append(flow({...mail,from:multiple?undefined:mail.from,to:toKeys.size>1?undefined:mail.to},{fromLabel:multiple?t('messageSources').replace('{count}',model.sources.length.toLocaleString(locale())):undefined,toLabel:toKeys.size>1?t('messageRecipients'):model.kind==='delegate'?target.label:undefined,toBrand:model.kind==='delegate'?target.brand:undefined}));
    }
    if(!expanded)return;
    for(const entry of entries.values())entry.box.hidden=true;
    for(const [index,mail] of model.messages.entries()){
      let entry=entries.get(index);
      if(!entry){const box=n('section','communication-entry'),relation=n('div'),content=n('div','message-body communication-text'),fallback=n('p','communication-fallback');box.append(relation,content,fallback);entry={box,relation,content,fallback};entries.set(index,entry);body.append(box);}
      entry.box.hidden=false;entry.relation.hidden=model.messages.length===1;
      const relationSignature=JSON.stringify([mail.from,mail.to,locale()]);
      if(!entry.relation.hidden&&(entry.relationSignature!==relationSignature||entry.fromProfile!==profiles[index][0]||entry.toProfile!==profiles[index][1])){entry.relationSignature=relationSignature;[entry.fromProfile,entry.toProfile]=profiles[index];entry.relation.replaceChildren(flow(mail));}
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
