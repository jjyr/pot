import {translate} from '../locales/messages.mjs';

// Reply and approval observations have independent baselines per owner. Task
// outcomes use their own durable cursor rather than a full Task-list snapshot.
const identity=assistant=>({space:assistant.space_id,control:assistant.control_id||assistant.space_id,owner:assistant.id,name:String(assistant.name||'Pot').slice(0,60)});
const event=(base,kind,id,task='')=>({...base,kind,task,id:JSON.stringify([base.control,base.owner,kind,id])});
// At most 120 Unicode code points (480 UTF-8 bytes), within the native
// notification body's 512-byte limit, including emoji and CJK replies.
function replyPreview(value) {
  const text=Array.from(String(value||'').replace(/\s+/gu,' ').trim());
  return text.length>120?text.slice(0,119).join('')+'…':text.join('');
}
export function taskOutcomeNotifications(assistant,items) {
  const base=identity(assistant);
  // Failed tasks first enter assistant recovery. Only an unresolved failure
  // produces a reply notification; do not interrupt before recovery is attempted.
  return items.filter(item=>['complete','interrupted'].includes(item.phase)).map(item=>
    event({...base,name:String(item.title||base.name).slice(0,60)},item.phase==='complete'?'task-complete':'task-failed',JSON.stringify([item.id,item.sequence]),item.id));
}
export function createNotificationProjection() {
  const owners=new Map();
  return {
    reset(space,owner='',control='') {
      for(const [key,value] of owners)if((!space||value.space===space)&&(!owner||value.owner===owner)&&(!control||value.control===control))owners.delete(key);
    },
    observe(assistant,approvals,boot='') {
      const base=identity(assistant),key=JSON.stringify([base.control,base.space,base.owner]),stored=owners.get(key),previous=stored?.boot===boot?stored:undefined;
      const current={...base,boot,reply:assistant.reply_id,count:assistant.reply_count||0,
        approvals:approvals?new Set(approvals.map(item=>item.id)):previous?.approvals||null};
      owners.set(key,current);
      if(!previous)return [];
      const events=[];
      if(current.reply&&current.reply!==previous.reply&&current.count>previous.count)events.push(event({...base,preview:replyPreview(assistant.preview)},'reply',current.reply));
      for(const approval of previous.approvals?approvals||[]:[])if(!previous.approvals.has(approval.id))
        events.push(event({...base,name:String(approval.task_title||base.name).slice(0,60)},'approval',approval.id,approval.request?.task||''));
      return events;
    },
  };
}

export function viewingNotification(view,event) {
  if(!view?.focused||view.space!==event.space||view.owner!==event.owner)return false;
  if(view.task)return view.task===event.task;
  return view.route==='chat';
}

export function notificationText(event,locale='en') {
  const keys={reply:'notifications.replyComplete','task-complete':'notifications.taskComplete','task-failed':'notifications.taskFailed',approval:'notifications.approvalRequired'};
  const body=(event.kind==='reply'&&replyPreview(event.preview))||translate(locale,keys[event.kind]);
  return {title:`Pot · ${String(event.name||'Pot').replace(/[\r\n\t]/g,' ').slice(0,60)}`,body};
}
