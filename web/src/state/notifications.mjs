// Reply and approval observations have independent baselines per owner. Task
// outcomes use their own durable cursor rather than a full Task-list snapshot.
const identity=assistant=>({space:assistant.space_id,control:assistant.control_id||assistant.space_id,owner:assistant.id,name:String(assistant.name||'Pot').slice(0,60)});
const event=(base,kind,id,task='')=>({...base,kind,task,id:JSON.stringify([base.control,base.owner,kind,id])});
export function taskOutcomeNotifications(assistant,items) {
  const base=identity(assistant);
  return items.filter(item=>['complete','failed','interrupted'].includes(item.phase)).map(item=>
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
      if(current.reply&&current.reply!==previous.reply&&current.count>previous.count)events.push(event(base,'reply',current.reply));
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
  const zh=locale==='zh-CN';
  const bodies={reply:zh?'助手回复已完成':'Assistant reply complete','task-complete':zh?'后台任务已完成':'Background task complete',
    'task-failed':zh?'后台任务失败或中断':'Background task failed or interrupted',approval:zh?'需要你的审批，请在 Pot 中查看':'Approval required. Review in Pot.'};
  return {title:`Pot · ${String(event.name||'Pot').replace(/[\r\n\t]/g,' ').slice(0,60)}`,body:bodies[event.kind]};
}
