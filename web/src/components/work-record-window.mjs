import {preparedDOM,preparedText} from './prepared-render.mjs';
import {createMessageWindow} from './message-window.mjs';
import {createWorkRecordProjection,workRecordParents} from '../state/work-records.mjs';
import {presentationLabel} from '../state/tool-presentation.mjs';
import {communicationParticipant} from './communication-participant.mjs';
import {reconcile} from './reconcile.mjs';
import {communication} from '../state/communication.mjs';
import {taskIdentities} from '../state/task-identity.mjs';
import {translate} from '../locales/messages.mjs';
import {mergeConversationRows} from '../state/stable-message-rows.mjs';

const node=(tag,cls='',text)=>{const value=document.createElement(tag);value.className=cls;if(text!==undefined)value.textContent=text;return value;};

export function createWorkRecordWindow({locale=()=> 'en',actor=()=>undefined,resolveIdentity=()=>undefined,blocked,
  memory=new Map(),scope='',...options}) {
  let project=createWorkRecordProjection(),currentScope,rawRows=[],closed=false;
  const currentActor=()=>actor();

  function render(message,existing){
    const record=options.render({...message,workRecord:!!message.workGroup},existing);
    let group=record.groupUI;
    if(!group){
      const prefix=node('div','work-record-prefix');
      const summary=node('button','work-record-summary');summary.type='button';
      const title=node('span','work-record-title');
      const counts=node('span','work-record-counts');
      const participants=node('span','work-record-participants');
      const failure=node('span','work-record-failure');
      const people=node('div','work-record-people');
      const activity=node('div','work-record-activity');
      summary.append(title,counts,participants,failure);
      prefix.append(summary,people,activity);
      record.row.prepend(prefix);
      group={prefix,summary,title,counts,participants,failure,people,activity,message};
      record.groupUI=group;
      summary.addEventListener('click',event=>{
        if(event.defaultPrevented)return;
        const g=record.groupUI;
        if(!g||!g.message?.workGroup)return;
        if(!g.prefix.isConnected||!options.host.contains(g.prefix))return;
        core.engage(event);event.preventDefault();core.pause();
        const meta=g.message.workGroup;
        const key=currentScope+meta.key;
        memory.set(key,{open:!meta.open});
        record.row.dispatchEvent(new CustomEvent('pot:prepare-resize',{bubbles:true}));
        options.requestRender();
      });
    }
    group.message=message;
    const metadata=message.workGroup;
    const leader=!!metadata?.leader;
    group.prefix.hidden=!leader;
    group.summary.setAttribute('aria-expanded',String(!!metadata?.open));
    record.row.classList.toggle('work-record-member',!!metadata);
    record.row.classList.toggle('work-record-group',leader);
    record.row.classList.toggle('work-record-folded',leader&&!metadata?.open&&!metadata?.exposed);
    record.row.hidden=!!metadata&&!leader&&!metadata.open&&!metadata.exposed;
    if(message.sequence!=null){record.row.dataset.sequence=String(message.sequence);}else{delete record.row.dataset.sequence;}
    if(metadata?.key){record.row.dataset.workGroup=metadata.key;}else{delete record.row.dataset.workGroup;}
    delete record.row.dataset.sequenceBefore;
    delete record.row.dataset.sequenceAfter;
    if(leader){
      const work=metadata;
      const format=value=>value.toLocaleString(locale()),t=key=>translate(locale(),'work.'+key);
      preparedText(group.title,'work-record-window:group.title',t('title'));
      preparedText(group.counts,'work-record-window:group.counts',(work.work.tools?[t('tools').replace('{count}',format(work.work.tools))]:[]).concat(work.work.messages?[t('messages').replace('{count}',format(work.work.messages))]:[]).join(' · '));
      group.counts.title=group.counts.textContent;
      group.failure.hidden=!work.work.failures;preparedText(group.failure,'work-record-window:group.failure',work.work.failures?t('failures').replace('{count}',format(work.work.failures)):'');
      const participantRevision=JSON.stringify([work.work.participants,locale(),work.work.participants.map(address=>resolveIdentity(address))]);
      preparedDOM(group.participants,'work-participants',participantRevision,target=>{
        const marks=node('span','work-record-participants');
        work.work.participants.forEach((address,index)=>{const mark=communicationParticipant(address,{resolveIdentity,locale});mark.dataset.participantIndex=String(index);marks.append(mark);});
        reconcile(target,marks);
      });group.participants.hidden=!work.work.participants.length;
      preparedDOM(group.people,'work-people',participantRevision,target=>{
        const people=node('div','work-record-people');for(const address of work.work.participants)people.append(communicationParticipant(address,{resolveIdentity,locale}));reconcile(target,people);
      });group.people.hidden=work.work.participants.length<2||!metadata.open;
      group.activity.hidden=!work.work.active;
      preparedText(group.activity,'work-record-window:group.activity',work.work.active?t('activity').replace('{name}',work.work.active.name==='compaction'?translate(locale(),'compact.title'):translate(locale(),'tool.'+presentationLabel(work.work.active))):'');
    }else{
      group.prefix.hidden=true;
    }
    return record;
  }

  const core=createMessageWindow({...options,locale,render});

  function reset(){
    rawRows=[];project=createWorkRecordProjection();currentScope=undefined;
    core.reset();
  }

  function layout(messages,settings={}){
    if(closed)return;
    const nextScope=typeof scope==='function'?scope():scope;
    if(currentScope!==undefined&&currentScope!==nextScope){reset();}
    currentScope=nextScope;
    rawRows=mergeConversationRows(rawRows,messages,settings);
    const visible=rawRows.filter(message=>!message.presentationSuppressed);
    const act=currentActor();
    const addresses=visible.flatMap(message=>communication(message.tool,act)?.messages.flatMap(mail=>[mail.from,mail.to])||[]);
    taskIdentities.register(addresses);
    const selection=getSelection(),selected=selection&&!selection.isCollapsed?selection.anchorNode?.parentElement?.closest('article.message'):null;
    const focus=document.activeElement?.closest('article.message');
    const anchor=(options.host.contains(focus)?focus:options.host.contains(selected)?selected:null)?.dataset.messageId||core.anchor?.id;
    const projected=project(visible,{actor:act,blocked,anchor});
    const nativeKeys=new Map(projected.map(m=>[m.id,m.viewKey]));
    for(const message of projected){
      const group=message.workGroup;
      if(!group)continue;
      const key=currentScope+group.key;
      if(!memory.has(key)){
        const inherited=group.memberIDs.map(id=>memory.get(currentScope+'work:'+nativeKeys.get(id))).find(value=>value!==undefined);
        if(inherited)memory.set(key,{open:!!inherited.open});
      }
      if(!core.revealing)continue;
      const anchors=core.position?.anchors;
      if(group.memberIDs.some(id=>
        id===core.destination?.id||
        core.visibilityPath?.has(id)||
        anchors?.some(a=>a.key===`message:${nativeKeys.get(id)}`)
      )){
        memory.set(currentScope+group.key,{open:true});
      }
    }
    const mapped=projected.map(message=>{
      if(!message.workGroup)return message;
      const key=currentScope+message.workGroup.key;
      const saved=memory.get(key);
      return {...message,workGroup:{...message.workGroup,open:saved?.open??false}};
    });
    core.setMessages(mapped,{...settings,parents:workRecordParents(mapped,settings.parents),
      afterCommit:()=>{
        settings.afterCommit?.();
        let needsResize=false;
        for(const message of mapped){
          const group=message.workGroup;
          if(!group)continue;
          const expected=memory.get(currentScope+group.key)?.open??false;
          if(expected!==group.open){needsResize=true;break;}
        }
        if(needsResize&&!closed&&currentScope===nextScope){
          const scopeCheck=currentScope;
          queueMicrotask(()=>{
            if(closed||currentScope!==scopeCheck)return;
            options.host?.dispatchEvent?.(new CustomEvent('pot:prepare-resize'));
            options.requestRender();
          });
        }
      }
    });
  }

  return Object.assign(Object.create(core),{setMessages:layout,close(){closed=true;core.close();},reset});
}
