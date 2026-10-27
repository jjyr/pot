import {moveConversationNode as place} from './conversation-dom.mjs';
import {createMessageWindow} from './message-window.mjs';
import {createWorkRecordProjection} from '../state/work-records.mjs';
import {presentationLabel} from '../state/tool-presentation.mjs';
import {communicationParticipant} from './communication-participant.mjs';
import {reconcile} from './reconcile.mjs';
import {communication} from '../state/communication.mjs';
import {taskIdentities} from '../state/task-identity.mjs';
import {translate} from '../locales/messages.mjs';

const node=(tag,cls='',text)=>{const value=document.createElement(tag);value.className=cls;if(text!==undefined)value.textContent=text;return value;};
// The virtualizer still owns scrolling. This adapter only projects adjacent
// logical records and retains the original render records across regrouping.
export function createWorkRecordWindow({locale=()=> 'en',actor=()=>undefined,resolveIdentity=()=>undefined,blocked,
  memory=new Map(),scope='',...options}) {
  let project=createWorkRecordProjection(),currentScope;
  const records=new Map();
  let owners=new Map(),current=[],source,actorSignature;
  const forget=(key,owner)=>{
    const entry=records.get(key);if(!entry||entry.owner!==owner)return;
    if(owners.has(key)&&owners.get(key)!==owner)return;
    options.dispose(entry.record);records.delete(key);
  };
  const renderMember=(message,owner)=>{
    const entry=records.get(message.viewKey),record=options.render({...message,workRecord:true},entry?.record);
    record.row.dataset.windowKey=message.viewKey;
    records.set(message.viewKey,{record,owner});return record;
  };
  function render(message,existing){
    if(!message.members)return renderMember(message,message.viewKey);
    let group=existing;
    if(!group){
      const row=node('article','message role-tool work-record-group'),details=node('details','work-records'),summary=node('summary','work-record-summary'),title=node('span','work-record-title'),counts=node('span','work-record-counts'),participants=node('span','work-record-participants'),failure=node('span','work-record-failure'),activity=node('div','work-record-activity'),body=node('div','work-record-body');
      const people=node('div','work-record-people'),items=node('div','work-record-items');body.append(people,items);
      summary.append(title,counts,participants,failure);details.append(summary,body);row.append(details,activity);
      group={row,details,title,counts,participants,failure,activity,body:items,people,members:[]};
      const memoryKey=currentScope+message.viewKey,saved=memory.get(memoryKey);
      details.open=saved?.open??message.members.some(member=>records.get(member.viewKey)?.record.tool?.element.open);
      details.addEventListener('toggle',()=>{memory.set(memoryKey,{open:details.open});options.requestRender();});
      summary.addEventListener('click',event=>{
        if(event.defaultPrevented)return;
        window.engage(event);event.preventDefault();window.pause();
        details.open=!details.open;memory.set(memoryKey,{open:details.open});options.requestRender();
      });
    }
    // Connect the destination before moving live members. moveBefore preserves
    // selection, focus and embedded resource state across the new wrapper.
    if(!group.row.isConnected&&options.host.isConnected)options.host.append(group.row);
    group.members=message.members;group.row.dataset.messageId=message.id;group.row.dataset.scrollAnchor=`message:${message.viewKey}`;
    if(window.revealing&&message.members.some(member=>member.id===window.destination?.id||window.visibilityPath.has(member.id)))group.details.open=true;
    const work=message.work,format=value=>value.toLocaleString(locale()),t=key=>translate(locale(),'work.'+key);
    group.title.textContent=t('title');
    group.counts.textContent=(work.tools?[t('tools').replace('{count}',format(work.tools))]:[]).concat(work.messages?[t('messages').replace('{count}',format(work.messages))]:[]).join(' · ');
    group.counts.title=group.counts.textContent;
    group.failure.hidden=!work.failures;group.failure.textContent=work.failures?t('failures').replace('{count}',format(work.failures)):'';
    const marks=node('span','work-record-participants');
    work.participants.forEach((address,index)=>{const mark=communicationParticipant(address,{resolveIdentity,locale});mark.dataset.participantIndex=String(index);marks.append(mark);});
    // Keep the first two counterparts at every width. Further objects stay
    // available in this group, with an overflow count rather than a long list.
    if(work.participants.length>2){const more=node('span','work-record-more',`+${format(work.participants.length-2)}`);more.title=t('participants').replace('{count}',format(work.participants.length));more.setAttribute('aria-label',more.title);marks.append(more);}
    reconcile(group.participants,marks);group.participants.hidden=!work.participants.length;
    const people=node('div','work-record-people');for(const address of work.participants)people.append(communicationParticipant(address,{resolveIdentity,locale}));
    reconcile(group.people,people);group.people.hidden=work.participants.length<2;
    group.activity.hidden=!work.active;
    group.activity.textContent=work.active?t('activity').replace('{name}',work.active.name==='compaction'?translate(locale(),'compact.title'):translate(locale(),'tool.'+presentationLabel(work.active))):'';
    let index=0;
    for(const member of message.members){
      // Each member keeps its own disclosure. Already mounted bodies stay alive.
      if(!group.details.open&&!records.has(member.viewKey))continue;
      const record=renderMember(member,message.viewKey);
      if(group.body.children[index]!==record.row)place(group.body,record.row,group.body.children[index]||null);
      index++;
    }
    while(group.body.children.length>index)group.body.lastElementChild.remove();
    return group;
  }
  function dispose(record){
    if(record.members){const moving=record.members.some(member=>owners.has(member.viewKey)&&owners.get(member.viewKey)!==record.row.dataset.windowKey);for(const member of record.members)forget(member.viewKey,record.row.dataset.windowKey);if(!moving)record.row.remove();}
    else forget(record.row.dataset.windowKey,record.row.dataset.windowKey);
  }
  const window=createMessageWindow({...options,render,dispose});
  function layout(messages,settings={}){
    const nextScope=typeof scope==='function'?scope():scope;
    if(currentScope!==undefined&&currentScope!==nextScope){owners.clear();window.setMessages([]);for(const {record}of records.values())options.dispose(record);records.clear();project=createWorkRecordProjection();source=undefined;}
    currentScope=nextScope;
    const currentActor=actor(),nextActorSignature=JSON.stringify(currentActor);
    if(source!==messages||actorSignature!==nextActorSignature||blocked){
      const addresses=messages.flatMap(message=>communication(message.tool,currentActor)?.messages.flatMap(mail=>[mail.from,mail.to])||[]);
      taskIdentities.register(addresses);
      const selection=getSelection(),selected=selection&&!selection.isCollapsed?selection.anchorNode?.parentElement?.closest('article.message'):null;
      const focus=document.activeElement?.closest('article.message');
      const anchor=(options.host.contains(focus)?focus:options.host.contains(selected)?selected:null)?.dataset.messageId||window.anchor?.id;
      current=project(messages,{actor:currentActor,blocked,anchor});source=messages;actorSignature=nextActorSignature;
      owners=new Map(current.flatMap(message=>(message.members||[message]).map(member=>[member.viewKey,message.viewKey])));
    }
    window.setMessages(current,settings);
    for(const [key,entry]of records)if(!owners.has(key)){options.dispose(entry.record);records.delete(key);}
  }
  return Object.assign(Object.create(window),{setMessages:layout,close(){owners.clear();window.close();for(const {record}of records.values())options.dispose(record);records.clear();}});
}
