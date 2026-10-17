import {node as n,action,field,table} from './primitives.mjs';

export function renderExecutionPermissions({root,machines,policy,request,scope,locale,refresh,edit,stale=false}) {
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en;
  const labels={ask:tr('Protected · sandbox by default','受保护 · 默认沙箱'),allow:tr('Trusted · host execution','可信 · 宿主执行'),deny:tr('Blocked','禁止执行')};
  const ruleLabels={allow_host:tr('Allow host','允许宿主执行'),default:tr('Follow machine default','遵循机器默认策略'),deny_host:tr('Deny host','禁止宿主执行')};
  const status=n('p','inline-error');status.setAttribute('role','alert');root.append(status);
  const mutate=async(button,body)=>{button.disabled=true;status.textContent='';try{await request('/api/v1/permissions',{...scope,method:'POST',body});await refresh();}catch(error){status.textContent=error.message;button.disabled=false;}};
  const editor=(machine,existing)=>{
    const id=machine.runtime_id||machine.id,current=policy.machines?.[id]||machine.policy,context=existing?.context||{};
    const initial={command:'',directory:'',shell:'/bin/bash',login:false,snapshot:true,interactive:false,tty:false,credentials:'',policy:'allow_host',...context,...(existing?{policy:existing.policy}:{}),credentials:context.credentials?JSON.stringify(context.credentials,null,2):''};
    edit({title:existing?tr('Edit command permission','编辑命令授权'):tr('Add command permission','新增命令授权'),initial,scope,fields:[
      {name:'command',label:tr('Exact command','完整命令'),kind:'textarea',required:true,help:tr('Exact text only. Arguments, pipelines and compound commands are not prefixes.','按完整文本匹配，不按参数前缀、管道或复合命令扩展授权。')},
      {name:'directory',label:tr('Working directory','工作目录'),required:true,placeholder:'/Projects/shop'},
      {name:'shell',label:tr('Shell executable','Shell 路径'),required:true},
      {name:'login',label:tr('Login initialization','加载登录初始化'),kind:'checkbox'},
      {name:'snapshot',label:tr('Shell snapshot setting','Shell 快照配置'),kind:'checkbox'},
      {name:'interactive',label:tr('Interactive host session','交互式宿主会话'),kind:'checkbox',help:tr('An interactive session may accept further commands.','交互式会话可以通过后续输入执行其他代码。')},
      {name:'tty',label:tr('Allocate terminal (TTY)','分配终端（TTY）'),kind:'checkbox'},
      {name:'credentials',label:tr('Credential bindings (JSON references)','凭据绑定（JSON 引用）'),kind:'textarea',help:'{"GH_TOKEN":{"service":"github"}}'},
      {name:'policy',label:tr('Host permission','宿主执行策略'),choices:Object.entries(ruleLabels)},
    ],save:values=>{const {policy:effect,credentials,...command}=values;return request('/api/v1/permissions',{...scope,method:'POST',body:{action:'save_rule',old_id:existing?.id||'',expected_revision:current.revision,rule:{machine_id:id,context:{...command,...(credentials.trim()?{credentials:JSON.parse(credentials)}:{})},policy:effect}}});}});
  };
  for(const machine of machines){
    const id=machine.runtime_id||machine.id,current=policy.machines?.[id]||machine.policy||{execution_access:'ask'},card=n('section','page-card permission-machine');card.dataset.machineId=id;
    card.append(n('h2','',machine.name||id));
    const mode=field(tr('Default execution','默认执行方式'),current.execution_access,{choices:Object.entries(labels)});card.append(mode.row);
    card.append(n('p','hint',tr('Trust applies to this machine only. Host execution bypasses file and socket protection. Saving settings stops existing execution on this machine.','信任仅适用于这台机器。宿主执行不受文件和 socket 隔离保护。保存设置会停止该机器上的现有执行。')));
    const save=action(tr('Save permissions','保存权限'),()=>mutate(save,{action:'configure',machine_id:id,machine_policy:{execution_access:mode.input.value,revision:current.revision}}),{disabled:stale||policy.application?.[id]?.recovery_required});card.append(save);
    const application=policy.application?.[id];if(application&&application.state!=='applied'){const box=n('div','permission-application');box.append(n('p','inline-error',tr('Permission update is not confirmed: ','权限变更尚未确认生效：')+(application.error||application.state)));const retry=action(tr(application.recovery_required?'Owner recovery required':'Retry application',application.recovery_required?'需要所有者恢复':'重试应用'),()=>mutate(retry,{action:'retry',machine_id:id,expected_revision:current.revision}),{disabled:stale||application.recovery_required});box.append(retry);card.append(box);}
    const header=n('div','list-row');header.append(n('h3','row-info',tr('Permanent command permissions','永久命令授权')),action(tr('Add permission','新增授权'),()=>editor(machine),{disabled:stale}));card.append(header);
    const rules=(policy.rules||[]).filter(rule=>rule.machine_id===id);
    if(!rules.length)card.append(n('p','hint',tr('No permanent command permissions.','暂无永久命令授权。')));
    else card.append(table([tr('Command and directory','命令与目录'),tr('Policy','策略'),tr('Updated','更新时间'),tr('Actions','操作')],rules.map(rule=>{const command=n('div');command.append(n('pre','tool-code',rule.context.command),n('p','hint',rule.context.directory),n('small','hint',`${rule.context.shell}${rule.context.interactive?' · interactive':''}`));const controls=n('div','editor-actions'),remove=action(tr('Remove','移除'),()=>mutate(remove,{action:'delete_rule',machine_id:id,id:rule.id,expected_revision:current.revision}),{disabled:stale});controls.append(action(tr('Edit','编辑'),()=>editor(machine,rule),{disabled:stale}),remove);return[command,ruleLabels[rule.policy],`${rule.updated_at||''} · ${rule.updated_by||''}`,controls];})));
    card.append(n('p','hint',tr('Removing a command rule restores the machine default; it does not remove machine trust.','移除命令规则后恢复机器默认策略，不会取消整台机器的信任。')));
    const grants=(policy.task_grants||[]).filter(grant=>grant.machine_id===id);if(grants.length){card.append(n('h3','',tr('Active task authorizations','当前任务授权')));for(const grant of grants){const row=n('div','list-row'),info=n('div','row-info');info.append(n('pre','tool-code',grant.context.command),n('p','hint',`${grant.context.directory} · ${grant.task||grant.turn}`));const remove=action(tr('Remove task authorization','移除任务授权'),()=>mutate(remove,{action:'remove_task_grant',id:grant.id}),{disabled:stale});row.append(info,remove);card.append(row);}}
    root.append(card);
  }
}
