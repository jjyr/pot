import {taskAgentSource} from './connection-visuals.mjs';
import {icon} from './icons.mjs';
import {reconcile} from './reconcile.mjs';
import {taskElapsed} from './task-time.mjs';
import {translate} from '../locales/messages.mjs';
import {taskIdentities} from '../state/task-identity.mjs';
import {taskIdentityMark,taskIdentityShort} from './task-identity.mjs';

const node=(tag,className='',text)=>{const value=document.createElement(tag);value.className=className;if(text!==undefined)value.textContent=text;return value;};
const taskKey=task=>JSON.stringify([task._space_id,task.assistant_id,task.id]);

// Conversation presentation state survives moving the strip to a newer reply,
// detail navigation and refreshes. It is never part of the transcript.
export function createBackgroundTasks({locale,onOpen,onToggle}) {
  const element=node('section','background-tasks'),states=new Map();
  const id=`background-tasks-${crypto.randomUUID()}`;
  let current=null,signature='';
  function update(scope,tasks,needsApproval) {
    let state=states.get(scope);
    if(!tasks.length){const changed=current===scope&&signature!=='';states.delete(scope);element.remove();current=null;signature='';return changed;}
    if(!state){state={expanded:false,order:[]};states.set(scope,state);}
    taskIdentities.register(tasks);
    const nextSignature=JSON.stringify([scope,locale(),state.expanded,tasks.map(task=>[taskKey(task),task.address,taskIdentities.short(task),task.title,task.phase,task.started_at_ms,task.agent_link,needsApproval(task.id,task.phase)])]);
    if(current===scope&&signature===nextSignature)return false;
    signature=nextSignature;
    const byID=new Map(tasks.map(task=>[taskKey(task),task]));
    state.order=state.order.filter(key=>byID.has(key));
    for(const key of byID.keys())if(!state.order.includes(key))state.order.push(key);
    // Keep existing order within each approval group; clock/status refreshes and
    // backend updated-at ordering must not shuffle the reader's task rows.
    const ordered=state.order.map(key=>byID.get(key)).sort((a,b)=>Number(needsApproval(b.id,b.phase))-Number(needsApproval(a.id,a.phase)));
    const t=key=>translate(locale(),key),projection=node('section','background-tasks');
    projection.setAttribute('aria-labelledby',`${id}-heading`);
    const heading=node('h2','background-tasks-heading',t('tasks.backgroundCount').replace('{count}',tasks.length.toLocaleString(locale())));
    heading.id=`${id}-heading`;heading.dataset.scrollAnchor='background-tasks-heading';
    const list=node('div','background-task-list');list.id=id;
    for(const task of state.expanded?ordered:ordered.slice(0,3)){
      const open=node('button','background-task');open.type='button';open.dataset.viewKey=taskKey(task);open.dataset.scrollAnchor=`task:${taskKey(task)}`;open.title=[task.title,task.address?.node_id,task.address?.agent_id].filter(Boolean).join(' · ');
      const waiting=needsApproval(task.id,task.phase),meta=node('span','background-task-meta');open.classList.toggle('needs-approval',waiting);
      open.append(taskIdentityMark(task),node('span','background-task-title',task.title),taskIdentityShort(task,t('tool.messageTaskIDMissing')));
      const description=node('span','background-task-description visually-hidden');
      description.id=`${id}-description-${list.children.length}`;
      open.setAttribute('aria-describedby',description.id);
      if(task.address?.node_id)description.append(node('span','',t('tool.messageFromSource').replace('{source}',task.address.node_id)+' · '));
      const status=waiting?'tool.waiting_approval':task.phase==='queued'?'tasks.queued':task.phase==='waiting'?'tasks.waiting':task.phase==='complete'?'tasks.complete':task.phase==='failed'?'tasks.failed':'tool.running';
      const source=taskAgentSource(task,locale());if(source)description.append(node('span','',source.getAttribute('aria-label')+' · '));
      const activity=node('span','task-activity-dot');activity.setAttribute('aria-hidden','true');activity.dataset.phase=waiting?'waiting_approval':task.phase;
      meta.append(activity,node('span','background-task-status',t(status)));
      const time=taskElapsed(task.started_at_ms,Date.now(),locale());
      if(time){
        const separator=node('span','','·');separator.setAttribute('aria-hidden','true');
        const elapsed=node('span','background-task-elapsed',time);elapsed.dataset.startedAt=task.started_at_ms;elapsed.setAttribute('role','timer');elapsed.setAttribute('aria-live','off');elapsed.title=t('tasks.elapsedDescription');
        description.append(separator,elapsed);
      }
      open.append(meta,description);open.onclick=()=>onOpen(task);list.append(open);
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
    if(current!==scope)element.replaceChildren();current=scope;
    reconcile(element,projection);return true;
  }
  return {element,update,close(){}};
}
