import {moveConversationNode as place} from './conversation-dom.mjs';
import {createMessageWindow} from './message-window.mjs';
import {createCommunicationProjection,communicationCounts} from '../state/communication-stack.mjs';
import {createCommunication} from './communication.mjs';
import {communication} from '../state/communication.mjs';
import {taskIdentities} from '../state/task-identity.mjs';
import {translate} from '../locales/messages.mjs';

const node=(tag,cls='')=>{const value=document.createElement(tag);value.className=cls;return value;};
// The virtualizer still owns scrolling. This adapter only projects adjacent
// logical records and retains the original render records across regrouping.
export function createCommunicationWindow({locale=()=> 'en',actor=()=>undefined,resolveIdentity=()=>undefined,onOpenTask,blocked,
  memory=new Map(),scope='',...options}) {
  let project=createCommunicationProjection(),currentScope;
  const records=new Map();
  let owners=new Map(),current=[],source,actorSignature;
  const forget=(key,owner)=>{
    const entry=records.get(key);if(!entry||entry.owner!==owner)return;
    if(owners.has(key)&&owners.get(key)!==owner)return;
    options.dispose(entry.record);records.delete(key);
  };
  const renderMember=(message,owner,stacked)=>{
    const entry=records.get(message.viewKey),record=options.render({...message,communicationStacked:stacked},entry?.record);
    record.row.dataset.windowKey=message.viewKey;
    records.set(message.viewKey,{record,owner});return record;
  };
  function render(message,existing){
    if(!message.members)return renderMember(message,message.viewKey,false);
    let group=existing;
    if(!group){
      const row=node('article','message role-tool communication-group'),details=node('details','communication-stack'),summary=node('summary','communication-stack-summary'),title=node('span','communication-stack-title'),routes=node('div','communication-stack-routes'),body=node('div','communication-stack-body');
      summary.append(title,routes);details.append(summary,body);row.append(details);
      group={row,details,title,routes,body,views:[],members:[]};
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
    const counts=communicationCounts(message.members,actor()),format=value=>value.toLocaleString(locale());
    group.title.textContent=translate(locale(),counts.tasks===1?'tool.communicationStackOneTask':'tool.communicationStack').replace('{records}',format(counts.records)).replace('{messages}',format(counts.messages)).replace('{tasks}',format(counts.tasks));
    counts.routes.forEach(({mail,count},index)=>{
      if(!group.views[index]){const view=createCommunication({locale,resolveIdentity,onOpenTask});const route=node('div','communication-stack-route'),label=node('span','communication-stack-count');route.append(view.summary,label);group.views[index]={view,route,label};group.routes.append(route);}
      const {view,route,label}=group.views[index];route.hidden=false;
      view.update({kind:'send',messages:[mail],sources:[],count:1},{summaryOnly:true});
      label.textContent=translate(locale(),count===1?'tool.communicationRouteOne':'tool.communicationRouteCount').replace('{count}',format(count));
    });
    group.views.slice(counts.routes.length).forEach(view=>view.route.hidden=true);
    let index=0;
    for(const member of message.members){
      // Opening a group reveals readable text immediately, while call details
      // remain a separate native disclosure. Already mounted bodies stay alive.
      if(!group.details.open&&!records.has(member.viewKey))continue;
      const record=renderMember(member,message.viewKey,true);
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
    if(currentScope!==undefined&&currentScope!==nextScope){owners.clear();window.setMessages([]);for(const {record}of records.values())options.dispose(record);records.clear();project=createCommunicationProjection();source=undefined;}
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
