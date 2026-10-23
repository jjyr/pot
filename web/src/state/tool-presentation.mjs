// Display-only classification. Never infer effects or execution targets from code.
export const toolLabels={exec_command:'command',write_stdin:'session',apply_patch:'patch',exec:'script',wait:'wait',cua_repl:'computer',create_task:'createTask',list_tasks:'listTasks',get_task:'getTask',wait_task:'waitTask',interrupt:'interruptTask',cancel_task:'cancelTask',set_task_dispositions:'setTaskDispositions',retry_task:'retryTask',list_schedules:'listSchedules',get_schedule:'getSchedule',manage_schedule:'manageSchedule',list_execution_targets:'listTargets',list_assistants:'listAssistants',get_model_catalog:'modelCatalog',register_resource:'registerResource',present_resource:'presentResource'};
export function toolArgs(tool){
  let value=tool?.args;
  if(typeof value==='string'){try{value=JSON.parse(value);}catch{return null;}}
  return value&&typeof value==='object'&&!Array.isArray(value)?value:null;
}
export function toolKind(tool){
  const name=tool?.name;
  if(['exec_command','write_stdin'].includes(name))return 'terminal';
  if(name==='apply_patch')return 'patch';
  if(['exec','wait'].includes(name))return 'script';
  if(name==='cua_repl')return 'browser';
  if(['list_tasks','get_task','wait_task','interrupt','cancel_task','retry_task','set_task_dispositions'].includes(name))return 'task';
  if(['list_schedules','get_schedule','manage_schedule'].includes(name))return 'schedule';
  if(['register_resource','present_resource'].includes(name))return 'resource';
  return 'data';
}
export function presentationLabel(tool){
  const action=toolArgs(tool)?.action;
  return tool.name==='manage_schedule'&&['create','update','pause','resume','delete','run'].includes(action)?'scheduleAction.'+action:Object.hasOwn(toolLabels,tool.name)?toolLabels[tool.name]:'externalTool';
}
export function presentationPreview(tool,{resolveTask=()=>undefined}={}){
  const args=toolArgs(tool)||{};
  if(toolKind(tool)==='patch')return String(args.patch||'').match(/^\*\*\* (?:Add|Update|Delete) File: (.+)$/m)?.[1]||'';
  if(toolKind(tool)==='task')return resolveTask(args.task_id)?.title||'';
  if(tool.name==='manage_schedule')return args.spec?.title||'';
  if(toolKind(tool)==='resource')return args.name||args.path||'';
  if(toolKind(tool)==='data')return String(args.query||args.q||args.url||args.path||'');
  return '';
}
export function patchGroups(patch=''){
  const groups=[];let current;
  for(const line of String(patch).split('\n')){
    const match=line.match(/^\*\*\* (Add File|Update File|Delete File): (.+)$/);
    if(match){current={path:match[2],operation:({'Add File':'added','Update File':'modified','Delete File':'deleted'})[match[1]],lines:[],added:0,removed:0};groups.push(current);}
    else if(current&&line.startsWith('*** Move to: '))current.destination=line.slice(13);
    else if(current&&!line.startsWith('*** ')&&line!==''){
      current.lines.push(line);if(line.startsWith('+'))current.added++;else if(line.startsWith('-'))current.removed++;
    }
  }
  return groups;
}
