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
  // Fit an ordered prefix of identities, reserving room for the remaining count.
  // Resize only changes presentation; it never changes task/row order or counts.
  function fitSummary(){
    const summary=element.querySelector('.background-task-identities');if(!summary||summary.hidden||!summary.clientWidth)return;
    const items=[...summary.querySelectorAll('.background-task-identity')],more=summary.querySelector('.background-task-more');
    items.forEach(item=>item.hidden=false);more.hidden=false;
    const widths=items.map(item=>item.getBoundingClientRect().width),available=summary.clientWidth;
    let used=0,count=0;
    for(const width of widths){
      const remaining=items.length-count-1;more.textContent='+'+Math.max(remaining,1).toLocaleString(locale());
      const required=used+width+(count?8:0)+(remaining?8+more.getBoundingClientRect().width:0);
      if(required>available)break;
      used+=width+(count?8:0);count++;
    }
    items.forEach((item,index)=>item.hidden=index>=count);
    const remaining=items.length-count;more.hidden=!remaining;more.textContent='+'+remaining.toLocaleString(locale());
    more.setAttribute('aria-label',translate(locale(),more.tagName==='BUTTON'?(remaining===1?'tasks.expandOne':'tasks.expandMore'):'tasks.moreIdentities').replace('{count}',remaining.toLocaleString(locale())));
    const focused=items.find(item=>item.hidden&&item.contains(document.activeElement));
    if(focused){
      const target=more.tagName==='BUTTON'?more:[...element.querySelectorAll('.background-task')].find(row=>row.dataset.viewKey===focused.dataset.viewKey);
      target?.focus({preventScroll:true});
    }
  }
  const resize=new ResizeObserver(fitSummary);resize.observe(element);
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
    const header=node('div','background-tasks-header'),summary=node('div','background-task-identities');summary.hidden=state.expanded;
    for(const task of ordered){
      const identity=node('button','background-task-identity');identity.type='button';identity.dataset.viewKey=taskKey(task);
      identity.title=[task.title,task.address?.node_id,task.address?.agent_id].filter(Boolean).join(' · ');
      identity.setAttribute('aria-label',t('tool.viewMessageTask').replace('{title}',task.title)+' '+(taskIdentities.short(task)||t('tool.messageTaskIDMissing')));
      identity.append(taskIdentityMark(task),taskIdentityShort(task,t('tool.messageTaskIDMissing')));identity.onclick=()=>onOpen(task);summary.append(identity);
    }
    const more=node(tasks.length>3?'button':'span','background-task-more');more.dataset.viewKey='more';
    if(tasks.length>3){more.type='button';more.setAttribute('aria-controls',id);more.setAttribute('aria-expanded','false');more.onclick=()=>{state.expanded=true;onToggle({expanded:true});};}
    summary.append(more);header.append(heading,summary);
    const list=node('div','background-task-list');list.id=id;
    for(const task of state.expanded?ordered:ordered.slice(0,3)){
      const open=node('button','background-task');open.type='button';open.dataset.viewKey=taskKey(task);open.dataset.scrollAnchor=`task:${taskKey(task)}`;open.title=task.title;
      const waiting=needsApproval(task.id,task.phase),meta=node('span','background-task-meta');open.classList.toggle('needs-approval',waiting);
      const identity=node('span','background-task-label');
      identity.append(node('span','background-task-title',task.title),taskIdentityShort(task,t('tool.messageTaskIDMissing')));
      open.append(taskIdentityMark(task),identity);
      const status=waiting?'tool.waiting_approval':task.phase==='queued'?'tasks.queued':task.phase==='waiting'?'tasks.waiting':task.phase==='complete'?'tasks.complete':task.phase==='failed'?'tasks.failed':'tool.running';
      const source=taskAgentSource(task,locale());if(source)meta.append(source);
      const activity=node('span','task-activity-dot');activity.setAttribute('aria-hidden','true');activity.dataset.phase=waiting?'waiting_approval':task.phase;
      meta.append(activity,node('span','background-task-status',t(status)));
      const time=taskElapsed(task.started_at_ms,Date.now(),locale());
      if(time){
        const separator=node('span','','·');separator.setAttribute('aria-hidden','true');
        const elapsed=node('span','background-task-elapsed',time);elapsed.dataset.startedAt=task.started_at_ms;elapsed.setAttribute('role','timer');elapsed.setAttribute('aria-live','off');elapsed.title=t('tasks.elapsedDescription');
        meta.append(separator,elapsed);
      }
      open.append(meta);open.onclick=()=>onOpen(task);list.append(open);
    }
    projection.append(header,list);
    if(tasks.length>3){
      const toggle=node('button','background-tasks-toggle');toggle.type='button';toggle.dataset.viewKey='toggle';toggle.dataset.scrollAnchor='background-tasks-toggle';
      toggle.setAttribute('aria-controls',id);toggle.setAttribute('aria-expanded',String(state.expanded));
      const count=tasks.length-3,label=state.expanded?t('tasks.collapse'):t(count===1?'tasks.expandOne':'tasks.expandMore').replace('{count}',count.toLocaleString(locale()));
      toggle.append(node('span','',label),icon('down'));
      toggle.onclick=()=>{state.expanded=!state.expanded;onToggle({expanded:state.expanded});};projection.append(toggle);
    }
    // A different conversation must not inherit another owner's keyed DOM.
    if(current!==scope)element.replaceChildren();current=scope;
    reconcile(element,projection);fitSummary();return true;
  }
  return {element,update,close(){resize.disconnect();}};
}
