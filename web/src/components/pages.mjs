import {usageNumbers,usageRows,usageTooltip,usageTrend,createUsageList} from './usage.mjs';
import {createTaskConversation} from './task-conversation.mjs';
import {connectProviderEditor} from './provider-connection.mjs';
import {avatar} from './avatar.mjs';
import {translate} from '../locales/messages.mjs';
import {icon} from './icons.mjs';
import {node as n, action, field, section, table, details} from './primitives.mjs';
import {modelDefaults} from '../state/models.mjs';
import {ownedItems,combineTasks,combineUsage} from '../state/aggregate.mjs';
import {createEditorSession, createRequestGate} from '../state/editor.mjs';
import {createTaskTranscript,appendTaskEvents} from '../state/task-transcript.mjs';
import {configurePush,forgetBrowserAccess,configureBrowserAccess,requestPWAInstall} from '../adapters/access.mjs';
import {renderMarkdown} from './markdown.mjs';
import {renderAbout} from './about.mjs';
import {createRefreshQueue,relevantChange} from '../state/refresh.mjs';
import {reconcile,keyedRows,setDisabled,setPending} from './reconcile.mjs';
import {createProviderList,createTaskSettings} from './settings-lists.mjs';
import {renderMachineMembership} from './membership.mjs';
import {createIntegrations} from './integrations.mjs';
import {renderMachinePermissions} from './execution-permissions.mjs';
import {createApprovalCard} from './approval-card.mjs';
import {createChatApprovals} from './chat-approvals.mjs';
import {taskElapsed,bindTaskClock} from './task-time.mjs';

export function createPages({onApprovalsChanged=()=>{},client, host, approvalHost, chatApprovalHost, chatVisible, locale, notify, changed, titleChanged, openChat, routeChanged=()=>{}}) {
  const gate = createRequestGate(), approvalGate = createRequestGate();
  let route = '', title = '', editor = null, disposed = false, pageVersion = 0, configurationSpace = '',taskView=null,taskOrigin=null,taskListView=null,scheduleView=null,detailParent=false,usageOwner=null;
  let filter = {taskScope:'active', taskQuery:'', taskOffset:0, scheduleOffset:0, taskTab:'tasks', usageDays:7, usageScope:'all', usageSpace:'all',settingsTab:'general'};
  let pendingApprovals=[];
  const toolMemory=new Map(),usageOffsets=new Map(),usageCache=new Map(),approvalCards=new Map(),taskNames=new Map();
  const chatApprovals=createChatApprovals({host:chatApprovalHost,locale,visible:chatVisible});
  const cached = new Map(),spaceCache = new Map(),machineDrafts=new Map(),editorParents=[],taskDrafts=new Map(),taskDraftVersions=new Map(),taskWrites=new Map(),scheduleWrites=new Map();
  let pageView=null,currentViewKey='',viewReady=false,shortcutOpen=null,approvalOwner='';
  const tr = (en, zh) => locale() === 'zh-CN' ? zh : en;
  const scope = () => client.scope();
  const request = (path, options = {}) => client.request(path, options);
  const uid = () => crypto.randomUUID();
  const list = value => Array.isArray(value) ? value : value?.items || [];
  function captureView(){if(!editor&&viewReady&&currentViewKey&&host.offsetParent)cached.set(currentViewKey,{scroll:host.scrollTop,expanded:[...host.querySelectorAll('details[data-cache-key][open]')].map(node=>node.dataset.cacheKey),scrolls:[...host.querySelectorAll('[data-scroll-key]')].map(node=>({key:node.dataset.scrollKey,top:node.scrollTop,left:node.scrollLeft}))});}
  function restoreView(key,token){currentViewKey=key;viewReady=true;requestAnimationFrame(()=>{if(!gate.current(token)||key!==currentViewKey)return;const value=cached.get(key);if(!value){host.scrollTop=0;taskView?.ui.start();return;}host.scrollTop=value.scroll;for(const node of host.querySelectorAll('details[data-cache-key]'))if(node.dataset.cacheKey!=='keyboard-shortcuts')node.open=value.expanded.includes(node.dataset.cacheKey);for(const node of host.querySelectorAll('[data-scroll-key]')){const saved=value.scrolls?.find(item=>item.key===node.dataset.scrollKey);if(saved){node.scrollTop=saved.top;node.scrollLeft=saved.left;}}taskView?.ui.restore();});}
  async function across(path, token, {spaceID='all', paginate=false,perAssistant=false,ownerID='',errorTarget=null}={}) {
    const spaces=(client.spaces||[]).filter(space=>spaceID==='all'||space.id===spaceID);
    if(!spaces.length)throw Error(tr('Space is unavailable.','Space 不可用。'));
    const targets=spaces.flatMap(space=>perAssistant?(client.assistants||[]).filter(item=>item.space_id===space.id).map(item=>({space,owner:item.id})):[{space,owner:ownerID}]);
    const results=await Promise.allSettled(targets.map(async({space,owner})=>{
      const key=JSON.stringify([space.id,space.control_id,owner,path]);
      try {
        let value=await request(path,{owner,space:space.id,signal:token.signal});
        if(paginate&&value?.next_offset!=null) {
          let next=value.next_offset,seen=new Set();const items=[...(value.items||[])];
          while(next!=null&&items.length<5000) {if(seen.has(next))throw Error('Invalid pagination cursor');seen.add(next);const url=new URL(path,location.origin);url.searchParams.set('offset',next);const page=await request(url.pathname+url.search,{owner,space:space.id,signal:token.signal});items.push(...(page.items||[]));next=page.next_offset;}
          if(next!=null)throw Error(tr('Too many records. Narrow the filter.','记录过多，请缩小筛选范围。'));value={...value,items,next_offset:null};
        }

        if(gate.current(token))spaceCache.set(key,value);return {space,owner,value,stale:false};
      }catch(reason){if(reason.name==='AbortError')throw reason;const value=spaceCache.get(key);if(value)return {space,owner,value,stale:true,error:reason.message};throw Object.assign(reason,{space});}
    }));
    if(!gate.current(token))return [];
    const failed=results.filter(value=>value.status==='rejected');
    const pages=results.filter(value=>value.status==='fulfilled').map(value=>value.value);
    (errorTarget||host).querySelector('.space-availability')?.remove();
    if((failed.length||pages.some(page=>page.stale))&&!(errorTarget||host).querySelector('.space-availability')) {const message=n('p','inline-error space-availability',route==='usage'?tr('Some Spaces are unavailable; totals may be incomplete and cached rows are marked stale.','部分 Space 不可用，用量可能不完整，缓存项目标记为过期。'):tr('Some Spaces are unavailable. Cached rows are marked stale.','部分 Space 不可用，缓存项目标记为过期。'));(errorTarget||host.querySelector('.page-content'))?.prepend(message);}
    if(!pages.length&&targets.length)throw failed[0]?.reason||Error('No Space data');return pages;
  }
  function heading(value) {title=value; titleChanged();}
  function errorView(reason, target = host) {
    const box=n('div','inline-error');box.setAttribute('role','alert');box.append(n('span','',reason.message || String(reason)),action(tr('Retry','重试'),()=>show(route,{keepUsageOwner:!!usageOwner})));target.append(box);
  }
  function body() {const value=n('div','page-content');host.replaceChildren(value);return value;}
  function card(title,description) {const value=section(title,description);value.classList.add('page-card');return value;}
  function segments(entries,current,select) {const value=n('div','segments');for(const [key,label]of entries){const button=action(label,()=>select(key));button.classList.toggle('selected',key===current);button.setAttribute('aria-pressed',String(key===current));value.append(button);}return value;}
  function toolbar(...children) {const result=n('div','page-toolbar');result.append(...children);return result;}
  async function operation(button, work, after = () => show(route)) {
    const version=pageVersion;setPending(button,true);
    try {await work(); if(!disposed&&version===pageVersion&&button.isConnected) await after();}
    catch(reason) {if(!disposed&&version===pageVersion&&button.isConnected) errorView(reason);}
    finally {setPending(button,false);}
  }
  function connectView(root,token,read,update,{topics=[],space=''}={}) {
    const status=n('div','hint page-refresh-status',tr('Loading…','正在加载…'));status.setAttribute('role','status');root.prepend(status);
    const view={root,token,topics,space};pageView=view;
    const active=()=>!disposed&&pageView===view&&gate.current(token);
    view.queue=createRefreshQueue(async()=>{
      root.setAttribute('aria-busy','true');
      try{const value=await read();if(!active())return;await update(value);if(active()){status.hidden=true;status.replaceChildren();}}
      finally{if(active())root.setAttribute('aria-busy','false');}
    },{active,onError:reason=>{status.hidden=false;status.replaceChildren(n('span','',reason.message||String(reason)),action(tr('Retry','重试'),()=>view.queue.run()));status.className='inline-error page-refresh-status';}});
    return view.queue.run();
  }
  async function load(work) {
    captureView();viewReady=false;
    const token=gate.next(), root=body();root.append(n('p','hint',tr('Loading…','正在加载…')));
    try {const value=await work(token);if(gate.current(token)&&!disposed) {root.replaceChildren();return {root,value,token};}}
    catch(reason) {if(gate.current(token)&&!disposed) {root.replaceChildren();errorView(reason,root);}}
  }
  function empty(root, text) {root.append(n('p','page-empty',text));}
  async function confirmDiscard() {
    return new Promise(resolve=>{
      const dialog=n('dialog','preferences'), title=n('h2','',tr('Discard changes?','放弃更改？'));
      const actions=toolbar(action(tr('Keep editing','继续编辑'),()=>{dialog.close();resolve(false);}),action(tr('Discard changes','放弃更改'),()=>{dialog.close();resolve(true);},{danger:true}));
      dialog.append(title,n('p','hint',tr('Your unsaved changes will be lost.','未保存的更改将丢失。')),actions);document.body.append(dialog);
      dialog.addEventListener('cancel',event=>{event.preventDefault();dialog.close();resolve(false);},{once:true});dialog.addEventListener('close',()=>dialog.remove(),{once:true});dialog.showModal();
    });
  }
  async function leave({toParent=false}={}) {
    captureView();
    const sessions=[...(editor?[editor]:[]),...(!toParent?[...editorParents,...[...machineDrafts.values()].map(session=>({session}))]:[])];
    if(sessions.some(value=>value.session.pending)) {notify(tr('Please wait for the save result.','请等待保存结果。'));return false;}
    if(sessions.some(value=>value.session.dirty)&&!await confirmDiscard())return false;
    for(const value of sessions)value.lifecycle?.dispose?.();
    pageView?.queue.close();pageView=null;editor=null;if(!toParent){editorParents.length=0;machineDrafts.clear();}closeTaskView();taskOrigin=null;detailParent=false;closeTaskList();scheduleView=null;viewReady=false;gate.close();host.replaceChildren();return true;
  }
  function suspendEditor(){if(editor){editor.scroll=host.scrollTop;editor.focus=innerWidth>=720&&editor.root.contains(document.activeElement)?document.activeElement:null;editorParents.push(editor);editor=null;}}
  function renderEditor(label, initial, definitions, save, {returnChat=false,capturedScope,submitLabel=tr('Save','保存')}={}) {
    captureView();viewReady=false;
    pageView?.queue.close();pageView=null;closeTaskList();gate.close();
    const captured=capturedScope||scope(), caller=route || 'settings';
    const session=createEditorSession({...captured,route:caller,boot:client.runtime.boot_id},initial);
    const root=body(), form=n('form','editor-form'), fields=n('div','editor-fields'), footer=n('div','editor-actions'), status=n('p','inline-error');status.setAttribute('role','alert');
    const controls=new Map(),lifecycle={};
    for(const definition of definitions) {
      if(definition.section) {fields.append(section(definition.section));continue;}
      const {name,label,parse,presets,...options}=definition;
      const item=field(label,initial[name],options);controls.set(name,item.input);
      if(presets){const choices=toolbar(...presets.map(value=>action(value>=1000000?`${value/1000000}M`:`${value/1000}K`,()=>{item.input.value=value;item.input.dispatchEvent(new Event('input',{bubbles:true}));})));choices.classList.add('token-presets');item.row.lastChild.append(choices);}
      item.input.addEventListener('input',()=>session.set(name,parse?parse(item.input.value):options.kind==='checkbox'?item.input.checked:item.input.value));fields.append(item.row);
    }
    const cancel=action(tr('Cancel','取消'),()=>lifecycle.cancel?lifecycle.cancel():back());const submit=action(submitLabel,()=>form.requestSubmit(),{primary:true});
    footer.append(cancel,submit);form.append(fields,status,footer);root.append(form);
    editor={session,caller,returnChat,root,form,controls,lifecycle,label,returnUsageOwner:usageOwner?{...usageOwner}:null,id:uid()};
    history.pushState({...history.state,potEditor:editor.id},'',location.href);heading(label);
    form.onsubmit=async event=>{
      event.preventDefault();if(!form.reportValidity())return;
      const attempt=session.begin();if(!attempt)return;
      status.textContent='';for(const control of controls.values())control.disabled=true;cancel.disabled=submit.disabled=true;
      try {
        if(client.runtime.boot_id!==session.context.boot)throw Error(tr('Runtime identity changed. Reopen this editor.','运行时身份已变化，请重新打开编辑器。'));
        const saved=await save(attempt.values,{...captured,request_id:attempt.request_id},session);
        session.settle(true);changed(saved);if(lifecycle.afterSave)await lifecycle.afterSave(saved);else await back();
      } catch(reason) {session.settle(false);status.textContent=reason.message||String(reason);}
      finally {for(const control of controls.values())control.disabled=false;cancel.disabled=submit.disabled=false;lifecycle.settled?.();}
    };
    return {session,root,form,controls,lifecycle};
  }
  async function back({skipPicker=false}={}) {
    const current=editor,taskReturn=taskView?.returnTo||taskOrigin,returnToConversation=!editor&&usageOwner;
    if(!await leave({toParent:true}))return false;
    if(skipPicker&&editorParents.at(-1)?.kind==='picker')editorParents.pop();
    if(editorParents.length){editor=editorParents.pop();host.replaceChildren(editor.root);heading(editor.label);history.replaceState({...history.state,potEditor:editor.id},'',location.href);host.scrollTop=editor.scroll||0;editor.focus?.focus({preventScroll:true});return true;}
    if(returnToConversation&&usageOwner.returnTask){const task=usageOwner.returnTask,returnTo=usageOwner.taskReturn;usageOwner=null;route='tasks';routeChanged('tasks');const state={potRoute:'tasks',potScope:{space:task._space_id,owner:task.assistant_id},potTask:task,potTaskReturn:returnTo};history.replaceState(state,'',location.href);await taskDetail(task,{recordHistory:false,returnTo});return true;}
    if(!current&&taskReturn?.route==='chat'){const target=taskReturn.scope;usageOwner=null;detailParent=false;route='';history.replaceState({potRoute:'chat',potScope:target,potDirectory:false},'',location.href);await openChat(target);return true;}
    if(returnToConversation){usageOwner=null;const state={...history.state,potRoute:'chat'};delete state.potUsage;history.replaceState(state,'',location.href);openChat();return true;}
    if(current?.returnSchedule){const state={...history.state};delete state.potEditor;history.replaceState(state,'',location.href);await scheduleHistory(current.returnSchedule,false);return true;}
    const state={...history.state};for(const key of ['potEditor','potTask','potTaskReturn','potSchedule','potResource','potUsage'])delete state[key];state.potRoute=current?.returnChat||returnToConversation?'chat':current?.caller||route||'settings';history.replaceState(state,'',location.href);
    if(current?.returnChat)openChat();else{usageOwner=current?.returnUsageOwner||usageOwner;show(current?.caller||route||'settings',{keepUsageOwner:!!usageOwner});}return true;
  }
  async function assistant(definition) {
    if(!await leave())return;
    const token=gate.next(),captured=scope(),loading=body();loading.append(n('p','hint',tr('Loading…','正在加载…')));
    try{
      const [providers,thread,rules]=await Promise.all([request('/api/v1/providers',captured),definition?request('/api/v1/agent',captured):null,definition?request('/api/v1/instructions',captured):null]);
      if(!gate.current(token)||disposed)return;
      let profiles=providers.profiles||[],revision=definition?.revision,generation=thread?.generation||0,configurationRevision=thread?.revision||0,rulesRevision=rules?.revision||'';
      const selection=thread?.selection||{},profileID=selection.profile_id||thread?.config?.source_profile_id||providers.default_provider_id||'',profile=profiles.find(value=>value.id===profileID);
      const initial={space_id:captured.space,name:definition?.name||'Pot',profile_id:profileID,model:selection.model||thread?.config?.model||profile?.model||'',model_selection:'',context_window:selection.context_window??null,max_output_tokens:selection.max_output_tokens??null,reasoning_effort:selection.reasoning_effort||'',text:rules?.text||''};
      const selectValue=values=>{const profile=profiles.find(value=>value.id===values.profile_id);return profile?{profile_id:profile.id,profile_revision:profile.revision,model:values.model,context_window:values.context_window||null,max_output_tokens:values.max_output_tokens||null,reasoning_effort:values.reasoning_effort.trim()||null}:null;};
      let initialSelection=JSON.stringify({...selectValue(initial),profile_revision:selection.profile_revision||profile?.revision});
      const form=renderEditor(definition?tr('Assistant settings','助手设置'):tr('New assistant','新建助手'),initial,[
        ...(!definition&&client.desktop?[{name:'space_id',label:tr('Location','位置'),choices:(client.spaces||[]).map(space=>[space.id,space.name])}]:[]),
        {name:'name',label:tr('Name','名称'),required:true},
        {name:'profile_id',label:tr('Provider','提供商'),required:true,choices:[['',tr('Select a Provider','选择提供商')],...profiles.map(value=>[value.id,value.name])]},
        {name:'model_selection',label:tr('Model','模型'),choices:[['',tr('Enter model ID','输入模型 ID')]]},
        {name:'model',label:tr('Model ID','模型 ID')},
        {name:'context_window',label:tr('Context window','上下文窗口'),kind:'number',parse:value=>value?Number(value):null,min:1,presets:[32000,64000,128000,200000,1000000]},
        {name:'max_output_tokens',label:tr('Maximum output tokens','最大输出 token'),kind:'number',parse:value=>value?Number(value):null,min:1,presets:[4000,8000,16000,32000]},
        {name:'reasoning_effort',label:tr('Reasoning effort','推理强度')},
        {name:'text',label:tr('Instructions','指令'),kind:'textarea',help:tr('Rules are saved for this assistant.','指令仅保存到此助手。')},
      ],async(values,context,session)=>{
        const options={owner:captured.owner,space:captured.space,method:'POST'},selection=selectValue(values);
        if(!selection)throw Error(tr('Connect and select a Provider first.','请先连接并选择提供商。'));
        preview.replaceWith(preview=avatar(56,definition?.request_id||definition?.id||context.request_id));
        if(!definition){const saved=await request('/api/v1/assistants',{...options,space:values.space_id,owner:'',body:{request_id:context.request_id,name:values.name,text:values.text,locale:locale(),...(selection?{selection}:{})}});return {assistant:{...saved,space_id:values.space_id}};}
        if(values.name!==initial.name){const saved=await request(`/api/v1/assistants/${encodeURIComponent(definition.id)}`,{...options,body:{name:values.name,revision}});revision=saved.revision;initial.name=values.name;session.acknowledge({name:values.name});}
        const signature=JSON.stringify(selection);
        if(selection&&signature!==initialSelection){const saved=await request('/api/v1/agent',{...options,body:{request_id:context.request_id,expected_generation:generation,expected_revision:configurationRevision,selection}});generation=saved.generation;configurationRevision=saved.revision;initialSelection=signature;session.acknowledge(Object.fromEntries(['profile_id','model','model_selection','context_window','max_output_tokens','reasoning_effort'].map(key=>[key,values[key]])));}
        if(values.text!==initial.text){const saved=await request('/api/v1/instructions',{...options,body:{text:values.text,revision:rulesRevision}});rulesRevision=saved.revision;initial.text=values.text;session.acknowledge({text:values.text});}
      },{returnChat:true,submitLabel:definition?tr('Save','保存'):tr('Create and start chatting','创建并开始聊天')});
      const controls=form.controls,session=form.session;
      editor.kind='assistant';
      let preview=avatar(56,definition?.request_id||definition?.id||session.requestID);
      form.form.querySelector('.editor-fields').prepend(preview);
      const set=(name,value)=>{controls.get(name).value=value??'';session.set(name,value);};
      const defaults=()=>{const profile=profiles.find(value=>value.id===session.values.profile_id),catalog=profile?.model_settings?.catalog_provider||'custom',limits=modelDefaults(providers,catalog,session.values.model);const same=profile?.model===session.values.model;controls.get('context_window').placeholder=String(same?(profile?.model_settings?.context_window||limits.context):limits.context);controls.get('max_output_tokens').placeholder=String(same?(profile?.model_settings?.max_output_tokens||limits.output):limits.output);controls.get('reasoning_effort').placeholder=profile?.params?.reasoning_effort||tr('Provider default','提供商默认值');controls.get('model').required=!!profile;form.form.querySelector('.editor-actions .primary').disabled=!profile;};
      const updateModels=()=>{const profile=profiles.find(value=>value.id===session.values.profile_id),catalog=(providers.directory||[]).find(value=>value.id===profile?.model_settings?.catalog_provider);const options=new Map();if(profile?.model)options.set(profile.model,profile.model);for(const model of catalog?.models||[])options.set(model.id,model.name||model.id);for(const model of providers.configured_models?.[profile?.id]||profile?.models||[])options.set(typeof model==='string'?model:model.id,typeof model==='string'?model:model.name||model.id);const input=controls.get('model_selection');input.replaceChildren();for(const [value,label]of [['',tr('Enter model ID','输入模型 ID')],...options]){const option=n('option','',label);option.value=value;input.append(option);}set('model_selection',options.has(session.values.model)?session.values.model:'');session.acknowledge({model_selection:session.values.model_selection});controls.get('model').closest('.form-row').hidden=!!input.value;defaults();};
      controls.get('profile_id').addEventListener('change',()=>{const selected=profiles.find(value=>value.id===session.values.profile_id);set('model',selected?.model||'');updateModels();});
      controls.get('model_selection').addEventListener('change',()=>{if(controls.get('model_selection').value)set('model',controls.get('model_selection').value);controls.get('model').closest('.form-row').hidden=!!controls.get('model_selection').value;defaults();});controls.get('model').addEventListener('input',defaults);updateModels();
      const instructionsRow=controls.get('text').closest('.form-row'),parameters=n('details','editor-disclosure');parameters.dataset.section='parameters';parameters.append(n('summary','',tr('Model parameters','模型参数')));for(const name of ['context_window','max_output_tokens','reasoning_effort'])parameters.append(controls.get(name).closest('.form-row'));instructionsRow.before(parameters);
      const instructions=n('details','editor-disclosure');instructions.dataset.section='instructions';instructions.append(n('summary','',tr('Instructions','指令')));instructionsRow.before(instructions);instructions.append(instructionsRow);instructionsRow.querySelector('.form-label').textContent='AGENTS.md';
      const providerHelp=n('p','hint',tr('Connect a Provider before creating an assistant.','请先连接提供商，再创建助手。'));form.form.querySelector('.editor-fields').prepend(providerHelp);providerHelp.hidden=profiles.length>0;
      const addProvider=action(tr('+ Add Provider','＋ 添加提供商'),()=>providerPicker(providers,{space:session.values.space_id||captured.space,owner:''}));controls.get('profile_id').closest('.field-body').append(addProvider);
      form.lifecycle.providersChanged=async saved=>{const value=await request('/api/v1/providers',{space:session.values.space_id||captured.space,owner:''});if(editor?.session!==session)return;profiles=value.profiles||[];Object.assign(providers,value);providerHelp.hidden=profiles.length>0;const input=controls.get('profile_id');input.replaceChildren();for(const profile of profiles){const option=n('option','',profile.name);option.value=profile.id;input.append(option);}const selected=profiles.find(profile=>profile.id===saved?.id)||profiles.find(profile=>profile.id===session.values.profile_id)||profiles[0];const changedProvider=selected?.id!==session.values.profile_id;set('profile_id',selected?.id||'');if(changedProvider||!session.values.model)set('model',selected?.model||'');updateModels();};
      form.lifecycle.settled=defaults;
      if(thread?.pending)form.root.prepend(n('p','hint',tr('Saved settings will apply to the next new turn. The current turn keeps its original configuration.','已保存的设置将在下一次新回合生效，当前回合保持原配置。')));
      if(!definition&&controls.has('space_id'))controls.get('space_id').addEventListener('change',async()=>{const next=controls.get('space_id').value;controls.get('profile_id').disabled=true;try{const snapshot=await request('/api/v1/providers',{space:next,owner:''});if(editor?.session!==session||controls.get('space_id').value!==next)return;profiles=snapshot.profiles||[];Object.assign(providers,snapshot);providerHelp.hidden=profiles.length>0;const input=controls.get('profile_id');input.replaceChildren();for(const profile of profiles){const option=n('option','',profile.name);option.value=profile.id;input.append(option);}set('profile_id',snapshot.default_provider_id||'');set('model',profiles.find(value=>value.id===session.values.profile_id)?.model||'');updateModels();}catch(reason){if(editor?.session===session)errorView(reason,form.root);}finally{if(editor?.session===session)controls.get('profile_id').disabled=false;}});
    }catch(reason){if(gate.current(token))errorView(reason);}
  }
  async function settings(root, token) {
    root.classList.add('settings-content');root.append(segments([['general',tr('General','通用')],['providers',tr('AI Providers','AI 提供商')],['integrations',translate(locale(),'integrations.title')],['tasks',tr('Task Configuration','任务配置')]],filter.settingsTab,value=>{filter.settingsTab=value;show('settings');}));
    if(filter.settingsTab==='general'){
    const preferences=card(tr('General','通用'));
    const preference=(key,value)=>{try{localStorage.setItem(key,value);}catch{}window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key,value}}));};
    const language=field(tr('Language','语言'),locale(),{choices:[['en','English'],['zh-CN','简体中文']]});language.row.classList.add('preference-setting');language.input.onchange=()=>preference('pot-go-preview-locale',language.input.value);preferences.append(language.row);
    const appearance=n('div','preference-setting appearance-setting');appearance.append(n('span','',tr('Appearance','外观')),segments([['system',tr('System','跟随系统')],['light',tr('Light','浅色')],['dark',tr('Dark','深色')]],document.documentElement.dataset.theme||'system',value=>preference('pot-go-preview-theme',value)));preferences.append(appearance);root.append(preferences);

    const shortcuts=n('details','page-card shortcut-card');shortcuts.dataset.cacheKey='keyboard-shortcuts';shortcuts.open=shortcutOpen??innerWidth>=720;const shortcutSummary=n('summary','',tr('Keyboard shortcuts','快捷键'));shortcutSummary.onclick=()=>{shortcutOpen=!shortcuts.open;};shortcuts.append(shortcutSummary);for(const [label,key]of [[tr('Parent page','父页面'),'⌘ ['],[tr('Machines','机器'),'⌘ D'],[tr('Settings','设置'),'⌘ ,'],[tr('New line','换行'),'⇧ ↵']]){const row=n('div','preference-row');row.append(n('span','hint',label),n('kbd','',key));shortcuts.append(row);}root.append(shortcuts);
    if(!client.desktop&&client.access){const access=n('section','page-card browser-actions');access.append(action(tr('Add to Home Screen','添加到主屏幕'),async()=>{if(!await requestPWAInstall())access.append(n('p','hint',tr('Use your browser menu to install Pot or add it to the Home Screen.','通过浏览器菜单安装 Pot 或添加到主屏幕。')));}));if(client.access.push_available){for(const enabled of [true,false]){const button=action(enabled?tr('Enable message notifications','启用消息通知'):tr('Disable message notifications','停用消息通知'),()=>operation(button,()=>configurePush(client.access,{enabled,locale:locale()}),()=>{}));access.append(button);}}access.append(action(tr('Change access token','更改访问令牌'),()=>renderEditor(tr('Browser access','浏览器访问'),{token:''},[{name:'token',label:tr('Access token','访问令牌'),kind:'password',required:true}],values=>configureBrowserAccess(values.token))),action(tr('Disconnect this browser','断开此浏览器'),()=>forgetBrowserAccess()));root.append(access);}

    await renderAbout({client,root,scope:{owner:'',space:client.desktop?(client.spaces||[]).find(space=>space.kind==='local')?.id||'':scope().space},locale});return;
    }
    const locations=client.spaces||[];if(!configurationSpace)configurationSpace=scope().space;
    const settingsScope={space:configurationSpace,owner:''};
    if(client.desktop&&locations.length>1){const location=field(tr('Configuration location','配置位置'),configurationSpace,{choices:locations.map(space=>[space.id,space.name])});location.input.onchange=()=>{configurationSpace=location.input.value;show('settings');};root.append(location.row);}
    const current=()=>gate.current(token)&&!disposed,refresh=()=>pageView?.queue.run(),options={root,tr,request,scope:settingsScope,current,refresh};
    if(filter.settingsTab==='integrations'){
      const update=createIntegrations({root,request,scope:settingsScope,signal:token.signal,locale,openExternal:client.openExternal,refresh});
      return connectView(root,token,()=>request('/api/v1/integrations',{...settingsScope,signal:token.signal}),update,{topics:['integrations','spaces'],space:settingsScope.space});
    }
    if(filter.settingsTab==='tasks'){
      const update=createTaskSettings({root,tr,configure:(profile,policy)=>routingEditor(profile,policy,settingsScope),allowance:quota=>providerPolicyEditor(quota,settingsScope)});
      return connectView(root,token,async()=>{const [providers,policy,allowances]=await Promise.all([request('/api/v1/providers',{...settingsScope,signal:token.signal}),request('/api/v1/agent/routing',{...settingsScope,signal:token.signal}),request('/api/v1/usage/providers',{...settingsScope,signal:token.signal})]);return {providers,policy,allowances};},update,{topics:['providers','settings','usage','spaces'],space:settingsScope.space});
    }
    const update=createProviderList({...options,pick:providers=>providerPicker(providers,settingsScope),edit:(profile,providers)=>providerEditor(profile,providers,settingsScope),spaceEditor});
    return connectView(root,token,async()=>{const [providers,spaces]=await Promise.all([request('/api/v1/providers',{...settingsScope,signal:token.signal}),client.desktop?request('/api/v1/spaces',{signal:token.signal,owner:'',space:''}):null]);return {providers,spaces};},update,{topics:['providers','spaces'],space:settingsScope.space});
  }
  function providerPolicyEditor(quota,owner) {
    renderEditor(tr('Provider allowance','提供商配额'),quota.policy,[
      {name:'period',label:tr('Period','周期'),choices:[['none',tr('Unlimited','无限制')],['day',tr('Daily','每日')],['week',tr('Weekly','每周')]]},
      {name:'limit',label:tr('Token limit','Token 上限'),kind:'number',parse:Number,required:true,min:1,max:Number.MAX_SAFE_INTEGER},
      {name:'timezone',label:tr('Time zone','时区'),required:true},{name:'auto_resume',label:tr('Automatically resume','自动恢复'),kind:'checkbox'},
    ],(values,context)=>request('/api/v1/usage/providers',{...context,method:'POST',body:{provider:quota.provider,revision:quota.revision,policy:values}}),{capturedScope:owner});
  }
  function routingEditor(profile,policy,owner) {
    renderEditor(profile.name,{profile_limit:policy.profile_limits?.[profile.id]??null,provider_limit:policy.provider_limits?.[profile.provider]??null},[
      {name:'profile_limit',label:tr('Model concurrency','模型并发数'),kind:'number',parse:value=>value?Number(value):null,min:1,max:1000,help:tr('Leave blank for no additional limit.','留空表示不另设上限。')},
      {name:'provider_limit',label:tr('Provider concurrency','提供商并发数'),kind:'number',parse:value=>value?Number(value):null,min:1,max:1000},
    ],(values,context)=>{const next=structuredClone(policy);next.profile_limits||={};next.provider_limits||={};for(const [key,id,value]of [['profile_limits',profile.id,values.profile_limit],['provider_limits',profile.provider,values.provider_limit]]){if(value)next[key][id]=value;else delete next[key][id];}return request('/api/v1/agent/routing',{...context,method:'POST',body:next});},{capturedScope:owner});
  }
  async function setupAI() {
    const captured={...scope(),owner:''};
    const snapshot=await request('/api/v1/providers',captured);
    await providerPicker(snapshot,captured);
    if(editor?.kind==='picker')editor.onboarding=true;
  }
  async function providerPicker(snapshot,captured) {
    if(editor?.kind==='assistant')suspendEditor();else if(!await leave())return;
    const form=renderEditor(tr('Add Provider','添加提供商'),{search:''},[{name:'search',label:tr('Search providers','搜索提供商'),kind:'search'}],()=>{}, {capturedScope:captured});
    editor.kind='picker';
    form.form.querySelector('.editor-actions .primary').remove();const grid=n('div','provider-grid'),hint=n('p','hint',tr('Choose the AI you want to use.','选择你要使用的 AI。'));form.form.querySelector('.editor-fields').prepend(hint);form.form.querySelector('.editor-fields').append(grid);let more=false;
    const render=()=>{const query=form.controls.get('search').value.trim().toLowerCase();form.session.acknowledge({search:form.session.values.search});grid.replaceChildren();for(const provider of (snapshot.directory||[]).filter(value=>query?`${value.name} ${value.id}`.toLowerCase().includes(query):more||value.common)){
      const choice=action(provider.name,()=>providerEditor(null,snapshot,captured,provider.id));choice.classList.add('provider-choice');if(provider.logo?.startsWith('<svg')){const image=n('img','provider-logo');image.alt='';image.src=`data:image/svg+xml;charset=utf-8,${encodeURIComponent(provider.logo)}`;choice.prepend(image);}grid.append(choice);
    }if(!query||'custom api 自定义'.includes(query)){const custom=action(tr('Custom API','自定义 API'),()=>providerEditor(null,snapshot,captured,'custom'));custom.classList.add('provider-choice');const gear=icon('settings');gear.classList.add('provider-logo');custom.prepend(gear);grid.append(custom);}};
    const toggle=action(tr('More providers…','更多提供商…'),()=>{more=!more;toggle.textContent=more?tr('Fewer providers','收起提供商'):tr('More providers…','更多提供商…');render();});form.form.querySelector('.editor-fields').append(toggle);form.controls.get('search').addEventListener('input',render);render();
  }
  async function providerEditor(profile, snapshot, settingsScope, providerOverride) {
    const onboarding=(editor?.kind==='picker'&&editor.onboarding)||(!(client.assistants||[]).length&&!editorParents.some(parent=>parent.kind==='assistant'));
    if(editor?.kind==='picker')suspendEditor();else if(!await leave())return;
    const connection=(snapshot.connections||[]).find(c=>c.profile_id===profile?.id);
    const providerID=providerOverride||profile?.model_settings?.catalog_provider||'custom';
    const selectedEntry=(snapshot.directory||[]).find(value=>value.id===providerID);
    const inherited=modelDefaults(snapshot,providerID,profile?.model||'');
    let configuredModels=[...(snapshot.configured_models?.[profile?.id]||[])];
    const baseName=selectedEntry?.name||tr('Custom Provider','自定义供应商'),names=new Set((snapshot.profiles||[]).map(value=>value.name));let defaultName=baseName;for(let index=2;names.has(defaultName);index++)defaultName=`${baseName} ${index}`;
    const initial={name:profile?.name||defaultName,endpoint:connection?.endpoint||selectedEntry?.endpoint||'',api_key:'',model:profile?.model||'',model_selection:'',protocol:connection?.protocol||selectedEntry?.protocol||'openai_responses',context_window:profile?.model_settings?.context_window??null,max_output_tokens:profile?.model_settings?.max_output_tokens??null,reasoning_effort:profile?.params?.reasoning_effort||''};
    const form=renderEditor(profile?tr('Edit Provider','编辑提供商'):tr('Add Provider','添加提供商'),initial,[
      {name:'name',label:tr('Name','名称'),required:true},
      {name:'endpoint',label:tr('Endpoint','API 地址'),kind:'url',required:true,help:tr('Base URL or complete request URL.','基础地址或完整请求地址。')},
      {name:'api_key',label:tr('API key','API 密钥'),kind:'password',required:!profile,help:profile?tr('Leave blank to keep the saved key.','留空保留现有密钥。'):''},
      {name:'model_selection',label:tr('Model','默认模型'),choices:[['',tr('Enter model ID','输入模型 ID')]]},
      {name:'model',label:tr('Model ID','模型 ID'),required:true},
      {name:'protocol',label:tr('Protocol','请求格式'),choices:[['openai_chat','OpenAI Chat Completions'],['openai_responses','OpenAI Responses'],['anthropic_messages','Anthropic Messages'],['gemini','Google Gemini']]},
      {name:'context_window',label:tr('Context window','上下文窗口'),kind:'number',parse:value=>value?Number(value):null,min:1,placeholder:String(inherited.context),presets:[128000,200000,256000,400000,512000,1000000]},
      {name:'max_output_tokens',label:tr('Maximum output tokens','最大输出 token'),kind:'number',parse:value=>value?Number(value):null,min:1,placeholder:String(inherited.output),presets:[8000,16000,32000,64000,128000]},
      {name:'reasoning_effort',label:tr('Reasoning effort','推理强度'),placeholder:tr('Provider default','提供商默认值')},
    ],(values,context)=>{
      const {model_selection,context_window,...connectionValues}=values;
      return request('/api/v1/providers',{...context,method:'POST',body:{request_id:context.request_id,profile_id:profile?.id||'',expected_revision:profile?.revision||0,models:[...new Set([...configuredModels,values.model].filter(Boolean))],...connectionValues,reasoning_effort:values.reasoning_effort.trim()||null,max_output_tokens:values.max_output_tokens||modelDefaults(snapshot,providerID,values.model).output,model_settings:{catalog_provider:providerID==='custom'?null:providerID,context_window:context_window||null,max_output_tokens:values.max_output_tokens||null}}});
    },{capturedScope:settingsScope});
    const controls=form.controls,session=form.session;let endpointEdited=false;
    form.lifecycle.cancel=()=>back({skipPicker:true});form.lifecycle.afterSave=async saved=>{await back({skipPicker:true});await editor?.lifecycle.providersChanged?.(saved);if(onboarding){openChat();changed(saved);}};
    const set=(name,value)=>{controls.get(name).value=value??'';session.set(name,value);};
    const showModel=()=>{controls.get('model').closest('.form-row').hidden=!!controls.get('model_selection').value;};
    const inherit=()=>{const limits=modelDefaults(snapshot,providerID,session.values.model);controls.get('context_window').placeholder=String(limits.context);controls.get('max_output_tokens').placeholder=String(limits.output);};
    const models=(entries,selected='')=>{const input=controls.get('model_selection');input.replaceChildren();for(const [value,label]of [['',tr('Enter model ID','输入模型 ID')],...entries.map(model=>[typeof model==='string'?model:model.id,typeof model==='string'?model:model.name||model.id])]){const option=n('option','',label);option.value=value;input.append(option);}set('model_selection',entries.some(model=>(typeof model==='string'?model:model.id)===selected)?selected:'');session.acknowledge({model_selection:session.values.model_selection});showModel();};
    models([...new Map([...(selectedEntry?.models||[]),...configuredModels,...(profile?.model?[profile.model]:[])].map(model=>[typeof model==='string'?model:model.id,model])).values()],initial.model);
    const keyURL={openai:'https://platform.openai.com/api-keys',anthropic:'https://platform.claude.com/settings/keys',google:'https://aistudio.google.com/api-keys',deepseek:'https://platform.deepseek.com/api_keys',opencode:'https://opencode.ai/auth','opencode-go':'https://opencode.ai/auth'}[providerID];
    if(keyURL){const link=n('a','provider-key-link',tr('Get API key ↗','获取 API Key ↗'));link.href=keyURL;link.target='_blank';link.rel='noopener noreferrer';if(client.openExternal)link.onclick=event=>{event.preventDefault();client.openExternal(keyURL);};controls.get('api_key').closest('.field-body').append(link);}
    controls.get('model_selection').addEventListener('change',()=>{if(controls.get('model_selection').value){set('model',controls.get('model_selection').value);const model=selectedEntry?.models?.find(model=>model.id===session.values.model);if(model&&!endpointEdited){if(model.endpoint)set('endpoint',model.endpoint);if(model.protocol)set('protocol',model.protocol);}}showModel();inherit();});
    controls.get('endpoint').addEventListener('input',()=>{endpointEdited=true;});
    controls.get('protocol').addEventListener('change',()=>{endpointEdited=true;});
    controls.get('model').addEventListener('input',inherit);inherit();
    connectProviderEditor({form,request,scope:settingsScope,profile,connection,tr,current:()=>editor?.session===session&&!disposed&&client.runtime.boot_id===session.context.boot,models:entries=>{configuredModels=entries.map(model=>typeof model==='string'?model:model.id);models([...new Map([...(selectedEntry?.models||[]),...configuredModels].map(model=>[typeof model==='string'?model:model.id,model])).values()],controls.get('model_selection').value);}});

  }
  function spaceEditor() {
    renderEditor(tr('Connect Space','连接 Space'),{name:'',endpoint:'',token:'',ca_pem:''},[
      {name:'name',label:tr('Name','名称'),required:true},{name:'endpoint',label:tr('Endpoint','端点'),kind:'url',required:true},{name:'token',label:tr('Access token','访问令牌'),kind:'password',required:true},{name:'ca_pem',label:tr('Trusted Control CA (optional)','受信任的 Control CA（可选）'),kind:'textarea'},
    ],(values,context)=>request('/api/v1/spaces',{method:'POST',owner:'',space:'',body:{action:'connect',request_id:context.request_id,...values}}));
  }
  async function tasks(root,token) {
    root.classList.add('tasks-content');
    root.append(segments([['tasks',tr('Tasks','任务')],['schedules',tr('Schedules','定时计划')]],filter.taskTab,value=>{filter.taskTab=value;show('tasks');}));
    if(filter.taskTab==='schedules')return schedules(root,token);
    const controls=toolbar();controls.classList.add('task-filters');
    const scopes=segments(['active','attention','archived'].map(value=>[value,tr({active:'Ongoing',attention:'Needs attention',archived:'Archived'}[value],{active:'进行中',attention:'需关注',archived:'已归档'}[value])]),filter.taskScope,value=>{filter.taskScope=value;filter.taskOffset=0;show('tasks');});controls.append(scopes);
    const search=field(tr('Search task title or ID…','搜索任务标题或 ID…'),filter.taskQuery,{kind:'search'});search.row.classList.add('task-search');search.input.placeholder=tr('Search task title or ID…','搜索任务标题或 ID…');search.input.onchange=()=>{filter.taskQuery=search.input.value;filter.taskOffset=0;show('tasks');};controls.append(search.row);root.append(controls);
    const rows=n('div','task-list');rows.dataset.scrollKey='task-list';rows.append(n('p','hint',tr('Loading…','正在加载…')));root.append(rows);
    const summary=n('span','hint'),previous=action(tr('Previous','上一页'),()=>{filter.taskOffset=Math.max(0,filter.taskOffset-20);show('tasks');},{disabled:!filter.taskOffset});
    const next=action(tr('Next','下一页'),()=>{filter.taskOffset=taskListView.result.next_offset;show('tasks');},{disabled:true});
    const footer=toolbar(summary,previous,next);footer.classList.add('pagination');root.append(footer);
    const view={token,rows,scopes,summary,previous,next,nodes:new Map(),result:null};view.queue=createRefreshQueue(()=>readTaskList(view),{active:()=>!disposed&&gate.current(token)&&taskListView===view});taskListView=view;
    view.clock=bindTaskClock(rows,{locale,active:()=>!disposed&&gate.current(token)&&taskListView===view&&!!rows.offsetParent});
    await refreshTaskList(view);
  }
  function refreshTaskList(view=taskListView){return view?.queue.run();}
  async function readTaskList(view) {
    try {
      const pages=await across(`/api/v1/tasks/query?${new URLSearchParams({scope:filter.taskScope,q:filter.taskQuery,offset:0,limit:50})}`,view.token,{paginate:true,perAssistant:true});
      if(!gate.current(view.token)||taskListView!==view)return;
      const result=combineTasks(pages,{offset:filter.taskOffset,limit:20});rememberTasks(result.items);
      if(!view.result)view.rows.replaceChildren();view.result=result;
      const text=(node,value)=>{if(node.textContent!==value)node.textContent=value;};
      for(const [index,value]of ['active','attention','archived'].entries())text(view.scopes.children[index],`${tr({active:'Ongoing',attention:'Needs attention',archived:'Archived'}[value],{active:'进行中',attention:'需关注',archived:'已归档'}[value])} ${result.counts[value]}`);
      const ordered=[],keys=new Set();let groupKey='';
      const retain=(key,create)=>{keys.add(key);let entry=view.nodes.get(key);if(!entry){entry=create();view.nodes.set(key,entry);}ordered.push(entry.row);return entry;};
      for(const task of result.items){
        const ownerKey=JSON.stringify([task._space_id,task._assistant_id]),taskKey=JSON.stringify([task._space_id,task._assistant_id,task.id]);
        if(ownerKey!==groupKey){groupKey=ownerKey;const assistant=(client.assistants||[]).find(item=>item.id===task._assistant_id&&item.space_id===task._space_id);text(retain('owner:'+taskKey,()=>({row:n('div','task-owner')})).row,assistant?.name||task._assistant_id);}
        const entry=retain(taskKey,()=>{
          const row=n('button','task-row');row.type='button';const line=n('span','task-title-line'),logo=n('span','task-brand'),title=n('span','task-title'),elapsed=n('span','task-list-elapsed'),chevron=n('span','hint task-chevron','›'),detail=n('span','task-meta'),phase=n('span'),meta=n('span','hint');
          elapsed.setAttribute('role','timer');elapsed.setAttribute('aria-live','off');
          line.append(logo,title);detail.append(phase,meta);row.append(line,elapsed,chevron,detail);return {row,logo,title,phase,meta,elapsed};
        });
        entry.row.setAttribute('aria-label',task.title);entry.row.disabled=task._stale;entry.row.title=task.title;entry.row.onclick=()=>taskDetail(task);
        text(entry.logo,task.executor_icon==='anthropic'?'A':'◉');text(entry.title,task.title);entry.phase.className=`task-phase phase-${task.phase}`;
        text(entry.phase,taskPhaseLabel(task.phase));
        const time=['queued','running','waiting','waiting_approval'].includes(task.phase)?taskElapsed(task.started_at_ms,Date.now(),locale()):'';
        entry.elapsed.hidden=!time;text(entry.elapsed,time);
        entry.elapsed.id=`task-elapsed-${encodeURIComponent(taskKey)}`;
        if(time)entry.row.setAttribute('aria-describedby',entry.elapsed.id);else entry.row.removeAttribute('aria-describedby');
        if(time)entry.elapsed.dataset.startedAt=task.started_at_ms;else delete entry.elapsed.dataset.startedAt;
        entry.elapsed.title=tr('Total time since execution first started, including waits','自首次开始执行起的总耗时，包含中间等待');
        text(entry.meta,`${task._space_name||''}${task.machine_id?' · '+task.machine_id:''}${task.pending?' · '+task.pending+' '+tr('pending instructions','条待处理指令'):''}${task._stale?' · '+tr('Stale','已过期'):''}`);
      }
      if(!result.items.length)text(retain('empty',()=>({row:n('p','page-empty')})).row,tr('No ongoing tasks. Describe a goal in Chat to get started.','暂无进行中的任务，在聊天中描述目标即可开始。'));
      const scroll=view.rows.scrollTop,focused=view.rows.contains(document.activeElement)?document.activeElement:null;
      for(const [key,entry]of view.nodes)if(!keys.has(key)){entry.row.remove();view.nodes.delete(key);}
      for(const [index,row]of ordered.entries())if(view.rows.children[index]!==row)view.rows.insertBefore(row,view.rows.children[index]||null);
      if(focused?.isConnected&&document.activeElement!==focused)focused.focus({preventScroll:true});
      view.rows.scrollTop=scroll;
      text(view.summary,`${result.total} ${tr('items','项')} · ${tr('Page','第')} ${Math.floor(filter.taskOffset/20)+1}`);view.previous.disabled=!filter.taskOffset;view.next.disabled=result.next_offset==null;
      view.clock.refresh();
    }catch(reason){if(gate.current(view.token)&&reason.name!=='AbortError'){if(!view.result){view.rows.replaceChildren();errorView(reason,view.rows);}else notify(reason.message);}}

  }
  function closeTaskList(){taskListView?.queue.close();taskListView?.clock.dispose();taskListView=null;}
  function closeTaskView(){taskView?.eventsQueue?.close();taskView?.ui?.dispose();taskView=null;host.classList.remove('task-page-host');}
  async function taskDetail(task,{recordHistory=true,returnTo=taskView?.returnTo||taskOrigin||{route:'tasks'}}={}) {
    rememberTasks([task]);taskOrigin=returnTo;captureView();pageView?.queue.close();pageView=null;closeTaskList();closeTaskView();scheduleView=null;detailParent=true;
    if(recordHistory){const replace=history.state?.potTask?.id===task.id&&history.state?.potScope?.space===task._space_id;history[replace?'replaceState':'pushState']({potRoute:'tasks',potScope:{space:task._space_id,owner:task.assistant_id},potTask:{...task},potTaskReturn:returnTo},'',location.href);}
    const path=`/api/v1/tasks/${encodeURIComponent(task.id)}`,fileScope={owner:task.assistant_id,space:task._space_id};
    const result=await load(token=>request(path,{...fileScope,signal:token.signal}));if(!result)return;
    const {root,token}=result,record=result.value;host.classList.add('task-page-host');
    const draftKey=JSON.stringify([task._space_id,task.assistant_id,task.id]);
    const view={task,record,token,returnTo,scope:fileScope,state:createTaskTranscript(),inputs:[],busy:false,again:false,unavailable:false};
    const current=()=>gate.current(token)&&taskView===view;
    const mutate=async(suffix,body)=>{if(!current())return;await request(path+suffix,{...fileScope,method:'POST',body});if(current())await refreshTaskEvents(view);};
    const ui=createTaskConversation({root,task,record,locale,client,scope:fileScope,draft:taskDrafts.get(draftKey)||'',onDraft:text=>{taskDraftVersions.set(draftKey,(taskDraftVersions.get(draftKey)||0)+1);if(text)taskDrafts.set(draftKey,text);else taskDrafts.delete(draftKey);},
      assistantName:(client.assistants||[]).find(item=>item.id===task.assistant_id&&item.space_id===task._space_id)?.name||task.assistant_id,
      downloadFile:({task:id,path})=>client.downloadFile(id,path,fileScope),toolMemory,approvalFor:tool=>approvalFor(tool,task.id),onDecision:decideApproval,
      onSend:async text=>{
        const version=taskDraftVersions.get(draftKey);
        let attempt=taskWrites.get(draftKey);if(!attempt||attempt.text!==text){attempt={request_id:uid(),text};taskWrites.set(draftKey,attempt);}
        await request(path+'/messages',{...fileScope,method:'POST',body:attempt});
        if(taskWrites.get(draftKey)===attempt)taskWrites.delete(draftKey);
        if(taskDrafts.get(draftKey)===text&&taskDraftVersions.get(draftKey)===version)taskDrafts.delete(draftKey);
        if(current())await refreshTaskEvents(view);
      },
      onStop:()=>mutate('/interrupt',{request_id:view.stopID||=(uid())}),
      onCancel:()=>mutate('/cancel',{request_id:view.cancelID||=(uid())}),
      onPause:()=>mutate('/control',{run_id:view.record.run_id,action:view.record.info?.paused?'resume':'pause'}),
      onUsage:()=>openUsageView({...fileScope,name:task.title,scope:task.id,returnTask:{...task},taskReturn:returnTo}),
      onRefresh:()=>refreshTaskEvents(view),onSettings:()=>{configurationSpace=task._space_id;filter.settingsTab='providers';routeChanged('settings');return show('settings');},
    });
    view.ui=ui;view.transcript=ui.transcript;taskView=view;heading(task.title);permissions();
    await refreshTaskEvents(view);if(!current())return;
    restoreView(`task:${draftKey}`,token);
  }
  function refreshTaskEvents(view=taskView){if(!view)return Promise.resolve();view.eventsQueue||=createRefreshQueue(()=>readTaskEvents(view),{active:()=>!disposed&&gate.current(view.token)&&taskView===view});return view.eventsQueue.run();}
  async function readTaskEvents(view) {
    let render=false;
    try{
      const path=`/api/v1/tasks/${encodeURIComponent(view.task.id)}`,options={...view.scope,signal:view.token.signal};
      const updates=await Promise.allSettled([request(path,options),request(`${path}/messages`,options)]);if(!gate.current(view.token)||taskView!==view)return;
      if(updates[0].status==='fulfilled')view.record=updates[0].value;if(updates[1].status==='fulfilled')view.inputs=list(updates[1].value);
      view.connectionError=updates.filter(update=>update.status==='rejected'&&update.reason.name!=='AbortError').map(update=>update.reason.message).join('\n');
      let count=0,page;
      do{page=await request(`${path}/events?${new URLSearchParams({after:view.state.cursor,limit:100})}`,options);if(!gate.current(view.token)||taskView!==view)return;const previous=view.state.cursor;appendTaskEvents(view.state,page.events);if(page.has_more&&view.state.cursor===previous)throw Error('Task event cursor did not advance');count+=(page.events||[]).length;}while(page.has_more&&count<5000);
      view.unavailable=!!page.unavailable;render=true;
      if(page.has_more)queueMicrotask(()=>refreshTaskEvents(view));
    }catch(reason){if(gate.current(view.token)&&reason.name!=='AbortError'){view.connectionError=reason.message;render=true;}}
    if(render&&gate.current(view.token)&&taskView===view)renderTaskTranscript(view);
  }
  function renderTaskTranscript(view){view.ui.update({record:view.record,state:view.state,inputs:view.inputs,unavailable:view.unavailable,connectionError:view.connectionError||'',approvals:pendingApprovals.filter(item=>item.request?.task===view.task.id)});renderApprovals();}
  function approvalFor(tool,task=''){if(approvalOwner!==approvalContext())return;const scope=approvalScope();const approval=pendingApprovals.find(item=>item.request.owner===scope.owner&&(item.request.task||'')===task&&item.request.call_id===tool.id&&item.request.turn===tool.turn_id);return approval?{...approval,task_title:taskNames.get(taskNameKey(scope,task))}:undefined;}
  function taskNeedsApproval(id,phase){return approvalOwner===approvalContext()?pendingApprovals.some(item=>item.request.owner===scope().owner&&item.request.task===id):phase==='waiting_approval';}
  const taskNameKey=(captured,id)=>JSON.stringify([captured.space,captured.owner,id]);
  function rememberTasks(items){for(const task of items)taskNames.set(taskNameKey({space:task._space_id,owner:task.assistant_id||task._assistant_id},task.id),task.title);}
  function taskPhaseLabel(phase){return phase==='waiting_approval'?translate(locale(),'tool.waiting_approval'):tr({running:'Running',queued:'Queued',waiting:'Waiting',failed:'Failed',complete:'Complete',interrupted:'Interrupted',cancelled:'Cancelled'}[phase]||phase,{running:'运行中',queued:'排队中',waiting:'等待中',failed:'失败',complete:'已完成',interrupted:'已中断',cancelled:'已取消'}[phase]||phase);}
  async function decideApproval(approval,action){
    const captured={...approvalScope()};
    if(disposed||approvalOwner!==approvalContext()||approval.request.owner!==captured.owner||(taskView&&approval.request.task!==taskView.task.id)||!pendingApprovals.some(item=>item.id===approval.id))throw Error(tr('Approval context changed.','授权上下文已改变。'));
    await request('/api/v1/permissions',{...captured,method:'POST',body:{id:approval.id,action}});
    await permissions();
  }
  async function schedules(root,token) {
    root.classList.add('schedules-content');const content=n('div','schedule-projection');root.append(content);
    return connectView(root,token,()=>across('/api/v1/schedules?limit=50',token,{paginate:true,perAssistant:true}),pages=>{
      const projection=n('div','schedule-projection');renderSchedules(projection,pages);reconcile(content,projection);
    },{topics:['schedules','tasks','assistants','spaces','machines']});
  }
  function renderSchedules(root,pages) {

    const items=pages.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner)).sort((a,b)=>(a.next_at_ms||0)-(b.next_at_ms||0)||a._assistant_id.localeCompare(b._assistant_id)||a.id.localeCompare(b.id));
    filter.scheduleOffset=Math.min(filter.scheduleOffset,Math.max(0,Math.floor((items.length-1)/20)*20));
    const rows=n('div','schedule-list');rows.dataset.scrollKey='schedule-list';if(!items.length)empty(rows,tr('No schedules','暂无定时计划'));
    for(const item of items.slice(filter.scheduleOffset,filter.scheduleOffset+20)) {
      const row=n('div','schedule-card page-card'),button=action(item.spec.title,()=>scheduleHistory(item),{disabled:item._stale});button.classList.add('schedule-title');
      const owner=n('span','schedule-owner'),assistant=(client.assistants||[]).find(owner=>owner.id===item._assistant_id&&owner.space_id===item._space_id),space=(client.spaces||[]).find(space=>space.id===item._space_id);
      owner.append(n('span','',assistant?.name||item._assistant_id));owner.title=item._space_name||'';
      owner.append(icon(space?.kind==='local'&&!space?.remote?'machines':'cloud'));
      row.append(button,owner,n('span','hint',item._stale?tr('Stale','已过期'):item.spec.enabled?item.next_label||tr('Not scheduled','未安排'):tr('Paused','已暂停')));row.dataset.viewKey=JSON.stringify([item._space_id,item._assistant_id,item.id]);rows.append(row);
    }
    root.append(rows);
    const footer=n('div','schedule-pagination'),buttons=toolbar(action(tr('Previous','上一页'),()=>{filter.scheduleOffset=Math.max(0,filter.scheduleOffset-20);show('tasks');},{disabled:!filter.scheduleOffset}),action(tr('Next','下一页'),()=>{filter.scheduleOffset+=20;show('tasks');},{disabled:filter.scheduleOffset+20>=items.length}));
    footer.append(n('span','hint',`${items.length} · ${Math.floor(filter.scheduleOffset/20)+1}`),buttons);root.append(footer);
  }
  async function scheduleHistory(item,record=true,runsOffset=0) {
    if(scheduleView&&scheduleView.item.id===item.id&&scheduleView.item._space_id===item._space_id&&scheduleView.item._assistant_id===item._assistant_id&&scheduleView.runsOffset===runsOffset&&pageView)return pageView.queue.run();
    captureView();pageView?.queue.close();pageView=null;closeTaskList();taskView=null;detailParent=true;filter.taskTab='schedules';
    if(record)history.pushState({potRoute:'tasks',potSchedule:{...item},potScope:{space:item._space_id,owner:item._assistant_id}},'',location.href);
    const token=gate.next(),root=body(),content=n('div');root.append(content);
    const view={item,runsOffset,token,busy:false,confirmed:false,content};scheduleView=view;
    await connectView(root,token,()=>request(`/api/v1/schedules?${new URLSearchParams({selected:item.id,runs_offset:runsOffset,limit:20})}`,{signal:token.signal,space:item._space_id,owner:item._assistant_id}),value=>{
      if(!value.selected||value.selected.deleted){scheduleView=null;return show('tasks',{force:true});}
      view.item={...item,...value.selected};const projection=n('div');renderScheduleHistory(projection,view,value,runsOffset);reconcile(content,projection);
    },{topics:['schedules','tasks','machines','spaces'],space:item._space_id});
    if(gate.current(token))restoreView(JSON.stringify(['schedule-history',item._space_id,item._assistant_id,item.id,runsOffset]),token);
  }
  function renderScheduleHistory(root,view,value,runsOffset) {
    const selected=view.item;heading(selected.spec.title);const info=card(tr('Schedule details','定时计划详情'));
    info.append(n('p','hint',selected.spec.goal),n('p','hint',`${selected.spec.timezone||''} · ${selected.next_label||tr('Not scheduled','未安排')}`));if(selected.last_error)info.append(n('p','inline-error',selected.last_error));
    const actions=toolbar();
    for(const name of ['run',selected.spec.enabled?'pause':'resume','edit','delete']) {
      const writeKey=JSON.stringify([selected._space_id,selected._assistant_id,selected.id,name]);
      const button=action(tr({run:'Run now',pause:'Pause',resume:'Resume',edit:'Edit',delete:'Remove'}[name],{run:'立即运行',pause:'暂停',resume:'恢复',edit:'编辑',delete:'移除'}[name]),async event=>{
        const button=event.currentTarget,actions=button.parentElement;
        if(view.busy||scheduleView!==view||!gate.current(view.token))return;
        if(name==='edit'){scheduleEditor(selected);return;}
        if(name==='delete'&&!view.confirmed){view.confirmed=true;button.textContent=tr('Confirm removal','确认移除');return;}
        view.busy=true;for(const control of actions.querySelectorAll('button'))control.disabled=true;
        const body=scheduleWrites.get(writeKey)||{request_id:uid(),action:name,schedule_id:selected.id};scheduleWrites.set(writeKey,body);
        try{await request('/api/v1/schedules',{method:'POST',space:selected._space_id,owner:selected._assistant_id,body});scheduleWrites.delete(writeKey);if(scheduleView===view&&gate.current(view.token)){if(name==='delete'){scheduleView=null;await show('tasks',{force:true});}else await pageView.queue.run();}}
        catch(reason){if(scheduleView===view&&gate.current(view.token))errorView(reason,view.content);}
        finally{view.busy=false;for(const control of actions.querySelectorAll('button'))control.disabled=false;}
      },{primary:name==='run',danger:name==='delete',disabled:selected._stale||view.busy});actions.append(button);
    }
    if(view.confirmed){const remove=actions.lastChild;remove.textContent=tr('Confirm removal','确认移除');}info.append(actions,n('p','hint',tr('Pausing or removing only affects future runs.','暂停或移除只影响未来的执行。')));root.append(info,n('p','hint',tr('Run history','运行历史')));
    const rows=(value.runs||[]).map(run=>{const summary=n('span','',run.summary);if(run.task_id)summary.append(action(tr('View task','查看任务'),()=>taskDetail({id:run.task_id,title:run.summary||selected.spec.title,assistant_id:selected._assistant_id,_assistant_id:selected._assistant_id,_space_id:selected._space_id})));return [run.scheduled_label,run.state,summary];});
    root.append(table([tr('Scheduled','计划时间'),tr('State','状态'),tr('Summary','摘要')],rows,(value.runs||[]).map(run=>run.id||JSON.stringify([run.scheduled_label,run.task_id]))));
    root.append(toolbar(action(tr('Newer entries','较新记录'),()=>scheduleHistory(selected,false,Math.max(0,runsOffset-20)),{disabled:!runsOffset}),action(tr('Older entries','较早记录'),()=>scheduleHistory(selected,false,value.runs_next_offset),{disabled:value.runs_next_offset==null})));
  }
  function scheduleEditor(item) {
    const spec=item?.spec||{title:'',goal:'',machine_id:'',timezone:Intl.DateTimeFormat().resolvedOptions().timeZone,kind:'daily',at:'',every_minutes:60,time:'09:00',weekdays:[1,2,3,4,5],misfire:'coalesce',notify:'changes',enabled:true,profile:null,model:null,requirements:{tools:false,desktop:false,exact_budget:false,live_steering:false}};
    renderEditor(item?tr('Edit schedule','编辑定时计划'):tr('New schedule','新建定时计划'),{...spec,owner_ref:JSON.stringify([item?item._space_id:scope().space,item?item._assistant_id:scope().owner]),weekdays:(spec.weekdays||[]).join(',')},[
      ...(!item?[{name:'owner_ref',label:tr('Assistant','助手'),choices:(client.assistants||[]).map(owner=>[JSON.stringify([owner.space_id,owner.id]),`${owner.name} · ${owner.space_name||''}`])}]:[]),
      {name:'title',label:tr('Title','标题'),required:true},{name:'goal',label:tr('Goal','目标'),kind:'textarea',required:true},{name:'machine_id',label:tr('Machine ID','机器 ID')},
      {name:'timezone',label:tr('Time zone','时区'),required:true},{name:'kind',label:tr('Repeat','重复'),choices:[['once',tr('Once','一次')],['interval',tr('Interval','间隔')],['daily',tr('Daily','每日')],['weekly',tr('Weekly','每周')]]},
      {name:'at',label:tr('Date and time','日期与时间'),help:'YYYY-MM-DD HH:MM'},{name:'every_minutes',label:tr('Interval in minutes','间隔分钟'),kind:'number',parse:Number},{name:'time',label:tr('Time','时间'),kind:'time'},
      {name:'weekdays',label:tr('Weekdays','星期'),help:tr('1 = Monday, 7 = Sunday; comma separated.','1 为周一，7 为周日；以逗号分隔。')},
      {name:'misfire',label:tr('Missed runs','错过执行'),choices:[['coalesce',tr('Run once','合并执行一次')],['skip',tr('Skip','跳过')]]},
      {name:'notify',label:tr('Notifications','通知'),choices:[['changes',tr('Changes','发生变化')],['all',tr('Every run','每次执行')],['failures',tr('Failures','仅失败')]]},{name:'enabled',label:tr('Enabled','启用'),kind:'checkbox'},
    ],(values,context)=>{const {owner_ref,...specValues}=values;const [space,owner]=JSON.parse(owner_ref);return request('/api/v1/schedules',{...context,space,owner,method:'POST',body:{request_id:context.request_id,action:item?'update':'create',schedule_id:item?.id||'',spec:{...specValues,weekdays:values.weekdays.split(',').filter(Boolean).map(Number)}}});},{capturedScope:item?{space:item._space_id,owner:item._assistant_id}:scope()});
    if(item)editor.returnSchedule={...item};
  }
  async function usage(root, token) {
    const spaces=client.spaces||[],headingActions=n('div','usage-heading-actions');
    if(!usageOwner){
      if(spaces.length<=1||!spaces.some(space=>space.id===filter.usageSpace))filter.usageSpace='all';
      if(spaces.length>1){
        const select=n('select','field-input usage-location-select');select.setAttribute('aria-label',tr('Statistics location','统计位置'));
        for(const [value,label]of [['all',tr('All locations','所有位置')],...spaces.map(space=>[space.id,space.name])]){const option=n('option','',label);option.value=value;select.append(option);}
        select.value=filter.usageSpace;select.title=select.selectedOptions[0].textContent;
        select.onchange=()=>{filter.usageSpace=select.value;usageOffsets.clear();show('usage',{keepUsageOwner:true});};headingActions.append(select);
      }
    }
    const titleRow=toolbar(n('h2','page-heading',usageOwner?tr('Lifetime usage','累计用量'):tr('Overall statistics','总体统计')),headingActions);titleRow.classList.add('usage-heading');root.append(titleRow);
    if(usageOwner)root.append(n('p','hint',usageOwner.name));
    const timezone=Intl.DateTimeFormat().resolvedOptions().timeZone,scopeID=usageOwner?.scope||usageOwner?.owner||'',params={days:filter.usageDays,timezone,scope:scopeID},spaceID=usageOwner?.space||filter.usageSpace;
    root.dataset.usageScope=JSON.stringify([spaceID,scopeID,usageOwner?.owner||'']);
    const listsRoot=n('div','usage-lists'),jobs=[];
    for(const space of (client.spaces||[]).filter(space=>spaceID==='all'||space.id===spaceID)){
      const owner={space:space.id,owner:usageOwner?.owner||''},base={timezone,scope:scopeID};
      for(const [kind,title]of [['models',usageOwner?tr('Lifetime model usage','累计模型用量'):tr('Agent / model usage','Agent / 模型用量')],...(usageOwner?[['requests',tr('Model requests','模型请求')],['tools',tr('Tool statistics','工具统计')],['compactions',tr('Context compaction','上下文压缩')]]:[])])jobs.push(createUsageList({root:listsRoot,kind,title,params:kind==='models'&&!usageOwner?params:base,owner,request,token,current:()=>gate.current(token),offsets:usageOffsets,cache:usageCache,locale:locale(),tr,spaceName:!usageOwner&&spaceID==='all'?space.name:''}));
    }
    const content=n('div'),limits=n('div','usage-limit-actions');headingActions.append(limits);root.append(content,listsRoot);
    let initial=true;
    return connectView(root,token,async()=>{
      const summary=across(`/api/v1/usage/summary?${new URLSearchParams(params)}`,token,{spaceID,ownerID:usageOwner?.owner||'',errorTarget:root});
      const [pages]=await Promise.all([summary,...jobs.map(job=>initial?job:job.refresh())]);initial=false;
      return pages;
    },pages=>{const projection=n('div'),limitProjection=n('div','usage-limit-actions');
    const data=pages.length===1?{...pages[0].value,spaces:pages}:combineUsage(pages);
    const {exact,compact}=usageNumbers(locale()),total=(usageOwner?data.lifetime||data.total:data.total)||{},metrics=n('div','usage-metrics'),totalCard=n('section','page-card usage-total');
    for(const [label,value]of [[tr('Total tokens','总 token'),total.input+total.output],[tr('Input (includes cache)','输入（含缓存）'),total.input],[tr('Output','输出'),total.output]]){const item=n('div','metric');item.append(n('span','hint',label),n('strong','',compact(value)));usageTooltip(item,{title:label,rows:usageRows(total,locale(),tr),note:tr('Cached tokens are included in input. Token counts are not billed cost.','缓存已计入输入，token 数量不代表实际费用。')});metrics.append(item);}
    totalCard.append(metrics,n('p','usage-cache-breakdown',`${tr('Cached input','缓存输入')} ${exact(total.cache)} · ${tr('Cache hit rate','缓存命中率')} ${usageNumbers(locale()).hitRate(total)}`),n('p','hint',`${tr('Model requests','模型请求')} ${exact(total.requests||0)}${total.unknown?' · '+total.unknown+' '+tr('incomplete records','条不完整记录'):''}`));projection.append(totalCard);
    const routing=data.routing_total||{};
    if(routing.requests){const routingCard=card(tr('Task routing turns','任务路由回合'));routingCard.append(usageTooltip(n('p','usage-number',`${compact(routing.input+routing.output)} tokens`),{title:tr('Task routing turns','任务路由回合'),rows:usageRows(routing,locale(),tr),note:tr('Includes the entire main-chat turn that delegated a task. Already included in totals.','包含委派任务的整个主聊天回合，已计入总量。')}));projection.append(routingCard);}
    const trend=usageTrend(data,{locale:locale(),tr,timezone,days:filter.usageDays,segments,onDays:days=>{filter.usageDays=days;if(!usageOwner)usageOffsets.clear();show('usage',{keepUsageOwner:true});}});projection.append(trend);
    for(const page of pages) {
      const value=page.value,owner={space:page.space.id,owner:usageOwner?.owner||''};
      const policy=value.quota.policy;
      limitProjection.append(action(`${tr('Usage limits','用量限制')}${pages.length>1?' · '+page.space.name:''}`,()=>policyEditor(policy,owner),{disabled:page.stale}));
      if(value.quota?.active){const allowance=card(`${tr('Token allowance','Token 配额')} · ${page.space.name}`);allowance.append(n('p','hint',value.quota?.active?`${Number(value.quota.remaining).toLocaleString(locale())} ${tr('remaining','剩余')} · ${value.quota.reset_label||''}`:tr('No active token limit','未启用 token 限额')),action(tr('Edit allowance','编辑配额'),()=>policyEditor(policy,owner),{disabled:page.stale}));projection.append(allowance);}
      if(usageOwner&&!usageOwner.returnTask)for(const mail of value.mail_waits||[]){const waiting=section(tr('Correspondence waiting for allowance','助手通信正在等待配额'));waiting.append(n('p','hint',mail.provider||''));const requestID=uid(),resume=action(tr('Resume correspondence','恢复助手通信'),event=>operation(event.currentTarget,()=>request('/api/v1/usage/resume',{...owner,method:'POST',body:{request_id:requestID,scope:mail.scope,turn:mail.turn,since:mail.since}}),()=>show('usage',{keepUsageOwner:true})),{disabled:page.stale});waiting.append(resume);projection.append(waiting);}
      if(value.waiting){const waiting=section(tr('Waiting for allowance','等待配额'));waiting.append(n('p','hint',value.waiting.provider||''));for(const name of ['resume','cancel']){const requestID=uid(),task=usageOwner?.returnTask;const button=action(tr(name==='resume'?'Resume':'Cancel waiting',name==='resume'?'恢复':'取消等待'),event=>operation(event.currentTarget,async()=>{if(task)return request(`/api/v1/tasks/${encodeURIComponent(task.id)}/${name}`,{...owner,method:'POST',body:{request_id:requestID,...(name==='resume'?{turn:value.waiting.turn,since:value.waiting.since}:{})}});if(name==='resume')return request('/api/v1/usage/resume',{...owner,method:'POST',body:{request_id:requestID,turn:value.waiting.turn,since:value.waiting.since}});const snapshot=await request('/api/v1/snapshot',owner),message=snapshot.messages?.find(message=>message.id===snapshot.turn?.user_message_id);if(!message||message.request_id!==value.waiting.turn)throw Error(tr('Allowance wait changed. Refresh Usage.','配额等待已变化，请刷新用量页面。'));return request('/api/v1/turn/cancel',{...owner,method:'POST',body:{request_id:requestID,user_message_id:message.id}});},()=>show('usage',{keepUsageOwner:true})),{disabled:page.stale||!usageOwner});waiting.append(button);}projection.append(waiting);}
    }
      for(const [index,node]of [...projection.children].entries())node.dataset.viewKey=node.querySelector('h2')?.textContent||node.className+':'+index;
      reconcile(content,projection);reconcile(limits,limitProjection);
    },{topics:['usage','spaces','providers'],space:spaceID==='all'?'':spaceID});
  }
  function policyEditor(policy,owner) {
    renderEditor(tr('Token allowance','Token 配额'),policy,[
      {name:'period',label:tr('Period','周期'),choices:[['none',tr('Unlimited','无限制')],['day',tr('Daily','每日')],['week',tr('Weekly','每周')]]},
      {name:'limit',label:tr('Token limit','Token 上限'),kind:'number',parse:Number,required:true},{name:'timezone',label:tr('Time zone','时区'),required:true},{name:'auto_resume',label:tr('Automatically resume','自动恢复'),kind:'checkbox'},
    ],(values,context)=>request('/api/v1/usage/policy',{...context,method:'POST',body:values}),{capturedScope:owner});
  }
  async function machines(root,token) {
    const rows=n('div'),entries=new Map(),membership=n('div'),emptyRow=n('p','page-empty',tr('No machines','暂无机器'));root.append(rows,emptyRow,membership);
    return connectView(root,token,async()=>{const [inventory,permissionPages]=await Promise.all([across('/api/v1/machines',token),across('/api/v1/permissions',token).catch(()=>[])]);return {inventory,permissionPages};},async({inventory,permissionPages})=>{
      const items=inventory.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner));emptyRow.hidden=!!items.length;
      keyedRows(rows,entries,items,{key:machine=>JSON.stringify([machine._space_id,machine.runtime_id||machine.id]),create:()=>{
        const element=n('section','page-card machine-card'),header=n('div','machine-header'),mark=n('span','machine-icon'),name=n('span','machine-name'),status=n('span'),tags=n('p','hint machine-tags'),location=n('p','hint machine-location'),permissions=n('div','machine-permissions');mark.append(icon('machines'));header.append(mark,name,status);element.append(location,header,tags,permissions);return {element,name,status,tags,location,permissions};
      },update:(entry,machine)=>{
        const id=machine.runtime_id||machine.id,permissionPage=permissionPages.find(page=>page.space.id===machine._space_id),policy=permissionPage?.value.policy;
        const stale=machine._stale||permissionPage?.stale||client.spaces.find(space=>space.id===machine._space_id)?.connected===false,permissions=entry.permissions;
        entry.element.dataset.machineId=id;entry.element.dataset.spaceId=machine._space_id;entry.name.textContent=machine.name||id;entry.location.textContent=machine._space_name;entry.status.textContent=stale?tr('Stale','已过期'):machine.connected?tr('Connected','已连接'):tr('Offline','离线');entry.status.className=machine.connected&&!stale?'success-text':'hint';entry.tags.textContent=(machine.tags||[]).join(' · ');entry.tags.hidden=!machine.tags?.length;
      if(!policy?.machines?.[id]){permissions.replaceChildren(n('p','inline-error',tr('Execution permissions are unavailable.','执行权限暂不可用。')),action(tr('Retry','重试'),()=>show('machines')));return;}
      const captured={space:machine._space_id,owner:''},controlID=permissionPage.space.control_id,current=policy.machines[id],key=JSON.stringify([machine._space_id,controlID,id]);
      let draft=machineDrafts.get(key);
      if(!draft||!draft.dirty&&!draft.pending){const error=draft?.error;draft=createEditorSession({...captured,boot:client.runtime.boot_id},current);draft.error=error;machineDrafts.set(key,draft);}
      renderMachinePermissions({root:permissions,machine,policy,request,scope:captured,locale,draft,stale,
        isCurrent:()=>client.runtime.boot_id===draft.context.boot&&client.spaces.some(space=>space.id===captured.space&&space.control_id===controlID),
        refresh:()=>{if(route==='machines'&&!editor)return pageView?.queue.run();},
        edit:({title,initial,fields,save,scope})=>renderEditor(title,initial,fields,save,{capturedScope:scope})});
      }});
      await renderMachineMembership({client,root,membershipRoot:membership,machines:items,locale,notify,refresh:()=>pageView?.queue.run(),edit:({title,initial,fields,save,scope})=>renderEditor(title,initial,fields,save,{capturedScope:scope}),signal:token.signal});
    },{topics:['machines','spaces','permissions'],space:client.spaces?.length===1?client.spaces[0].id:''});
  }
  const approvalScope=()=>taskView?.scope||scope();
  const approvalContext=()=>JSON.stringify([approvalScope(),taskView?.task.id||'']);
  function renderApprovals() {
    const chatContext=JSON.stringify(scope());
    if(disposed||approvalOwner!==approvalContext()){approvalHost.replaceChildren();chatApprovals.update(chatContext,[]);return;}
    const nodes=[],entries=[],retained=new Set();
    for(const approval of pendingApprovals) {
      const toolRoot=taskView?[...taskView.transcript.querySelectorAll('.tool-call')].find(node=>node.dataset.callId===approval.request.call_id&&node.dataset.turnId===approval.request.turn):null;
      if(toolRoot){continue;}
      const key=JSON.stringify([approvalOwner,approval.id]);retained.add(key);
      if(!approvalCards.has(key)) {
        const capturedKey=approvalOwner;
        approvalCards.set(key,createApprovalCard({locale,onDecision:async(original,decision)=>{
          if(approvalContext()!==capturedKey)throw Error(tr('Approval context changed.','授权上下文已改变。'));
          await decideApproval(original,decision);
        }}));
      }
      const card=approvalCards.get(key);card.update(approval,{taskTitle:taskNames.get(taskNameKey(approvalScope(),approval.request.task))});
      if(taskView)nodes.push(card.element);else entries.push({id:approval.id,element:card.element});
    }
    chatApprovals.update(chatContext,entries);
    for(const key of approvalCards.keys())if(!retained.has(key))approvalCards.delete(key);
    const target=taskView?.ui.approvalHost||approvalHost;
    if(taskView)approvalHost.replaceChildren();
    for(const node of [...target.childNodes])if(!nodes.includes(node))node.remove();
    nodes.forEach((node,index)=>{if(target.childNodes[index]!==node)target.insertBefore(node,target.childNodes[index]||null);});
  }
  let approvalQueue=null,approvalQueueKey='';
  function permissions(){
    const captured={...approvalScope()},key=approvalContext();
    if(!captured.owner||disposed){approvalQueue?.close();approvalQueue=null;approvalQueueKey='';approvalGate.close();pendingApprovals=[];approvalOwner='';approvalCards.clear();approvalHost.replaceChildren();chatApprovals.update(JSON.stringify(scope()),[]);onApprovalsChanged();return Promise.resolve();}
    if(!approvalQueue||approvalQueueKey!==key){
      approvalQueue?.close();approvalQueueKey=key;const token=approvalGate.next(),current=()=>!disposed&&approvalGate.current(token)&&approvalQueueKey===key&&approvalContext()===key;
      pendingApprovals=[];approvalOwner='';approvalCards.clear();approvalHost.replaceChildren();
      approvalQueue=createRefreshQueue(async()=>{
        const value=await request('/api/v1/permissions',{...captured,signal:token.signal});if(!current())return;
        pendingApprovals=(value.pending||[]).filter(item=>item.request?.owner===captured.owner&&(!taskView||item.request.task===taskView.task.id));approvalOwner=key;
        if(taskView)renderTaskTranscript(taskView);onApprovalsChanged();renderApprovals();
        await Promise.all([...new Set(pendingApprovals.map(item=>item.request.task).filter(Boolean))].map(async id=>{
          const nameKey=taskNameKey(captured,id);if(taskNames.has(nameKey))return;
          try{const record=await request(`/api/v1/tasks/${encodeURIComponent(id)}`,{...captured,signal:token.signal});if(current())taskNames.set(nameKey,record.info?.title||record.request?.text||id);}catch{/* Retain the exact Task ID when its title is unavailable. */}
        }));if(current())renderApprovals();
      },{active:current,onError:reason=>notify(reason.message)});
    }return approvalQueue.run();
  }
  async function show(next,{keepUsageOwner=false,force=false}={}) {
    if(editor)return;
    if(!keepUsageOwner)usageOwner=null;
    if(next==='settings'&&filter.settingsTab!=='general'&&!configurationSpace)configurationSpace=scope().space;
    const viewKey=JSON.stringify([next,next==='tasks'?[filter.taskTab,filter.taskScope,filter.taskQuery,filter.taskOffset,filter.scheduleOffset]:next==='usage'?[filter.usageDays,filter.usageSpace,usageOwner]:next==='settings'?[filter.settingsTab,configurationSpace]:[]]);
    if(!force&&route===next&&currentViewKey===viewKey){if(taskListView)return refreshTaskList();if(pageView)return pageView.queue.run();}
    captureView();viewReady=false;pageView?.queue.close();pageView=null;

    closeTaskView();taskOrigin=null;closeTaskList();scheduleView=null;detailParent=false;if(!keepUsageOwner)usageOwner=null;
    route=next;heading(tr({tasks:filter.taskTab==='schedules'?'Schedules':'Tasks',machines:'Machines',usage:'Usage',settings:'Settings'}[next]||next,{tasks:filter.taskTab==='schedules'?'定时计划':'任务',machines:'机器',usage:'用量',settings:'设置'}[next]||next));
    const version=++pageVersion,token=gate.next(),root=body();currentViewKey=viewKey;
    root.append(n('p','hint',tr('Loading…','正在加载…')));
    try {root.replaceChildren();await ({settings,tasks,machines,usage}[next]||settings)(root,token);if(gate.current(token))restoreView(viewKey,token);}
    catch(reason){if(gate.current(token)&&version===pageVersion&&!disposed)errorView(reason,root);}
  }
  function openUsageView(context,record=true){usageOffsets.clear();usageOwner={...context};route='usage';if(record)history.pushState({potRoute:'usage',potScope:{space:context.space,owner:context.owner},potUsage:{...usageOwner}},'',location.href);routeChanged('usage');return show('usage',{keepUsageOwner:true});}
  const popstate=async event=>{
    if(!editor)return false;if(event.state?.potEditor===editor.id)return true;
    const current=editor;if(!await back())history.pushState({...history.state,potEditor:current.id},'',location.href);return true;
  };
  const beforeUnload=event=>{if([...(editor?[editor.session]:[]),...editorParents.map(value=>value.session),...machineDrafts.values()].some(session=>session.dirty||session.pending)){event.preventDefault();event.returnValue='';}};
  const preference=()=>{if(taskView){taskDetail(taskView.task,{recordHistory:false});return;}if(scheduleView&&!editor){scheduleHistory(scheduleView.item,false,scheduleView.runsOffset);return;}if(!editor&&route)show(route,{keepUsageOwner:!!usageOwner,force:true});};
  window.addEventListener('beforeunload',beforeUnload);window.addEventListener('pot:locale',preference);
  return {get title(){return title;},get editing(){return !!editor;},get approvalSpace(){return approvalScope().space;},get taskOwner(){return taskView?.scope.owner||'';},get hasParent(){return !!editor||detailParent||!!usageOwner;},show,assistant,setupAI,leave,back,permissions,syncApprovals:renderApprovals,taskMenu(){taskView?.ui.openMenu();},handleHistory:popstate,openSchedule:scheduleHistory,openUsage:openUsageView,clearApprovals(){approvalQueue?.close();approvalQueue=null;approvalQueueKey='';approvalGate.close();approvalOwner='';pendingApprovals=[];approvalCards.clear();approvalHost.replaceChildren();chatApprovals.update(JSON.stringify(scope()),[]);},approvalFor,decideApproval,taskNeedsApproval,rememberTasks,renderApprovals,conversationUsage(name,record=true){return openUsageView({...scope(),name},record);},openTask(task,options={}){route='tasks';return taskDetail(task,options);},
    showProviders(){filter.settingsTab='providers';return show('settings');},
    refresh(topics=[],source={}){
      if(editor||disposed||!route||!host.offsetParent)return;
      if(pageView){if(relevantChange(topics,pageView.topics,pageView.space,source))return pageView.queue.run();return;}
      if(route==='settings')return;
      if(route==='tasks'&&!relevantChange(topics,['tasks','runner','spaces','assistants','machines',...(filter.taskTab==='schedules'?['schedules']:[])],taskView?.scope.space||'',source))return;
      if(taskListView)return refreshTaskList();if(detailParent&&!taskView&&!scheduleView)return;
      if(taskView)return refreshTaskEvents();

    },
    dispose(){for(const value of [...editorParents,...(editor?[editor]:[])])value.lifecycle?.dispose?.();disposed=true;closeTaskList();closeTaskView();approvalQueue?.close();pageView?.queue.close();gate.close();approvalGate.close();window.removeEventListener('beforeunload',beforeUnload);window.removeEventListener('pot:locale',preference);},
  };
}
