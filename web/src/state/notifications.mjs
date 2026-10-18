// Catalogue projections are observations, not an event log. The first successful
// read after startup, disconnection or a boot change is always a baseline.
export function createNotificationProjection() {
  const owners = new Map();
  return {
    reset(space) { for (const [key, value] of owners) if (!space || value.space === space) owners.delete(key); },
    observe(assistant, tasks, approvals, boot = '') {
      const space=assistant.space_id, control=assistant.control_id||space;
      const key=JSON.stringify([control,assistant.id]),stored=owners.get(key),previous=stored?.boot===boot?stored:undefined;
      const current={space,boot,reply:assistant.reply_id,count:assistant.reply_count||0,
        tasks:tasks?new Map(tasks.map(task=>[task.id,task.phase])):previous?.tasks||null,approvals:approvals?new Set(approvals.map(item=>item.id)):previous?.approvals||null};
      owners.set(key,current);
      if(!previous||previous.boot!==boot)return [];
      const events=[],base={space,control,owner:assistant.id,name:String(assistant.name||'Pot').slice(0,60)};
      const add=(kind,id,task='')=>events.push({...base,kind,task,id:JSON.stringify([control,assistant.id,kind,id])});
      if(current.reply&&current.reply!==previous.reply&&current.count>previous.count)add('reply',current.reply);
      for(const task of previous.tasks?tasks||[]:[]) {
        if(previous.tasks.get(task.id)===task.phase)continue;
        if(['complete','failed','interrupted'].includes(task.phase)) {
          add(task.phase==='complete'?'task-complete':'task-failed',JSON.stringify([task.id,task.updated_at_ms||0]),task.id);
          events.at(-1).name=String(task.title||base.name).slice(0,60);
        }
      }
      for(const approval of previous.approvals?approvals||[]:[])if(!previous.approvals.has(approval.id)){
        const taskID=approval.request?.task||'';add('approval',approval.id,taskID);
        const title=tasks?.find(task=>task.id===taskID)?.title;
        if(title)events.at(-1).name=String(title).slice(0,60);
      }
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
