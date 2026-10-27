import {currentPreparedRender,withPreparedRender,registerPreparedRoot,preparedDOM,preparedText} from './prepared-render.mjs';
import {moveConversationNode as place} from './conversation-dom.mjs';
import {createMessageWindow} from './message-window.mjs';
import {createWorkRecordProjection,workRecordParents} from '../state/work-records.mjs';
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
  let owners=new Map(),projectedOwners=new Map(),current=[],source,actorSignature;
  const groupMemory=message=>memory.get(currentScope+message.viewKey)||message.members.map(member=>memory.get(currentScope+'work:'+member.viewKey)).find(Boolean);
  const forget=(key,owner)=>{
    const entry=records.get(key);if(!entry||entry.owner!==owner)return;
    if(owners.has(key)&&owners.get(key)!==owner)return;
    options.dispose(entry.record);records.delete(key);
  };
  const renderMember=(message,owner,existing,preparedMembers)=>{
    const environment=currentPreparedRender(),preparing=environment?.context.phase==='prepare',entry=records.get(message.viewKey);
    if(preparing)registerPreparedRoot(environment.context,entry?.record.row);
    let record;
    if(!preparing&&!entry&&preparedMembers?.has(message.viewKey)){
      record=preparedMembers.get(message.viewKey);record.commit?.();record.preparing=false;
    }else{
      const build=()=>options.render({...message,workRecord:true},preparing?existing:entry?.record||existing);
      record=environment?withPreparedRender(environment.context,message.id,build):build();
    }
    record.row.dataset.windowKey=message.viewKey;
    if(preparing){
      record.preparing=true;
      if(!record.registerWorkRecord){const commit=record.commit;record.commit=()=>{commit?.();record.preparing=false;records.set(message.viewKey,{record,owner});return record;};record.registerWorkRecord=true;}
    }else{record.preparing=false;records.set(message.viewKey,{record,owner});}
    return record;
  };
  function render(message,existing){
    const environment=currentPreparedRender(),preparing=environment?.context.phase==='prepare';
    if(!message.members)return renderMember(message,message.viewKey,existing);
    let group=existing;
    if(!group){
      const row=node('article','message role-tool work-record-group'),details=node('details','work-records'),summary=node('summary','work-record-summary'),title=node('span','work-record-title'),counts=node('span','work-record-counts'),participants=node('span','work-record-participants'),failure=node('span','work-record-failure'),activity=node('div','work-record-activity'),body=node('div','work-record-body');
      const people=node('div','work-record-people'),items=node('div','work-record-items');body.append(people,items);
      summary.append(title,counts,participants,failure);details.append(summary,body);row.append(details,activity);
      group={row,details,title,counts,participants,failure,activity,body:items,people,members:[],preparedMembers:new Map(),preparing};
      group.commit=()=>render(message,group);
      const memoryKey=currentScope+message.viewKey,saved=groupMemory(message);
      if(saved)memory.set(memoryKey,saved);
      details.open=saved?.open??message.members.some(member=>records.get(member.viewKey)?.record.tool?.element.open);
      // Native toggle also fires for renderer assignments in hidden candidates.
      // Only the summary action and explicit native-member navigation own saved
      // disclosure intent; preparation must never write it back asynchronously.
      details.addEventListener('toggle',()=>{if(!group.preparing&&row.isConnected&&options.host.contains(row))options.requestRender();});
      summary.addEventListener('click',event=>{
        if(event.defaultPrevented)return;
        window.engage(event);event.preventDefault();window.pause();
        memory.set(memoryKey,{open:!(memory.get(memoryKey)?.open??details.open)});
        row.dispatchEvent(new CustomEvent('pot:prepare-resize',{bubbles:true}));options.requestRender();
      });
    }
    // Connect the destination before moving live members. moveBefore preserves
    // selection, focus and embedded resource state across the new wrapper.
    if(!preparing&&!group.row.isConnected&&options.host.isConnected)options.host.append(group.row);
    group.members=message.members;group.row.dataset.messageId=message.id;group.row.dataset.scrollAnchor=`message:${message.viewKey}`;
    const remembered=groupMemory(message);if(remembered)group.details.open=remembered.open;
    group.row.dataset.anchorMessageId=message.sourceAnchor?.id||'';group.row.dataset.sequence=message.sourceAnchor?.sequence||'';
    group.row.dataset.sequenceBefore=message.sourceAnchor?.before||'';group.row.dataset.sequenceAfter=message.sourceAnchor?.after||'';
    const destination=window.destination?.id,nativeReading=message.members.some(member=>window.position?.anchors?.[0]?.key===`message:${member.viewKey}`);
    if(window.revealing&&(destination&&message.members.some(member=>member.id===destination||window.visibilityPath.has(member.id))||nativeReading&&window.visibilityPath.has(message.id))){group.details.open=true;if(!preparing)memory.set(currentScope+message.viewKey,{open:true});}
    const work=message.work,format=value=>value.toLocaleString(locale()),t=key=>translate(locale(),'work.'+key);
    preparedText(group.title,'work-record-window:group.title',t('title'));
    preparedText(group.counts,'work-record-window:group.counts',(work.tools?[t('tools').replace('{count}',format(work.tools))]:[]).concat(work.messages?[t('messages').replace('{count}',format(work.messages))]:[]).join(' · '));
    group.counts.title=group.counts.textContent;
    group.failure.hidden=!work.failures;preparedText(group.failure,'work-record-window:group.failure',work.failures?t('failures').replace('{count}',format(work.failures)):'');
    const participantRevision=JSON.stringify([work.participants,locale(),work.participants.map(address=>resolveIdentity(address))]);
    preparedDOM(group.participants,'work-participants',participantRevision,target=>{
      const marks=node('span','work-record-participants');
      work.participants.forEach((address,index)=>{const mark=communicationParticipant(address,{resolveIdentity,locale});mark.dataset.participantIndex=String(index);marks.append(mark);});
      if(work.participants.length>2){const more=node('span','work-record-more',`+${format(work.participants.length-2)}`);more.title=t('participants').replace('{count}',format(work.participants.length));more.setAttribute('aria-label',more.title);marks.append(more);}
      reconcile(target,marks);
    });group.participants.hidden=!work.participants.length;
    preparedDOM(group.people,'work-people',participantRevision,target=>{
      const people=node('div','work-record-people');for(const address of work.participants)people.append(communicationParticipant(address,{resolveIdentity,locale}));reconcile(target,people);
    });group.people.hidden=work.participants.length<2;
    group.activity.hidden=!work.active;
    preparedText(group.activity,'work-record-window:group.activity',work.active?t('activity').replace('{name}',work.active.name==='compaction'?translate(locale(),'compact.title'):translate(locale(),'tool.'+presentationLabel(work.active))):'');
    let index=0;
    for(const member of message.members){
      // Each member keeps its own disclosure. Already mounted bodies stay alive.
      if(!group.details.open&&!records.has(member.viewKey)&&!group.preparedMembers.has(member.viewKey))continue;
      const record=renderMember(member,message.viewKey,preparing?group.preparedMembers.get(member.viewKey):undefined,group.preparedMembers);
      if(preparing)group.preparedMembers.set(member.viewKey,record);
      if(group.body.children[index]!==record.row)place(group.body,record.row,group.body.children[index]||null);
      index++;
    }
    while(group.body.children.length>index)group.body.lastElementChild.remove();
    group.preparing=preparing;return group;
  }
  function dispose(record){
    if(record.preparing){for(const member of record.preparedMembers?.values()||[])options.dispose(member);if(!record.members)options.dispose(record);else record.row.remove();return;}
    if(record.members){const moving=record.members.some(member=>owners.has(member.viewKey)&&owners.get(member.viewKey)!==record.row.dataset.windowKey);for(const member of record.members)forget(member.viewKey,record.row.dataset.windowKey);if(!moving)record.row.remove();}
    else forget(record.row.dataset.windowKey,record.row.dataset.windowKey);
  }
  const window=createMessageWindow({...options,render,dispose});
  function layout(messages,settings={}){
    const nextScope=typeof scope==='function'?scope():scope;
    if(currentScope!==undefined&&currentScope!==nextScope){owners.clear();projectedOwners.clear();window.setMessages([]);for(const {record}of records.values())options.dispose(record);records.clear();project=createWorkRecordProjection();source=undefined;}
    currentScope=nextScope;
    const currentActor=actor(),nextActorSignature=JSON.stringify(currentActor);
    if(source!==messages||actorSignature!==nextActorSignature||blocked){
      const addresses=messages.flatMap(message=>communication(message.tool,currentActor)?.messages.flatMap(mail=>[mail.from,mail.to])||[]);
      taskIdentities.register(addresses);
      const selection=getSelection(),selected=selection&&!selection.isCollapsed?selection.anchorNode?.parentElement?.closest('article.message'):null;
      const focus=document.activeElement?.closest('article.message');
      const anchor=(options.host.contains(focus)?focus:options.host.contains(selected)?selected:null)?.dataset.messageId||window.anchor?.id;
      current=project(messages,{actor:currentActor,blocked,anchor});source=messages;actorSignature=nextActorSignature;
      projectedOwners=new Map(current.flatMap(message=>(message.members||[message]).map(member=>[member.viewKey,message.viewKey])));
    }
    const committedOwners=projectedOwners;
    window.setMessages(current,{...settings,parents:workRecordParents(current,settings.parents),
      beforeCommit:()=>{owners=committedOwners;settings.beforeCommit?.();},
      afterCommit:()=>{for(const [key,entry]of records)if(!committedOwners.has(key)){options.dispose(entry.record);records.delete(key);}settings.afterCommit?.();},
    });
  }
  return Object.assign(Object.create(window),{setMessages:layout,close(){owners.clear();window.close();for(const {record}of records.values())options.dispose(record);records.clear();}});
}
