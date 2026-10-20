import {reconcile} from './reconcile.mjs';
import {node as n,action,field} from './primitives.mjs';
import {icon} from './icons.mjs';
import {translate} from '../locales/messages.mjs';

export function renderMachinePermissions({root:target,machine,policy,request,scope,locale,refresh,edit,draft,isCurrent,stale=false}) {
  const root=n('div','machine-permissions');
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en;
  const labels={ask:tr('Sandbox execution','沙箱执行'),allow:tr('Direct execution','直接执行'),deny:tr('Blocked','禁止执行')};
  const ruleLabels={allow_host:tr('Allow host','允许宿主执行'),default:tr('Follow machine default','遵循机器默认策略'),deny_host:tr('Deny host','禁止宿主执行')};
  const id=machine.runtime_id||machine.id,current=policy.machines[id],application=policy.application?.[id];
  const warning=tr('Saving stops existing execution on this machine and clears temporary authorizations. Work is not restarted automatically.','保存会停止这台机器上的现有执行，并清除临时授权。任务不会自动重启。');
  const status=n('p','inline-error permission-status',draft.error||'');status.setAttribute('role','alert');
  const blocked=()=>stale||draft.pending||application?.recovery_required;
  const mutate=async(body,{configure=false}={})=>{
    if(blocked()||!draft.begin())return;
    draft.error='';update();
    try {
      if(!isCurrent())throw Error(translate(locale(),'topology.controlChanged'));
      await request('/api/v1/permissions',{...scope,method:'POST',body});
      draft.settle(configure);await refresh();
    }catch(error){draft.settle(false);draft.error=error.message;if(target.isConnected)update();else await refresh();}
  };
  const editor=existing=>{
    const context=existing?.context||{};
    const initial={command:'',directory:'',shell:'/bin/bash',login:false,snapshot:true,interactive:false,tty:false,policy:'allow_host',...context,...(existing?{policy:existing.policy}:{}),credentials:context.credentials?JSON.stringify(context.credentials,null,2):''};
    const form=edit({title:existing?tr('Edit command permission','编辑命令授权'):tr('Add command permission','新增命令授权'),initial,scope,fields:[
      {name:'command',label:tr('Exact command','完整命令'),kind:'textarea',required:true,help:tr('Exact text only. Arguments, pipelines and compound commands are not prefixes.','按完整文本匹配，不按参数前缀、管道或复合命令扩展授权。')},
      {name:'directory',label:tr('Working directory','工作目录'),required:true,placeholder:'/Projects/shop'},
      {name:'policy',label:tr('Host permission','宿主执行策略'),choices:Object.entries(ruleLabels)},
      {name:'shell',label:tr('Shell executable','Shell 路径'),required:true},
      {name:'login',label:tr('Login initialization','加载登录初始化'),kind:'checkbox'},
      {name:'snapshot',label:tr('Shell snapshot setting','Shell 快照配置'),kind:'checkbox'},
      {name:'interactive',label:tr('Interactive host session','交互式宿主会话'),kind:'checkbox',help:tr('An interactive session may accept further commands.','交互式会话可以通过后续输入执行其他代码。')},
      {name:'tty',label:tr('Allocate terminal (TTY)','分配终端（TTY）'),kind:'checkbox'},
      {name:'credentials',label:tr('Credential bindings (JSON references)','凭据绑定（JSON 引用）'),kind:'textarea',help:'{"GH_TOKEN":{"service":"github"}}'},
    ],save:values=>{if(!isCurrent())throw Error(translate(locale(),'topology.controlChanged'));const {policy:effect,credentials,...command}=values;return request('/api/v1/permissions',{...scope,method:'POST',body:{action:'save_rule',old_id:existing?.id||'',expected_revision:current.revision,rule:{machine_id:id,context:{...command,...(credentials.trim()?{credentials:JSON.parse(credentials)}:{})},policy:effect}}});}});
    form.form.querySelector('.editor-actions').before(n('p','hint permission-warning',warning));
  };
  const mode=field(tr('Default execution','默认执行方式'),draft.values.execution_access,{choices:Object.entries(labels)});
  mode.row.classList.add('permission-default');
  const changes=n('div','permission-changes'),conflict=n('p','inline-error'),save=action(tr('Save permissions','保存权限'),()=>mutate({action:'configure',machine_id:id,machine_policy:{execution_access:draft.values.execution_access,revision:draft.values.revision}},{configure:true}),{primary:true});
  const cancel=action(tr('Cancel','取消'),()=>{draft.settle(true,current);draft.error='';target.querySelector('.permission-default select').value=current.execution_access;update();});
  const controls=n('div','editor-actions');controls.append(cancel,save);changes.append(n('p','hint permission-warning',warning),conflict,controls);
  mode.input.onchange=event=>{draft.set('execution_access',event.currentTarget.value);update();};
  root.append(mode.row,changes,status);
  if(stale)root.append(n('p','hint',tr('Space unavailable. Cached permissions are read-only.','Space 不可用，缓存权限只读。')));
  if(application&&application.state!=='applied'){
    const box=n('div','permission-application');box.append(n('p','inline-error',tr('Permission update is not confirmed: ','权限变更尚未确认生效：')+(application.error||application.state)));
    const retry=action(tr(application.recovery_required?'Owner recovery required':'Retry application',application.recovery_required?'需要所有者恢复':'重试应用'),()=>mutate({action:'retry',machine_id:id,expected_revision:current.revision}));box.append(retry);root.append(box);
  }
  const rules=(policy.rules||[]).filter(rule=>rule.machine_id===id),disclosure=n('details','permission-rules');disclosure.dataset.cacheKey=JSON.stringify(['command-permissions',scope.space,id]);
  const summary=n('summary'),tail=n('span','permission-count');tail.append(n('span','',String(rules.length)),icon('chevron-right'));summary.append(n('span','',tr('Permanent command permissions','永久命令授权')),tail);disclosure.append(summary);
  const content=n('div','permission-rule-content');
  if(!rules.length)content.append(n('p','hint',tr('No permanent command permissions.','暂无永久命令授权。')));
  for(const rule of rules){
    const row=n('div','permission-rule'),command=n('div','row-info'),controls=n('div','editor-actions');
    row.dataset.viewKey=rule.id;
    command.append(n('pre','tool-code',rule.context.command),n('p','hint',rule.context.directory),n('small','hint',`${ruleLabels[rule.policy]} · ${rule.context.shell}${rule.context.interactive?' · '+tr('interactive','交互式'):''}`));
    const metadata=n('details','permission-rule-metadata');metadata.append(n('summary','hint',tr('Execution context and update','执行上下文与更新信息')));
    const context=n('pre','tool-code');context.textContent=JSON.stringify(rule.context,null,2);metadata.append(context,n('p','hint',`${rule.updated_at||''} · ${rule.updated_by||''}`));command.append(metadata);
    const remove=action(tr('Remove','移除'),()=>mutate({action:'delete_rule',machine_id:id,id:rule.id,expected_revision:current.revision}));
    controls.append(action(tr('Edit','编辑'),()=>editor(rule)),remove);row.append(command,controls);content.append(row);
  }
  content.append(action(tr('Add permission','新增授权'),()=>editor()));
  if(rules.length)content.append(n('p','hint',tr('Removing a command rule restores the machine default; it does not remove machine trust.','移除命令规则后恢复机器默认策略，不会取消整台机器的信任。')));
  disclosure.append(content);root.append(disclosure);
  const grants=(policy.task_grants||[]).filter(grant=>grant.machine_id===id);
  if(grants.length){
    const section=n('section','permission-grants');section.append(n('h3','',tr('Active task authorizations','当前任务授权')));
    for(const grant of grants){const row=n('div','permission-rule'),info=n('div','row-info');info.append(n('pre','tool-code',grant.context.command),n('p','hint',`${grant.context.directory} · ${grant.task||grant.turn}`));const remove=action(tr('Remove task authorization','移除任务授权'),()=>mutate({action:'remove_task_grant',id:grant.id}));row.append(info,remove);section.append(row);}
    root.append(section);
  }
  function update(){
    const conflicted=draft.dirty&&draft.values.revision!==current.revision;
    const liveSave=target.querySelector('.permission-changes .primary'),liveCancel=target.querySelector('.permission-changes .control');
    for(const button of target.querySelectorAll('button'))button.disabled=blocked()||draft.dirty&&button!==liveSave&&button!==liveCancel;
    target.querySelector('.permission-default select').disabled=blocked();liveCancel.disabled=draft.pending;liveSave.disabled=blocked()||conflicted;
    const changes=target.querySelector('.permission-changes'),conflict=changes.querySelector('.inline-error'),status=target.querySelector('.permission-status');
    changes.hidden=!draft.dirty;conflict.hidden=!conflicted;conflict.textContent=conflicted?tr('Permissions changed elsewhere. Cancel to load the latest policy before editing again.','权限已在其他位置变更，请取消以载入最新策略后重新编辑。'):'';
    status.textContent=draft.error||'';
  }
  reconcile(target,root);update();
}
