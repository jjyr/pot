import {taskAgentSource} from './connection-visuals.mjs';
import {icon} from './icons.mjs';
import {reconcile} from './reconcile.mjs';
import {taskElapsed,taskClockEnd} from './task-time.mjs';
import {translate} from '../locales/messages.mjs';
import {taskIdentities} from '../state/task-identity.mjs';
import {taskIdentityMark,taskIdentityShort} from './task-identity.mjs';

const node=(tag,className='',text)=>{const value=document.createElement(tag);value.className=className;if(text!==undefined)value.textContent=text;return value;};
const taskKey=task=>JSON.stringify([task._space_id,task.assistant_id,task.id]);
const statuses={queued:'tasks.queued',running:'tool.running',waiting:'tasks.waiting',waiting_approval:'tool.waiting_approval',complete:'tasks.complete',failed:'tasks.failed',cancelled:'schedule.state.cancelled',interrupted:'schedule.state.interrupted'};
const activePhases=new Set(['queued','running','waiting','waiting_approval']);

// Conversation presentation state survives moving the strip to a newer reply,
// detail navigation and refreshes. It is never part of the transcript.
export function createBackgroundTasks({locale,onOpen,onToggle}) {
  const element=node('section','background-tasks'),states=new Map();
  const baseID=`background-tasks-${crypto.randomUUID()}`;let presentation=0;
  let current=null,signature='';
  function update(scope,tasks,needsApproval,target=element,now=Date.now()) {
    let state=states.get(scope);
    target.hidden=!tasks.length;
    if(!tasks.length){const changed=current===scope&&signature!=='';target.replaceChildren();if(target===element){states.delete(scope);current=null;signature='';}return changed;}
    if(!state){state={expanded:false,order:[],observed:new Map()};states.set(scope,state);}
    const byID=new Map(tasks.map(task=>{
      const key=taskKey(task),previous=state.observed.get(key);
      // Display memory never grants capabilities or changes the source record.
      // Retained data cannot roll a confirmed phase back during revalidation.
      const presentation=task._stale&&previous?{...previous}: {
        phase:task.phase,started_at_ms:task.started_at_ms,finished_at_ms:task.finished_at_ms,
        waiting:task.phase==='waiting_approval'||(!task._stale&&needsApproval(task.id,task.phase)),
      };
      if(task._stale){
        // A mounted live clock stops at loss of confirmation. A cold retained
        // active row has no observed duration, so do not invent one from now.
        if(!previous?.stale)presentation.clockEnd=previous?taskClockEnd(previous,now):activePhases.has(task.phase)?undefined:task.finished_at_ms;
      }else presentation.clockEnd=undefined;
      presentation.stale=!!task._stale;state.observed.set(key,presentation);
      return [key,{...task,...presentation,source:task}];
    }));
    state.records=byID;
    for(const key of state.observed.keys())if(!byID.has(key))state.observed.delete(key);
    taskIdentities.register(tasks);
    const nextSignature=JSON.stringify([scope,locale(),state.expanded,[...byID.values()].map(task=>[taskKey(task),task.address,taskIdentities.short(task),task.title,task.phase,task.stale,task.clockEnd,task.started_at_ms,task.finished_at_ms,task.agent_link,task.waiting])]);
    if(target===element&&current===scope&&signature===nextSignature)return false;
    state.order=state.order.filter(key=>byID.has(key));
    for(const key of byID.keys())if(!state.order.includes(key))state.order.push(key);
    // Keep existing order within each approval group; clock/status refreshes and
    // backend updated-at ordering must not shuffle the reader's task rows.
    const ordered=state.order.map(key=>byID.get(key)).sort((a,b)=>Number(b.waiting)-Number(a.waiting));
    const id=baseID+'-'+ ++presentation;
    const t=key=>translate(locale(),key),projection=node('section','background-tasks');
    projection.setAttribute('aria-labelledby',`${id}-heading`);
    const heading=node('h2','background-tasks-heading',t('tasks.backgroundCount').replace('{count}',tasks.length.toLocaleString(locale())));
    heading.id=`${id}-heading`;heading.dataset.scrollAnchor='background-tasks-heading';
    const list=node('div','background-task-list');list.id=id;
    for(const task of state.expanded?ordered:ordered.slice(0,3)){
      const open=node('button','background-task');open.type='button';open.dataset.viewKey=taskKey(task);open.dataset.scrollAnchor=`task:${taskKey(task)}`;open.title=[task.title,task.address?.node_id,task.address?.agent_id].filter(Boolean).join(' · ');
      const waiting=task.waiting,meta=node('span','background-task-meta');open.classList.toggle('needs-approval',waiting);
      open.append(taskIdentityMark(task),taskIdentityShort(task,t('tool.messageTaskIDMissing')),node('span','background-task-title',task.title));
      const description=node('span','background-task-description visually-hidden');
      description.id=`${id}-description-${list.children.length}`;
      open.setAttribute('aria-describedby',description.id);
      if(task.address?.node_id)description.append(node('span','',t('tool.messageFromSource').replace('{source}',task.address.node_id)+' · '));
      const status=waiting?'tool.waiting_approval':statuses[task.phase]||'loading.pending';
      const source=taskAgentSource(task,locale());if(source)description.append(node('span','',source.getAttribute('aria-label')+' · '));
      const activity=node('span','task-activity-dot');activity.setAttribute('aria-hidden','true');activity.dataset.phase=waiting?'waiting_approval':statuses[task.phase]?task.phase:'unknown';
      meta.append(activity,node('span','background-task-status',t(status)));
      const end=task.stale?task.clockEnd:taskClockEnd(task,now);
      const time=statuses[task.phase]?taskElapsed(task.started_at_ms,end,locale(),true):'';
      if(time){
        const separator=node('span','','·');separator.setAttribute('aria-hidden','true');
        const elapsed=node('span','background-task-elapsed',time);elapsed.dataset.startedAt=task.started_at_ms;elapsed.dataset.compact='true';if(task.stale||!activePhases.has(task.phase))elapsed.dataset.finishedAt=end||0;elapsed.setAttribute('role','timer');elapsed.setAttribute('aria-live','off');elapsed.title=t('tasks.elapsedDescription');
        meta.append(separator,elapsed);
      }
      open.append(meta,description);open.onclick=()=>onOpen(state.records.get(taskKey(task)).source);list.append(open);
    }
    projection.append(heading,list);
    if(tasks.length>3){
      const toggle=node('button','background-tasks-toggle');toggle.type='button';toggle.dataset.viewKey='toggle';toggle.dataset.scrollAnchor='background-tasks-toggle';
      toggle.setAttribute('aria-controls',id);toggle.setAttribute('aria-expanded',String(state.expanded));
      const count=tasks.length-3,label=state.expanded?t('tasks.collapse'):t(count===1?'tasks.expandOne':'tasks.expandMore').replace('{count}',count.toLocaleString(locale()));
      toggle.append(node('span','',label),icon('down'));
      toggle.onclick=()=>{state.expanded=!state.expanded;onToggle({expanded:state.expanded});};projection.append(toggle);
    }
    // A different conversation must not inherit another owner's keyed DOM.
    if(target===element){if(current!==scope)element.replaceChildren();current=scope;signature=nextSignature;}
    reconcile(target,projection);return true;
  }
  return {element,update,region(scope,tasks,needsApproval){
    tasks=structuredClone(tasks);const now=Date.now();
    const decisions=new Map(tasks.map(task=>[task.id,needsApproval(task.id,task.phase)]));
    return {key:'background-tasks',host:element,revision:JSON.stringify([scope,locale(),states.get(scope)?.expanded||false,tasks,tasks.map(task=>taskElapsed(task.started_at_ms,taskClockEnd(task,now),locale(),true)),[...decisions]]),render:target=>update(scope,tasks,id=>decisions.get(id),target,now)};
  },close(){}};
}
