import {createToolCall} from './tool-call.mjs';
import {toolForest} from '../state/tool-calls.mjs';
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
import {renderSandboxResources} from './resources.mjs';
import {renderAbout} from './about.mjs';
import {renderMachineMembership} from './membership.mjs';
import {renderIntegrations} from './integrations.mjs';

export function createPages({onApprovalsChanged=()=>{},client, host, approvalHost, locale, notify, changed, titleChanged, openChat, routeChanged=()=>{},withResourceConsent=({work})=>work()}) {
  const gate = createRequestGate(), approvalGate = createRequestGate();
  let route = '', title = '', editor = null, disposed = false, pageVersion = 0, configurationSpace = '',taskView=null,resourceView=null,scheduleView=null,detailParent=false,usageOwner=null;
  let filter = {taskScope:'active', taskQuery:'', taskOffset:0, scheduleOffset:0, taskTab:'tasks', usageDays:7, usageScope:'all', usageOffset:0,usageSpace:'all',settingsTab:'general',taskTarget:null,taskSpace:'all'};
  let pendingApprovals=[];
  const toolMemory=new Map();
  const cached = new Map(),spaceCache = new Map(),expandedMachines=new Set(),editorParents=[],taskDrafts=new Map(),scheduleWrites=new Map();
  let currentViewKey='',viewReady=false,approvalOwner='',shortcutOpen=null;
  const tr = (en, zh) => locale() === 'zh-CN' ? zh : en;
  const scope = () => client.scope();
  const request = (path, options = {}) => client.request(path, options);
  const uid = () => crypto.randomUUID();
  const list = value => Array.isArray(value) ? value : value?.items || [];
  function captureView(){if(!editor&&viewReady&&currentViewKey&&host.offsetParent)cached.set(currentViewKey,{scroll:host.scrollTop,expanded:[...host.querySelectorAll('details[data-cache-key][open]')].map(node=>node.dataset.cacheKey),scrolls:[...host.querySelectorAll('[data-scroll-key]')].map(node=>({key:node.dataset.scrollKey,top:node.scrollTop,left:node.scrollLeft}))});}
  function restoreView(key,token){currentViewKey=key;viewReady=true;requestAnimationFrame(()=>{if(!gate.current(token)||key!==currentViewKey)return;const value=cached.get(key);if(!value){host.scrollTop=0;return;}host.scrollTop=value.scroll;for(const node of host.querySelectorAll('details[data-cache-key]'))if(node.dataset.cacheKey!=='keyboard-shortcuts')node.open=value.expanded.includes(node.dataset.cacheKey);for(const node of host.querySelectorAll('[data-scroll-key]')){const saved=value.scrolls?.find(item=>item.key===node.dataset.scrollKey);if(saved){node.scrollTop=saved.top;node.scrollLeft=saved.left;}}});}
  async function across(path, token, {spaceID='all', paginate=false,perAssistant=false,ownerID='',allModels=false}={}) {
    const spaces=(client.spaces||[]).filter(space=>spaceID==='all'||space.id===spaceID);
    if(!spaces.length)throw Error(tr('Space is unavailable.','Space 不可用。'));
    const targets=spaces.flatMap(space=>perAssistant?(client.assistants||[]).filter(item=>item.space_id===space.id).map(item=>({space,owner:item.id})):[{space,owner:ownerID}]);
    const results=await Promise.allSettled(targets.map(async({space,owner})=>{
      const key=JSON.stringify([space.id,owner,path]);
      try {
        let value=await request(path,{owner,space:space.id,signal:token.signal});
        if(paginate&&value?.next_offset!=null) {
          let next=value.next_offset,seen=new Set();const items=[...(value.items||[])];
          while(next!=null&&items.length<5000) {if(seen.has(next))throw Error('Invalid pagination cursor');seen.add(next);const url=new URL(path,location.origin);url.searchParams.set('offset',next);const page=await request(url.pathname+url.search,{owner,space:space.id,signal:token.signal});items.push(...(page.items||[]));next=page.next_offset;}
          if(next!=null)throw Error(tr('Too many records. Narrow the filter.','记录过多，请缩小筛选范围。'));value={...value,items,next_offset:null};
        }
        if(allModels){let next=value.agent_model_next_offset,seen=new Set(),models=[...(value.agent_models||[])];while(next!=null&&models.length<5000){if(seen.has(next))throw Error('Invalid model pagination cursor');seen.add(next);const url=new URL(path,location.origin);url.searchParams.set('offset',next);const page=await request(url.pathname+url.search,{owner,space:space.id,signal:token.signal});models.push(...(page.agent_models||[]));next=page.agent_model_next_offset;}if(next!=null)throw Error(tr('Too many models. Narrow the date range.','模型过多，请缩小日期范围。'));value={...value,agent_models:models,agent_model_next_offset:null,next_offset:null};}
        if(gate.current(token))spaceCache.set(key,value);return {space,owner,value,stale:false};
      }catch(reason){if(reason.name==='AbortError')throw reason;const value=spaceCache.get(key);if(value)return {space,owner,value,stale:true,error:reason.message};throw Object.assign(reason,{space});}
    }));
    if(!gate.current(token))return [];
    const failed=results.filter(value=>value.status==='rejected');
    const pages=results.filter(value=>value.status==='fulfilled').map(value=>value.value);
    if(failed.length||pages.some(page=>page.stale)) {const message=n('p','inline-error',tr('Some Spaces are unavailable; totals may be incomplete and cached rows are marked stale.','部分 Space 不可用，用量可能不完整，缓存项目标记为过期。'));host.querySelector('.page-content')?.prepend(message);}
    if(!pages.length&&targets.length)throw failed[0]?.reason||Error('No Space data');return pages;
  }
  function heading(value) {title=value; titleChanged();}
  function errorView(reason, target = host) {
    const box=n('div','inline-error');box.setAttribute('role','alert');box.append(n('span','',reason.message || String(reason)),action(tr('Retry','重试'),()=>show(route)));target.append(box);
  }
  function body() {const value=n('div','page-content');host.replaceChildren(value);return value;}
  function targetSummary(label){const summary=n('summary','machine-host-summary');summary.append(icon('chevron-right'),n('span','',label));return summary;}
  function card(title,description) {const value=section(title,description);value.classList.add('page-card');return value;}
  function segments(entries,current,select) {const value=n('div','segments');for(const [key,label]of entries){const button=action(label,()=>select(key));button.classList.toggle('selected',key===current);button.setAttribute('aria-pressed',String(key===current));value.append(button);}return value;}
  function toolbar(...children) {const result=n('div','page-toolbar');result.append(...children);return result;}
  async function operation(button, work, after = () => show(route)) {
    const version=pageVersion;button.disabled=true;
    try {await work(); if(!disposed&&version===pageVersion&&button.isConnected) await after();}
    catch(reason) {if(!disposed&&version===pageVersion&&button.isConnected) errorView(reason);}
    finally {button.disabled=false;}
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
    const sessions=[...(editor?[editor]:[]),...(!toParent?editorParents:[])];
    if(sessions.some(value=>value.session.pending)) {notify(tr('Please wait for the save result.','请等待保存结果。'));return false;}
    if(sessions.some(value=>value.session.dirty)&&!await confirmDiscard())return false;
    for(const value of sessions)value.lifecycle.dispose?.();
    editor=null;if(!toParent)editorParents.length=0;taskView=null;scheduleView=null;viewReady=false;gate.close();host.replaceChildren();return true;
  }
  function suspendEditor(){if(editor){editor.scroll=host.scrollTop;editor.focus=innerWidth>=720&&editor.root.contains(document.activeElement)?document.activeElement:null;editorParents.push(editor);editor=null;}}
  function renderEditor(label, initial, definitions, save, {returnChat=false,capturedScope,submitLabel=tr('Save','保存')}={}) {
    captureView();viewReady=false;
    gate.close();
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
    const current=editor,returnToConversation=!editor&&usageOwner;
    if(!await leave({toParent:true}))return false;
    if(skipPicker&&editorParents.at(-1)?.kind==='picker')editorParents.pop();
    if(editorParents.length){editor=editorParents.pop();host.replaceChildren(editor.root);heading(editor.label);history.replaceState({...history.state,potEditor:editor.id},'',location.href);host.scrollTop=editor.scroll||0;editor.focus?.focus({preventScroll:true});return true;}
    if(returnToConversation&&usageOwner.returnTask){const task=usageOwner.returnTask;usageOwner=null;route='tasks';routeChanged('tasks');const state={potRoute:'tasks',potScope:{space:task._space_id,owner:task.assistant_id},potTask:task};history.replaceState(state,'',location.href);await taskDetail(task,{recordHistory:false});return true;}
    if(returnToConversation){usageOwner=null;const state={...history.state,potRoute:'chat'};delete state.potUsage;history.replaceState(state,'',location.href);openChat();return true;}
    if(current?.returnSchedule){const state={...history.state};delete state.potEditor;history.replaceState(state,'',location.href);await scheduleHistory(current.returnSchedule,false);return true;}
    const state={...history.state};for(const key of ['potEditor','potTask','potSchedule','potResource','potUsage'])delete state[key];state.potRoute=current?.returnChat||returnToConversation?'chat':current?.caller||route||'settings';history.replaceState(state,'',location.href);
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
    if(client.desktop){const resources=n('section','page-card');resources.append(action(tr('Resource cache','资源缓存'),resourceCache));root.append(resources);}
    if(!client.desktop&&client.access){const access=n('section','page-card browser-actions');access.append(action(tr('Add to Home Screen','添加到主屏幕'),async()=>{if(!await requestPWAInstall())access.append(n('p','hint',tr('Use your browser menu to install Pot or add it to the Home Screen.','通过浏览器菜单安装 Pot 或添加到主屏幕。')));}));if(client.access.push_available){for(const enabled of [true,false]){const button=action(enabled?tr('Enable message notifications','启用消息通知'):tr('Disable message notifications','停用消息通知'),()=>operation(button,()=>configurePush(client.access,{enabled,locale:locale()}),()=>{}));access.append(button);}}access.append(action(tr('Change access token','更改访问令牌'),()=>renderEditor(tr('Browser access','浏览器访问'),{token:''},[{name:'token',label:tr('Access token','访问令牌'),kind:'password',required:true}],values=>configureBrowserAccess(values.token))),action(tr('Disconnect this browser','断开此浏览器'),()=>forgetBrowserAccess()));root.append(access);}

    await renderAbout({client,root,scope:{owner:'',space:client.desktop?(client.spaces||[]).find(space=>space.kind==='local')?.id||'':scope().space},locale});return;
    }
    const locations=client.spaces||[];if(!configurationSpace)configurationSpace=scope().space;
    const settingsScope={space:configurationSpace,owner:''};
    if(client.desktop&&locations.length>1){const location=field(tr('Configuration location','配置位置'),configurationSpace,{choices:locations.map(space=>[space.id,space.name])});location.input.onchange=()=>{configurationSpace=location.input.value;show('settings');};root.append(location.row);}
    if(filter.settingsTab==='integrations'){await renderIntegrations({root,request,scope:settingsScope,signal:token.signal,locale,openExternal:client.openExternal});return;}
    const providers=await request('/api/v1/providers',{...settingsScope,signal:token.signal});if(!gate.current(token))return;
    if(filter.settingsTab==='tasks'){const [policy,allowances]=await Promise.all([request('/api/v1/agent/routing',{...settingsScope,signal:token.signal}),request('/api/v1/usage/providers',{...settingsScope,signal:token.signal})]);if(!gate.current(token))return;const execution=card(tr('Task Configuration','任务配置'));for(const profile of providers.profiles||[]){const row=n('div','list-row');row.append(n('span','row-info',profile.name),n('span','hint',profile.model),action(tr('Configure','配置'),()=>routingEditor(profile,policy,settingsScope)));execution.append(row);}root.append(execution);const limits=card(tr('Provider allowances','提供商配额'));for(const quota of allowances.providers||[]){const profile=(providers.profiles||[]).find(value=>value.provider===quota.provider),row=n('div','list-row');row.append(n('span','row-info',profile?.name||quota.provider),n('span','hint',quota.active?`${Number(quota.remaining).toLocaleString(locale())} ${tr('remaining','剩余')}`:tr('Unlimited','无限制')),action(tr('Edit allowance','编辑配额'),()=>providerPolicyEditor(quota,settingsScope)));limits.append(row);}root.append(limits);return;}
    root.append(n('p','hint',tr('Manage connections and default models for assistants.','管理助手的连接和默认模型。')));
    const defaultProvider=field(tr('Default Provider for new assistants','新助手的默认提供商'),providers.default_provider_id||'',{choices:[['',tr('Select a Provider','选择提供商')],...(providers.profiles||[]).map(profile=>[profile.id,profile.name])]});defaultProvider.row.classList.add('default-provider');defaultProvider.input.onchange=()=>operation(defaultProvider.input,()=>request('/api/v1/providers/default',{...settingsScope,method:'POST',body:{provider_id:defaultProvider.input.value}}));root.append(defaultProvider.row);
    const add=action(tr('+ Add Provider','＋ 添加提供商'),()=>providerPicker(providers,settingsScope));add.setAttribute('aria-label',tr('Add connection','添加连接'));const header=toolbar(n('h2','page-heading',tr('Configured Providers','已配置的提供商')),add);header.classList.add('provider-heading');root.append(header);
    if(!(providers.profiles||[]).length)empty(root,tr('Connect an AI provider, then create an assistant.','先连接 AI 提供商，再创建助手。'));
    for(const profile of providers.profiles||[]){const row=n('div','page-card provider-summary'),info=n('div','row-info'),connection=(providers.connections||[]).find(value=>value.profile_id===profile.id);info.append(n('span','',profile.name),n('span','hint',profile.model));
      const logo=(providers.directory||[]).find(value=>value.id===profile.model_settings?.catalog_provider)?.logo;if(logo?.startsWith('<svg')){const image=n('img','provider-logo');image.alt='';image.src=`data:image/svg+xml;charset=utf-8,${encodeURIComponent(logo)}`;row.append(image);}
      const edit=action(tr('Configure','配置'),()=>providerEditor(profile,providers,settingsScope));edit.setAttribute('aria-label',tr('Edit','编辑'));row.append(info,edit);root.append(row);
    }
    if(!client.desktop)return;
    const machines=card(tr('Spaces and connections','Space 与连接'));
    const spaces=await request('/api/v1/spaces',{signal:token.signal,owner:'',space:''});if(!gate.current(token))return;
    for(const space of spaces.items||[]) {const row=n('div','list-row');row.append(n('span','',space.name),n('span','hint',space.connected?tr('Connected','已连接'):tr('Offline','离线')));if(client.desktop&&space.kind==='remote') {for(const name of ['connect','disconnect','remove']) {const button=action(tr({connect:'Connect',disconnect:'Disconnect',remove:'Remove'}[name],{connect:'连接',disconnect:'断开',remove:'移除'}[name]),()=>operation(button,()=>request('/api/v1/spaces',{method:'POST',owner:'',space:'',body:{action:name,space_id:space.id}})));row.append(button);}}machines.append(row);}
    if(client.desktop)machines.append(action(tr('Connect Space','连接 Space'),()=>spaceEditor()));root.append(machines);

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
  async function resourceCache(record=true){
    if(!await leave())return;
    const local=(client.spaces||[]).find(space=>space.kind==='local');if(!local)return;
    if(record)history.pushState({potRoute:'settings',potResource:true,potScope:{space:local.id,owner:''}},'',location.href);const token=gate.next(),root=body(),view={token,controller:null};resourceView=view;detailParent=true;heading(tr('Resource cache','资源缓存'));
    const controller=await renderSandboxResources({client,root,scope:{space:local.id,owner:''},locale,notify});
    if(resourceView===view&&gate.current(token)){view.controller=controller;restoreView('resource-cache',token);}
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
    const pages=await across(`/api/v1/tasks/query?${new URLSearchParams({scope:filter.taskScope,q:filter.taskQuery,offset:0,limit:50,...(filter.taskTarget?{execution_target:JSON.stringify(filter.taskTarget)}:{})})}`,token,{paginate:true,perAssistant:true,spaceID:filter.taskSpace});if(!gate.current(token))return;const result=combineTasks(pages,{offset:filter.taskOffset,limit:20});
    if(filter.taskTarget)root.append(toolbar(n('span','hint',`${filter.taskTarget.runtime_id} · ${filter.taskTarget.target.kind==='host'?'Host':'Sandbox'}`),action(tr('Show all tasks','显示所有任务'),()=>{filter.taskTarget=null;filter.taskSpace='all';filter.taskOffset=0;show('tasks');})));
    const controls=toolbar();controls.classList.add('task-filters');controls.append(segments(['active','attention','archived'].map(value=>[value,`${tr({active:'Ongoing',attention:'Needs attention',archived:'Archived'}[value],{active:'进行中',attention:'需关注',archived:'已归档'}[value])} ${result.counts[value]}`]),filter.taskScope,value=>{filter.taskScope=value;filter.taskOffset=0;show('tasks');}));
    const search=field(tr('Search task title or ID…','搜索任务标题或 ID…'),filter.taskQuery,{kind:'search'});search.row.classList.add('task-search');search.input.placeholder=tr('Search task title or ID…','搜索任务标题或 ID…');search.input.onchange=()=>{filter.taskQuery=search.input.value;filter.taskOffset=0;show('tasks');};controls.append(search.row);root.append(controls);
    const tasks=n('div','task-list');tasks.dataset.scrollKey='task-list';if(!result.items?.length)empty(tasks,tr('No ongoing tasks. Describe a goal in Chat to get started.','暂无进行中的任务，在聊天中描述目标即可开始。'));
    let groupKey='';
    for(const task of result.items||[]){const key=JSON.stringify([task._space_id,task._assistant_id]);if(key!==groupKey){groupKey=key;const assistant=(client.assistants||[]).find(item=>item.id===task._assistant_id&&item.space_id===task._space_id);tasks.append(n('div','task-owner',assistant?.name||task._assistant_id));}
      const row=n('button','task-row');row.type='button';row.setAttribute('aria-label',task.title);row.disabled=task._stale;row.onclick=()=>taskDetail(task);row.title=task.title;
      const line=n('span','task-title-line'),logo=n('span','task-brand',task.executor_icon==='anthropic'?'A':'◉');line.append(logo,n('span','task-title',task.title),n('span','hint','›'));
      const detail=n('span','task-meta'),phase=n('span',`task-phase phase-${task.phase}`,tr({running:'Running',queued:'Queued',waiting:'Waiting',failed:'Failed',complete:'Complete',interrupted:'Interrupted',cancelled:'Cancelled'}[task.phase]||task.phase,{running:'运行中',queued:'排队中',waiting:'等待中',failed:'失败',complete:'已完成',interrupted:'已中断',cancelled:'已取消'}[task.phase]||task.phase));detail.append(phase,n('span','hint',`${task._space_name||''}${task.machine_id?' · '+task.machine_id:''}${task.pending?' · '+task.pending+' '+tr('pending instructions','条待处理指令'):''}${task._stale?' · '+tr('Stale','已过期'):''}`));row.append(line,detail);tasks.append(row);
    }root.append(tasks);
    const footer=toolbar(n('span','hint',`${result.total} ${tr('items','项')} · ${tr('Page','第')} ${Math.floor(filter.taskOffset/20)+1}`));footer.classList.add('pagination');footer.append(action(tr('Previous','上一页'),()=>{filter.taskOffset=Math.max(0,filter.taskOffset-20);show('tasks');},{disabled:!filter.taskOffset}),action(tr('Next','下一页'),()=>{filter.taskOffset=result.next_offset;show('tasks');},{disabled:result.next_offset==null}));root.append(footer);
  }
  async function taskDetail(task,{recordHistory=true}={}) {
    taskView=null;scheduleView=null;detailParent=true;if(recordHistory){const replace=history.state?.potTask?.id===task.id&&history.state?.potScope?.space===task._space_id;history[replace?'replaceState':'pushState']({potRoute:'tasks',potScope:{space:task._space_id,owner:task.assistant_id},potTask:{...task}},'',location.href);}
    const result=await load(async token=>request(`/api/v1/tasks/${encodeURIComponent(task.id)}`,{signal:token.signal,owner:task.assistant_id,space:task._space_id}));if(!result)return;
    const {root}=result, record=result.value;heading(task.title);
    const fileScope={owner:task.assistant_id,space:task._space_id};
    const downloadFile=async({task:id,path})=>{try{await client.downloadFile(id,path,fileScope);}catch(reason){if(gate.current(result.token))errorView(reason,root);}};
    root.append(n('p','hint',`${record.info?.phase||task.phase} · ${task.machine_id||''}`));
    if(record.error)root.append(n('p','inline-error',record.error));
    if(record.request?.text)root.append(section(tr('Goal','目标')),renderMarkdown(record.request.text,{downloadFile}));
    if(record.result)root.append(section(tr('Result','结果')),renderMarkdown(typeof record.result==='string'?record.result:JSON.stringify(record.result),{downloadFile}));
    const transcript=n('div','task-transcript message-body');root.append(transcript);
    const view={task,token:result.token,scope:fileScope,transcript,state:createTaskTranscript(),nodes:new Map(),busy:false,again:false,downloadFile};taskView=view;titleChanged();permissions();
    await refreshTaskEvents(view);
    const inputs=await request(`/api/v1/tasks/${encodeURIComponent(task.id)}/messages`,{owner:task.assistant_id,space:task._space_id});
    if(!gate.current(result.token))return;
    for(const input of list(inputs))root.append(n('p','task-input',input.text));
    const draftKey=JSON.stringify([task._space_id,task.assistant_id,task.id]),input=field(tr('Message','消息'),taskDrafts.get(draftKey)||'',{kind:'textarea'});input.input.oninput=()=>taskDrafts.set(draftKey,input.input.value);root.append(input.row);
    let guidance;
    const send=action(tr('Send guidance','发送指导'),()=>{if(!guidance||guidance.text!==input.input.value)guidance={request_id:uid(),text:input.input.value};const attempt={...guidance};return operation(send,async()=>{await request(`/api/v1/tasks/${encodeURIComponent(task.id)}/messages`,{owner:task.assistant_id,space:task._space_id,method:'POST',body:attempt});if(taskDrafts.get(draftKey)===attempt.text)taskDrafts.delete(draftKey);},()=>taskDetail(task));},{primary:true});
    const actions=toolbar(send,action(tr('Usage','用量'),()=>openUsageView({...fileScope,name:task.title,scope:task.id,returnTask:{...task}})));if((record.info?.phase||task.phase)==='running'&&record.run_id){const paused=!!record.info?.paused,control=action(paused?tr('Resume task','恢复任务'):tr('Pause task','暂停任务'),()=>operation(control,()=>request(`/api/v1/tasks/${encodeURIComponent(task.id)}/control`,{...fileScope,method:'POST',body:{run_id:record.run_id,action:paused?'resume':'pause'}}),()=>taskDetail(task)));actions.append(control);}for(const name of ['interrupt','cancel']) {const requestID=uid(),button=action(tr(name==='cancel'?'Cancel task':'Interrupt',name==='cancel'?'取消任务':'中断'),()=>operation(button,()=>request(`/api/v1/tasks/${encodeURIComponent(task.id)}/${name}`,{method:'POST',owner:task.assistant_id,space:task._space_id,body:{request_id:requestID}}),()=>taskDetail(task)),{danger:true});actions.append(button);}root.append(actions);
    if(record.files?.length)for(const file of record.files){const link=n('a','',file.name||file.path);link.href='#';link.onclick=event=>{event.preventDefault();downloadFile({task:task.id,path:file.path});};root.append(link);}
    restoreView(`task:${draftKey}`,result.token);
  }
  async function refreshTaskEvents(view=taskView) {
    if(!view||!gate.current(view.token))return;if(view.busy){view.again=true;return;}view.busy=true;
    try{
      let count=0,page;
      do{page=await request(`/api/v1/tasks/${encodeURIComponent(view.task.id)}/events?${new URLSearchParams({after:view.state.cursor,limit:100})}`,{...view.scope,signal:view.token.signal});if(!gate.current(view.token)||taskView!==view)return;const previous=view.state.cursor;appendTaskEvents(view.state,page.events);if(page.has_more&&view.state.cursor===previous)throw Error('Task event cursor did not advance');count+=(page.events||[]).length;}while(page.has_more&&count<5000);
      renderTaskTranscript(view);
      if(page.unavailable&&!view.unavailable){view.unavailable=n('p','hint',tr('Original machine is offline. Showing cached execution output.','原执行机器离线，显示缓存执行输出。'));view.transcript.prepend(view.unavailable);}else if(!page.unavailable&&view.unavailable){view.unavailable.remove();view.unavailable=null;}
      if(page.has_more)queueMicrotask(()=>refreshTaskEvents(view));
    }catch(reason){if(gate.current(view.token)&&reason.name!=='AbortError')notify(reason.message);}finally{view.busy=false;if(view.again){view.again=false;queueMicrotask(()=>refreshTaskEvents(view));}}
  }
  function renderTaskTranscript(view){
    const forest=toolForest(view.state.messages);
    for(const message of view.state.messages){
      let entry=view.nodes.get(message.id);
      if(!entry){const element=n('div',`task-event role-${message.role}`);entry={element,text:null};view.nodes.set(message.id,entry);view.transcript.append(element);}
      if(message.tool){
        if(!entry.tool){entry.tool=createToolCall({memory:toolMemory,key:JSON.stringify([view.scope,view.task.id,message.id]),locale,onDecision:decideApproval,downloadFile:view.downloadFile});entry.element.append(entry.tool.element);}
        const children=forest.byID.get(message.tool.turn_id+':'+message.tool.id)?.children||[];
        entry.tool.update(message,{approval:approvalFor(message.tool),offline:!!view.unavailable,children:children.map(entry=>entry.message)});
        const parent=forest.byID.get(message.tool.turn_id+':'+message.tool.parent_id);
        if(parent&&parent.children.some(entry=>entry.message.id===message.id)&&view.nodes.get(parent.message.id)?.tool)view.nodes.get(parent.message.id).tool.children.append(entry.element);
      }else if(entry.text!==message.text){entry.element.replaceChildren(renderMarkdown(message.text,{downloadFile:view.downloadFile}));entry.text=message.text;}
    }
  }
  function approvalFor(tool){const scope=approvalScope();return pendingApprovals.find(item=>item.request.owner===scope.owner&&(item.request.task||'')===(taskView?.task.id||'')&&item.request.call_id===tool.id&&item.request.turn===tool.turn_id);}
  async function decideApproval(approval,action){
    const captured={...approvalScope()};
    if(approval.request.owner!==captured.owner||(approval.request.task||'')!==(taskView?.task.id||''))throw Error(tr('Approval context changed.','授权上下文已改变。'));
    await request('/api/v1/permissions',{...captured,method:'POST',body:{id:approval.id,action}});
    await permissions();
  }
  async function schedules(root, token) {
    root.classList.add('schedules-content');
    const pages=await across('/api/v1/schedules?limit=50',token,{paginate:true,perAssistant:true});if(!gate.current(token))return;
    const items=pages.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner)).sort((a,b)=>(a.next_at_ms||0)-(b.next_at_ms||0)||a._assistant_id.localeCompare(b._assistant_id)||a.id.localeCompare(b.id));
    filter.scheduleOffset=Math.min(filter.scheduleOffset,Math.max(0,Math.floor((items.length-1)/20)*20));
    const rows=n('div','schedule-list');rows.dataset.scrollKey='schedule-list';if(!items.length)empty(rows,tr('No schedules','暂无定时计划'));
    for(const item of items.slice(filter.scheduleOffset,filter.scheduleOffset+20)) {
      const row=n('div','schedule-card page-card'),button=action(item.spec.title,()=>scheduleHistory(item),{disabled:item._stale});button.classList.add('schedule-title');
      const owner=n('span','schedule-owner'),assistant=(client.assistants||[]).find(owner=>owner.id===item._assistant_id&&owner.space_id===item._space_id),space=(client.spaces||[]).find(space=>space.id===item._space_id);
      owner.append(n('span','',assistant?.name||item._assistant_id));owner.title=item._space_name||'';
      owner.append(icon(space?.kind==='local'&&!space?.remote?'machines':'cloud'));
      row.append(button,owner,n('span','hint',item._stale?tr('Stale','已过期'):item.spec.enabled?item.next_label||tr('Not scheduled','未安排'):tr('Paused','已暂停')));rows.append(row);
    }
    root.append(rows);
    const footer=n('div','schedule-pagination'),buttons=toolbar(action(tr('Previous','上一页'),()=>{filter.scheduleOffset=Math.max(0,filter.scheduleOffset-20);show('tasks');},{disabled:!filter.scheduleOffset}),action(tr('Next','下一页'),()=>{filter.scheduleOffset+=20;show('tasks');},{disabled:filter.scheduleOffset+20>=items.length}));
    footer.append(n('span','hint',`${items.length} · ${Math.floor(filter.scheduleOffset/20)+1}`),buttons);root.append(footer);
  }
  async function scheduleHistory(item,record=true,runsOffset=0) {
    detailParent=true;filter.taskTab='schedules';if(record)history.pushState({potRoute:'tasks',potSchedule:{...item},potScope:{space:item._space_id,owner:item._assistant_id}},'',location.href);
    const result=await load(token=>request(`/api/v1/schedules?${new URLSearchParams({selected:item.id,runs_offset:runsOffset,limit:20})}`,{signal:token.signal,space:item._space_id,owner:item._assistant_id}));if(!result)return;
    if(!result.value.selected||result.value.selected.deleted){scheduleView=null;return show('tasks');}
    const selected={...item,...result.value.selected},view={item:selected,runsOffset};scheduleView=view;
    const {root}=result;heading(selected.spec.title);const info=card(tr('Schedule details','定时计划详情'));
    info.append(n('p','hint',selected.spec.goal),n('p','hint',`${selected.spec.timezone||''} · ${selected.next_label||tr('Not scheduled','未安排')}`));if(selected.last_error)info.append(n('p','inline-error',selected.last_error));
    const actions=toolbar();let busy=false,confirmed=false;
    for(const name of ['run',selected.spec.enabled?'pause':'resume','edit','delete']) {
      const writeKey=JSON.stringify([selected._space_id,selected._assistant_id,selected.id,name]);
      const button=action(tr({run:'Run now',pause:'Pause',resume:'Resume',edit:'Edit',delete:'Remove'}[name],{run:'立即运行',pause:'暂停',resume:'恢复',edit:'编辑',delete:'移除'}[name]),async()=>{
        if(busy||scheduleView!==view||!gate.current(result.token))return;
        if(name==='edit'){scheduleEditor(selected);return;}
        if(name==='delete'&&!confirmed){confirmed=true;button.textContent=tr('Confirm removal','确认移除');return;}
        busy=true;for(const control of actions.querySelectorAll('button'))control.disabled=true;
        const body=scheduleWrites.get(writeKey)||{request_id:uid(),action:name,schedule_id:selected.id};scheduleWrites.set(writeKey,body);
        try{await request('/api/v1/schedules',{method:'POST',space:selected._space_id,owner:selected._assistant_id,body});scheduleWrites.delete(writeKey);if(scheduleView===view&&gate.current(result.token)){if(name==='delete'){scheduleView=null;await show('tasks');}else await scheduleHistory(selected,false,runsOffset);}}
        catch(reason){if(scheduleView===view&&gate.current(result.token))errorView(reason,root);}
        finally{busy=false;for(const control of actions.querySelectorAll('button'))control.disabled=false;}
      },{primary:name==='run',danger:name==='delete',disabled:selected._stale});actions.append(button);
    }
    info.append(actions,n('p','hint',tr('Pausing or removing only affects future runs.','暂停或移除只影响未来的执行。')));root.append(info,n('p','hint',tr('Run history','运行历史')));
    const rows=(result.value.runs||[]).map(run=>{const summary=n('span','',run.summary);if(run.task_id)summary.append(action(tr('View task','查看任务'),()=>taskDetail({id:run.task_id,title:run.summary||selected.spec.title,assistant_id:selected._assistant_id,_assistant_id:selected._assistant_id,_space_id:selected._space_id})));return [run.scheduled_label,run.state,summary];});
    root.append(table([tr('Scheduled','计划时间'),tr('State','状态'),tr('Summary','摘要')],rows));
    root.append(toolbar(action(tr('Newer entries','较新记录'),()=>scheduleHistory(selected,false,Math.max(0,runsOffset-20)),{disabled:!runsOffset}),action(tr('Older entries','较早记录'),()=>scheduleHistory(selected,false,result.value.runs_next_offset),{disabled:result.value.runs_next_offset==null})));
    restoreView(JSON.stringify(['schedule-history',item._space_id,item._assistant_id,item.id,runsOffset]),result.token);
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
    const locations=n('div','usage-location');locations.append(n('span','hint',tr('Location','位置')),segments([['all',tr('All locations','所有位置')],...(client.spaces||[]).map(space=>[space.id,space.name])],filter.usageSpace,value=>{filter.usageSpace=value;show('usage',{keepUsageOwner:true});}));if(!usageOwner)root.append(locations);
    const titleRow=toolbar(n('h2','page-heading',usageOwner?tr('Total usage','总用量'):tr('Overall statistics','总体统计')));titleRow.classList.add('usage-heading');root.append(titleRow);root.append(segments([7,30,90,365].map(days=>[days,days===365?tr('Last year','最近一年'):tr(`Last ${days} days`,`最近 ${days} 天`)]),filter.usageDays,days=>{filter.usageDays=days;filter.usageOffset=0;show('usage',{keepUsageOwner:true});}));
    const timezone=Intl.DateTimeFormat().resolvedOptions().timeZone;
    const pages=await across(`/api/v1/usage/summary?${new URLSearchParams({days:filter.usageDays,timezone,scope:usageOwner?.scope||usageOwner?.owner||'',offset:filter.usageOffset})}`,token,{spaceID:usageOwner?.space||filter.usageSpace,ownerID:usageOwner?.owner||'',allModels:!usageOwner});if(!gate.current(token))return;const data=pages.length===1?{...pages[0].value,spaces:pages}:combineUsage(pages);
    if(usageOwner)root.insertBefore(n('p','hint',usageOwner.name),root.firstChild.nextSibling);
    const compact=value=>Number(value||0)>=1000000?(Number(value)/1000000).toFixed(1)+'M':Number(value||0)>=1000?(Number(value)/1000).toFixed(1)+'K':String(Number(value||0));
    const total=(usageOwner?data.lifetime||data.total:data.total)||{},metrics=n('div','usage-metrics'),totalCard=n('section','page-card usage-total');
    for(const [label,value]of [[tr('Total tokens','总 token'),total.input+total.output],[tr('Input','输入'),total.input],[tr('Output','输出'),total.output]]){const item=n('div','metric');item.append(n('span','hint',label),n('strong','',compact(value)));metrics.append(item);}totalCard.append(metrics,n('p','hint',`${tr('Cached input','缓存输入')}: ${compact(total.cache)} tokens · ${tr('Usage records','用量记录')}: ${total.requests||0}${total.unknown?' · '+total.unknown+' '+tr('incomplete record; totals may be incomplete','条不完整记录，总量可能不完整'):''}`));root.append(totalCard);
    const routing=data.routing_total||{},routingCard=card(tr('Main chat usage including task routing','主聊天用量（含任务路由）'));routingCard.append(n('p','usage-number',`${compact((routing.input||0)+(routing.output||0))} tokens`),n('p','hint',`${tr('Input','输入')} ${compact(routing.input)} · ${tr('Output','输出')} ${compact(routing.output)} · ${tr('Cached input','缓存输入')} ${compact(routing.cache)}`),n('p','hint',tr('Includes the entire main chat turn that delegated a task, including understanding and responding. Already included in totals; this is not a separate routing fee.','包含委派任务的整个主聊天回合（包括理解和回复），已计入总量，不单独收取路由费用。')));root.append(routingCard);
    const models=card(tr('Agent / model usage','Agent / 模型用量'));for(const model of data.agent_models||[]){const row=n('div','usage-model-row'),line=n('div','usage-model-title');line.append(n('span','',`${model.agent==='pot'?'Pot':model.agent} · ${model.model}`),n('span','',`${compact(model.input+model.output)} tokens`));row.append(line,n('p','hint',`${tr('Input','输入')} ${compact(model.input)} · ${tr('Output','输出')} ${compact(model.output)} · ${tr('Cached input','缓存输入')} ${compact(model.cache)}${model.unknown?' · '+model.unknown+' '+tr('incomplete usage record','条不完整用量记录'):''}`));models.append(row);}if(!data.agent_models?.length)models.append(n('p','hint',tr('No usage records yet','暂无用量记录')));root.append(models);
    const chart=n('div','usage-chart');chart.setAttribute('role','img');chart.setAttribute('aria-label',tr('Daily token usage','每日 token 用量'));
    const maximum=Math.max(1,...(data.buckets||[]).map(b=>b.input+b.output));
    for(const bucket of data.buckets||[]){const column=n('div','chart-column'),bar=n('div','chart-bar');bar.style.height=`${Math.max(0,(bucket.input+bucket.output)/maximum*140)}px`;bar.title=`${bucket.label}: ${bucket.input+bucket.output}`;column.append(bar,n('small','',bucket.label));chart.append(column);}const chartCard=n('section','page-card');chartCard.append(n('p','hint',`${tr('One day per bar','每条代表一天')} · ${data.timezone||timezone} · ${tr('Today is still in progress','今天尚未结束')}`),chart);root.append(chartCard);
    for(const page of pages) {
      const value=page.value,owner={space:page.space.id,owner:usageOwner?.owner||''};
      const policy=value.quota?.policy||await request('/api/v1/usage/policy',{...owner,signal:token.signal});if(!gate.current(token))return;
      if(pages.length===1)titleRow.append(action(tr('Usage limits','用量限制'),()=>policyEditor(policy,owner),{disabled:page.stale}));
      const allowance=card(`${tr('Token allowance','Token 配额')} · ${page.space.name}`);allowance.append(n('p','hint',value.quota?.active?`${Number(value.quota.remaining).toLocaleString(locale())} ${tr('remaining','剩余')} · ${value.quota.reset_label||''}`:tr('No active token limit','未启用 token 限额')),action(tr('Edit allowance','编辑配额'),()=>policyEditor(policy,owner),{disabled:page.stale}));root.append(allowance);
      if(usageOwner&&!usageOwner.returnTask)for(const mail of value.mail_waits||[]){const waiting=section(tr('Correspondence waiting for allowance','助手通信正在等待配额'));waiting.append(n('p','hint',mail.provider||''));const requestID=uid(),resume=action(tr('Resume correspondence','恢复助手通信'),()=>operation(resume,()=>request('/api/v1/usage/resume',{...owner,method:'POST',body:{request_id:requestID,scope:mail.scope,turn:mail.turn,since:mail.since}}),()=>show('usage',{keepUsageOwner:true})),{disabled:page.stale});waiting.append(resume);root.append(waiting);}
      if(value.waiting){const waiting=section(tr('Waiting for allowance','等待配额'));waiting.append(n('p','hint',value.waiting.provider||''));for(const name of ['resume','cancel']){const requestID=uid(),task=usageOwner?.returnTask;const button=action(tr(name==='resume'?'Resume':'Cancel waiting',name==='resume'?'恢复':'取消等待'),()=>operation(button,async()=>{if(task)return request(`/api/v1/tasks/${encodeURIComponent(task.id)}/${name}`,{...owner,method:'POST',body:{request_id:requestID,...(name==='resume'?{turn:value.waiting.turn,since:value.waiting.since}:{})}});if(name==='resume')return request('/api/v1/usage/resume',{...owner,method:'POST',body:{request_id:requestID,turn:value.waiting.turn,since:value.waiting.since}});const snapshot=await request('/api/v1/snapshot',owner),message=snapshot.messages?.find(message=>message.id===snapshot.turn?.user_message_id);if(!message||message.request_id!==value.waiting.turn)throw Error(tr('Allowance wait changed. Refresh Usage.','配额等待已变化，请刷新用量页面。'));return request('/api/v1/turn/cancel',{...owner,method:'POST',body:{request_id:requestID,user_message_id:message.id}});},()=>show('usage',{keepUsageOwner:true})),{disabled:page.stale||!usageOwner});waiting.append(button);}root.append(waiting);}
    }
    if(usageOwner){root.append(section(tr('Model requests','模型请求')),table([tr('Time','时间'),tr('Model','模型'),tr('Input','输入'),tr('Output','输出'),tr('State','状态')],(data.requests||[]).map(record=>[record.label,record.model,record.input,record.output,record.state])));
    const next=data.next_offset??data.agent_model_next_offset??data.compaction_next_offset;if(filter.usageOffset||next!=null)root.append(toolbar(action(tr('Previous page','上一页'),()=>{filter.usageOffset=Math.max(0,filter.usageOffset-20);show('usage',{keepUsageOwner:true});},{disabled:!filter.usageOffset}),action(tr('Next page','下一页'),()=>{filter.usageOffset=next;show('usage',{keepUsageOwner:true});},{disabled:next==null})));
    root.append(section(tr('Tools','工具')),table([tr('Tool','工具'),tr('Calls','调用'),tr('Failed','失败'),tr('Duration (ms)','耗时（毫秒）')],(data.tools||[]).map(tool=>[tool.name,tool.calls,tool.failed,tool.duration_ms])));
    root.append(section(tr('Context compaction','上下文压缩')),table([tr('Time','时间'),tr('State','状态'),tr('Before','之前'),tr('After','之后')],(data.compactions||[]).map(record=>[record.label,record.state,record.tokens_before,record.tokens_after])));}
  }
  function policyEditor(policy,owner) {
    renderEditor(tr('Token allowance','Token 配额'),policy,[
      {name:'period',label:tr('Period','周期'),choices:[['none',tr('Unlimited','无限制')],['day',tr('Daily','每日')],['week',tr('Weekly','每周')]]},
      {name:'limit',label:tr('Token limit','Token 上限'),kind:'number',parse:Number,required:true},{name:'timezone',label:tr('Time zone','时区'),required:true},{name:'auto_resume',label:tr('Automatically resume','自动恢复'),kind:'checkbox'},
    ],(values,context)=>request('/api/v1/usage/policy',{...context,method:'POST',body:values}),{capturedScope:owner});
  }
  async function machines(root, token) {
    const pages=await across('/api/v1/machines',token);if(!gate.current(token))return;const result={items:pages.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner))};
    root.append(n('p','hint',tr('Machines','机器')));if(!list(result).length)empty(root,tr('No machines','暂无机器'));
    let machineSpace='';for(const machine of list(result)) {if(machineSpace!==machine._space_id){machineSpace=machine._space_id;root.append(n('p','hint machine-location',machine._space_name));}
      const machineKey=JSON.stringify([machine._space_id,machine.id]),group=n('section','page-card machine-card'),header=n('div','machine-header');const machineIcon=n('span','machine-icon');machineIcon.append(icon('machines'));header.append(machineIcon,n('span','machine-name',machine.name||machine.runtime_id||machine.id),n('span',machine.connected?'success-text':'hint',machine._stale?tr('Stale','已过期'):machine.connected?tr('Connected','已连接'):tr('Offline','离线')),action('',()=>{if(expandedMachines.has(machineKey))expandedMachines.delete(machineKey);else expandedMachines.add(machineKey);show('machines');}));header.lastChild.setAttribute('aria-label',tr('Machine settings','机器设置'));header.lastChild.append(icon('settings'));group.dataset.machineId=machine.runtime_id||machine.id;group.dataset.spaceId=machine._space_id;group.append(header,n('p','hint',(machine.tags||[]).join(' · ')));const settings=n('div','machine-settings');settings.hidden=!expandedMachines.has(machineKey);group.append(settings);
      const capabilities=machine.capabilities||{};settings.append(n('p','hint',Object.entries(capabilities).filter(([,value])=>value===true).map(([key])=>key).join(' · ')));
      const policy=machine.policy||{execution_mode:'sandbox',host_access:'ask'};
      const mode=field(tr('Execution mode','执行模式'),policy.execution_mode,{choices:[['sandbox','Sandbox'],['host','Host']]}),hostAccess=field(tr('Host access','Host 访问'),policy.host_access,{choices:[['ask',tr('Ask each time','每次询问')],['allow',tr('Allow','允许')],['deny',tr('Deny','拒绝')]]});settings.append(mode.row,hostAccess.row);
      const save=action(tr('Save permissions','保存权限'),()=>operation(save,()=>request('/api/v1/permissions',{method:'POST',space:machine._space_id,owner:'',body:{action:'configure',machine_id:machine.runtime_id||machine.id,machine_policy:{execution_mode:mode.input.value,host_access:hostAccess.input.value}}})),{disabled:!machine.connected||machine._stale});settings.append(save);const hostTarget=n('details','machine-target');hostTarget.dataset.cacheKey=machineKey+':host';hostTarget.append(targetSummary(`Host · ${machine.connected?tr('Connected','已连接'):tr('Offline','离线')}`),action(tr('View Host tasks','查看 Host 任务'),()=>machineTasks(machine,'host')));group.append(hostTarget);
      if(machine.sandbox) {const sandbox=machine.sandbox,target=n('details','machine-target');target.dataset.cacheKey=machineKey+':sandbox';target.append(targetSummary('Sandbox'),n('p','hint',`${sandbox.state?.phase||sandbox.state||''}${sandbox.quarantined?' · '+tr('Quarantined','已隔离'):''}`));
        const actions=toolbar(action(tr('View Sandbox tasks','查看 Sandbox 任务'),()=>machineTasks(machine,'sandbox')));for(const name of ['start','stop','rebuild','recover']){const button=action(tr({start:'Start',stop:'Stop',rebuild:'Rebuild',recover:'Recover'}[name],{start:'启动',stop:'停止',rebuild:'重建',recover:'恢复'}[name]),()=>operation(button,()=>{const work=()=>request('/api/v1/machines',{method:'POST',space:machine._space_id,owner:'',body:{action:name,machine_id:machine.id,sandbox_id:sandbox.id}});return ['start','rebuild'].includes(name)&&client.desktop&&(machine.runtime_id||machine.id)===(client.runtime.runtime_id||client.runtime.id)?withResourceConsent({space:machine._space_id,sandboxID:sandbox.id,work}):work();}),{disabled:!machine.connected||machine._stale,danger:name==='rebuild'});actions.append(button);}target.append(actions);
        if(sandbox.resources) {target.append(n('p','hint',`${sandbox.resources.cpus} CPU · ${sandbox.resources.memory_gib} GiB · ${sandbox.resources.disk_gib?`${sandbox.resources.disk_gib} GiB`:tr('Shared storage','共享存储')}`));target.append(action(tr('Configure resources','配置资源'),()=>resourceEditor(machine),{disabled:!machine.connected||machine._stale}));}
        group.append(target);
      }
      if(capabilities.desktop)group.append(action(tr('Open desktop','打开桌面'),()=>desktop(machine),{disabled:!machine.connected||machine._stale||!client.openDesktop||machine._space_id!==(client.spaces||[]).find(space=>space.kind==='local')?.id||(machine.runtime_id||machine.id)!==(client.runtime.runtime_id||client.runtime.id)}));root.append(group);
    }
    await renderMachineMembership({client,root,machines:list(result),locale,notify,refresh:()=>show('machines'),edit:({title,initial,fields,save,scope})=>renderEditor(title,initial,fields,save,{capturedScope:scope}),signal:token.signal});
  }
  function resourceEditor(machine) {
    renderEditor(tr('Sandbox resources','Sandbox 资源'),machine.sandbox.resources,[
      {name:'cpus',label:'CPU',kind:'number',parse:Number,required:true},{name:'memory_gib',label:tr('Memory (GiB)','内存（GiB）'),kind:'number',parse:Number,required:true},...(machine.sandbox.resources.disk_gib?[{name:'disk_gib',label:tr('Disk (GiB)','磁盘（GiB）'),kind:'number',parse:Number,required:true}]:[]),
    ],(values,context)=>request('/api/v1/machines',{...context,method:'POST',body:{action:'configure_resources',machine_id:machine.id,sandbox_id:machine.sandbox.id,resources:{...machine.sandbox.resources,...values}}}),{capturedScope:{space:machine._space_id,owner:''}});
  }
  async function desktop(machine) {
    if(client.desktop&&(machine._space_id!==client.spaces?.find(space=>space.kind==='local')?.id||(machine.runtime_id||machine.id)!==(client.runtime.runtime_id||client.runtime.id))){errorView(Error(tr('Remote desktop viewing is unavailable.','远端桌面查看暂不可用。')));return;}
    if(client.openDesktop){try{await client.openDesktop(machine.sandbox?.id||machine.id);}catch(reason){errorView(reason);}return;}
    const captured={space:machine._space_id,owner:''};let sessionPath;
    try {
      const result=await request('/api/v1/desktop/sessions',{...captured,method:'POST',body:{sandbox_id:machine.sandbox?.id||machine.id}});
      if(typeof result.path!=='string'||!/^\/desktop\/[A-Za-z0-9]+\/$/.test(result.path))throw Error(tr('Desktop transport returned an invalid display path.','桌面传输返回的显示路径无效。'));
      sessionPath=result.path;
      const root=body(),frame=n('iframe','desktop-frame');frame.src=sessionPath;frame.title=machine.name||machine.id;
      const close=action(tr('Close desktop','关闭桌面'),()=>operation(close,()=>request(`${sessionPath}api/close`,{...captured,method:'POST',body:{}}),()=>show('machines')));
      root.append(toolbar(close),frame);
    }catch(reason){if(sessionPath)request(`${sessionPath}api/close`,{...captured,method:'POST',body:{}}).catch(()=>{});errorView(reason);}
  }
  const approvalScope=()=>taskView?.scope||scope();
  async function permissions() {
    if(!approvalScope().owner||disposed){approvalGate.close();pendingApprovals=[];approvalHost.replaceChildren();onApprovalsChanged();return;}
    const token=approvalGate.next(),captured=approvalScope(),key=JSON.stringify(captured);if(approvalOwner!==key){approvalOwner=key;approvalHost.replaceChildren();}
    try {
      const value=await request('/api/v1/permissions',{...captured,signal:token.signal});if(!approvalGate.current(token)||disposed||JSON.stringify(approvalScope())!==JSON.stringify(captured))return;
      const pending=(value.pending||[]).filter(item=>(!item.request?.owner||item.request.owner===captured.owner)&&(!taskView||item.request?.task===taskView.task.id));
      pendingApprovals=pending;
      const fragment=document.createDocumentFragment();let inlineLocator=false;
      for(const approval of pending) {const toolRoot=[...(taskView?.transcript||document.querySelector('.message-column')).querySelectorAll('.tool-call')].find(node=>node.dataset.callId===approval.request.call_id&&(!node.dataset.turnId||node.dataset.turnId===approval.request.turn));
        if(toolRoot){if(inlineLocator)continue;inlineLocator=true;const locate=action(`${pending.length} ${translate(locale(),'tool.approvalPending')} · ${translate(locale(),'tool.viewApprovals')}`,()=>{toolRoot.open=true;toolRoot.scrollIntoView({block:'center',behavior:'smooth'});toolRoot.querySelector('summary')?.focus();});fragment.append(locate);continue;}
        const box=n('section','approval-card');box.append(n('strong','',tr('Request approval','请求授权')),n('p','approval-description',`${captured.owner} · ${approval.request.machine_id}\n${tr('Wants to operate this machine directly','想要直接操作这台机器')}\n\n${tr('Approve the exact operation shown below. It runs with the machine Runtime account permissions; this does not grant access to future commands.','仅批准下面展示的具体操作，以该机器 Runtime 账户的权限执行，不授权后续命令。')}`),n('pre','approval-arguments',`${approval.request.tool}\n${JSON.stringify(approval.request.args,null,2)}`));
        for(const name of ['allow_once','deny']){const button=action(tr(name==='allow_once'?'Allow this operation':'Deny',name==='allow_once'?'允许此次操作':'拒绝'),()=>operation(button,()=>request('/api/v1/permissions',{...captured,method:'POST',body:{id:approval.id,action:name}}),permissions));box.append(button);}fragment.append(box);}
      approvalHost.replaceChildren(fragment);
      if(taskView)renderTaskTranscript(taskView);onApprovalsChanged();
    }catch(reason){if(approvalGate.current(token)&&reason.name!=='AbortError')notify(reason.message);}
  }
  async function show(next,{keepUsageOwner=false}={}) {
    if(editor)return;
    captureView();viewReady=false;
    taskView=null;resourceView=null;scheduleView=null;detailParent=false;if(!keepUsageOwner)usageOwner=null;
    route=next;heading(tr({tasks:filter.taskTab==='schedules'?'Schedules':'Tasks',machines:'Machines',usage:'Usage',settings:'Settings'}[next]||next,{tasks:filter.taskTab==='schedules'?'定时计划':'任务',machines:'机器',usage:'用量',settings:'设置'}[next]||next));
    const version=++pageVersion,token=gate.next(),root=body();
    root.append(n('p','hint',tr('Loading…','正在加载…')));
    const viewKey=JSON.stringify([next,next==='tasks'?[filter.taskTab,filter.taskScope,filter.taskQuery,filter.taskOffset,filter.scheduleOffset,filter.taskTarget,filter.taskSpace]:next==='usage'?[filter.usageDays,filter.usageSpace,filter.usageOffset,usageOwner]:next==='settings'?[filter.settingsTab,configurationSpace]:[]]);
    try {root.replaceChildren();await ({settings,tasks,machines,usage}[next]||settings)(root,token);if(gate.current(token))restoreView(viewKey,token);}
    catch(reason){if(gate.current(token)&&version===pageVersion&&!disposed)errorView(reason,root);}
  }
  function openUsageView(context,record=true){usageOwner={...context};route='usage';filter.usageOffset=0;if(record)history.pushState({potRoute:'usage',potScope:{space:context.space,owner:context.owner},potUsage:{...usageOwner}},'',location.href);routeChanged('usage');return show('usage',{keepUsageOwner:true});}
  function machineTasks(machine,kind){filter.taskTarget={runtime_id:machine.runtime_id||machine.id,target:kind==='host'?{kind:'host'}:{kind:'sandbox',sandbox_id:machine.sandbox.id}};filter.taskSpace=machine._space_id;filter.taskOffset=0;filter.taskTab='tasks';route='tasks';routeChanged('tasks');history.pushState({potRoute:'tasks',potScope:scope()},'',location.href);return show('tasks');}
  const popstate=async event=>{
    if(!editor)return false;if(event.state?.potEditor===editor.id)return true;
    const current=editor;if(!await back())history.pushState({...history.state,potEditor:current.id},'',location.href);return true;
  };
  const beforeUnload=event=>{if([...(editor?[editor]:[]),...editorParents].some(value=>value.session.dirty||value.session.pending)){event.preventDefault();event.returnValue='';}};
  const preference=()=>{if(scheduleView&&!editor){scheduleHistory(scheduleView.item,false,scheduleView.runsOffset);return;}if(!editor&&route)show(route,{keepUsageOwner:!!usageOwner});};
  window.addEventListener('beforeunload',beforeUnload);window.addEventListener('pot:locale',preference);
  return {get title(){return title;},get editing(){return !!editor;},get taskOwner(){return taskView?.scope.owner||'';},get hasParent(){return !!editor||detailParent||!!usageOwner;},show,assistant,setupAI,leave,back,permissions,handleHistory:popstate,openResources:resourceCache,openSchedule:scheduleHistory,openUsage:openUsageView,clearApprovals(){approvalGate.close();approvalOwner='';pendingApprovals=[];approvalHost.replaceChildren();},approvalFor,decideApproval,conversationUsage(name,record=true){return openUsageView({...scope(),name},record);},openTask(task,record=true){route='tasks';return taskDetail(task,{recordHistory:record});},
    showProviders(){filter.settingsTab='providers';return show('settings');},
    refresh(){if(scheduleView&&!editor){scheduleHistory(scheduleView.item,false,scheduleView.runsOffset);return;}if(resourceView){resourceView.controller?.refresh();return;}if(taskView){refreshTaskEvents();return;}if(!editor&&route&&host.offsetParent)show(route,{keepUsageOwner:!!usageOwner});},
    dispose(){for(const value of [...editorParents,...(editor?[editor]:[])])value.lifecycle.dispose?.();disposed=true;gate.close();approvalGate.close();window.removeEventListener('beforeunload',beforeUnload);window.removeEventListener('pot:locale',preference);},
  };
}
