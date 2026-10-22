import {node as n,action} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';
import {toolSummary} from '../state/tool-calls.mjs';

// Chat, Task calls and transcript-less requests share this exact review UI.
export function createApprovalCard({locale,machineLabel=()=>translate(locale(),'machine.unavailable'),onDecision}) {
  const t=key=>translate(locale(),`tool.${key}`);
  const element=n('details','approval-card'),summary=n('summary','approval-summary'),title=n('strong','approval-title'),machine=n('span','approval-machine'),state=n('span','approval-state'),operation=n('span','approval-operation');
  summary.append(title,machine,state,operation);
  const body=n('div','approval-body'),preview=n('div','approval-preview'),command=n('pre','approval-arguments'),reason=n('p','approval-justification'),consent=n('p','approval-description'),exact=n('pre','approval-arguments'),error=n('p','inline-error'),actions=n('div','approval-actions');
  error.setAttribute('role','alert');preview.append(command,reason,consent,exact);body.append(preview,error,actions);element.append(summary,body);
  let current,signature='',busy=false;
  const buttons=new Map();
  function update(approval,{taskTitle=approval.task_title||''}={}) {
    current=approval;const request=approval.request;
    element.dataset.approvalId=approval.id;element.dataset.taskId=request.task||'';
    title.textContent=taskTitle||t('approvalTitle');machine.textContent=approval.source||machineLabel(request.machine_id);state.textContent=t('waiting_approval');
    operation.textContent=approval.once_only?translate(locale(),request.args?.networkApprovalContext?'agentLinks.reviewNetwork':request.tool==='item/fileChange/requestApproval'?'agentLinks.reviewFiles':'agentLinks.reviewCommand'):toolSummary({name:request.tool,args:request.args});operation.title=operation.textContent;
    const next=JSON.stringify([approval.id,request,approval.details,machine.textContent,locale()]);
    if(signature===next)return;signature=next;
    command.hidden=!request.args?.cmd;command.textContent=request.args?.cmd||'';
    reason.hidden=!request.args?.justification;reason.textContent=`${t('justification')}: ${request.args?.justification||''}`;
    consent.textContent=approval.once_only?translate(locale(),'agentLinks.reviewConsent'):t(request.tool==='update_agent_instructions'?'instructionConsent':'hostConsent');
    exact.textContent=`${machine.textContent} · ${request.tool}\n${JSON.stringify(approval.details||request.command_context||request.args,null,2)}`;
    const names=approval.once_only||request.tool==='update_agent_instructions'?['allow_once','deny']:['allow_always','allow_task','deny'];
    for(const name of names) {
      if(!buttons.has(name))buttons.set(name,action('',async()=>{
        if(busy)return;busy=true;error.textContent='';buttons.forEach(button=>button.disabled=true);
        const original=current;
        try{await onDecision(original,name);}catch(failure){error.textContent=failure.message||String(failure);}
        finally{busy=false;buttons.forEach(button=>button.disabled=false);}
      },{primary:name==='allow_task'||name==='allow_once'}));
      const button=buttons.get(name);button.textContent=t(name);button.disabled=busy;
      if(button.parentNode!==actions)actions.append(button);
    }
    for(const [name,button] of buttons)if(!names.includes(name)){button.remove();buttons.delete(name);}
  }
  return {element,update};
}
