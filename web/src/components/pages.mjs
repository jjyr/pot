import {connectionBrand,taskAgentSource} from './connection-visuals.mjs';
import {createExtensions} from './extensions.mjs';
import {browserTimezone,connectScheduleEditor} from './schedule-editor.mjs';
import {usageCalendar} from './usage-calendar.mjs';
import {notificationSetting} from './notification-setting.mjs';
import {usageNumbers,usageRows,usageTooltip,createUsageList} from './usage.mjs';
import {createTaskConversation} from './task-conversation.mjs';
import {createProviderSave,saveAllowanceConfiguration} from '../state/provider-save.mjs';
import {connectProviderEditor} from './provider-connection.mjs';
import {avatar,avatarFromFile} from './avatar.mjs';
import {translate} from '../locales/messages.mjs';
import {icon} from './icons.mjs';
import {node as n, action, field, section, table, details} from './primitives.mjs';
import {modelDefaults,createModelInputDiscovery,providerConversationModels} from '../state/models.mjs';
import {ownedItems,combineTasks,combineUsage} from '../state/aggregate.mjs';
import {createTaskPages} from '../state/task-pages.mjs';
import {bindSearch} from './search.mjs';
import {createEditorSession, createRequestGate} from '../state/editor.mjs';
import {createTaskTranscript,appendTaskEvents} from '../state/task-transcript.mjs';
import {forgetBrowserAccess,configureBrowserAccess,requestPWAInstall} from '../adapters/access.mjs';
import {renderMarkdown} from './markdown.mjs';
import {renderAbout} from './about.mjs';
import {createRefreshQueue,relevantChange} from '../state/refresh.mjs';
import {reconcile,keyedRows,setDisabled,setPending} from './reconcile.mjs';
import {createProviderList,createTaskSettings} from './settings-lists.mjs';
import {createIntegrations} from './integrations.mjs';
import {renderMachinePermissions} from './execution-permissions.mjs';
import {createApprovalCard} from './approval-card.mjs';
import {createChatApprovals} from './chat-approvals.mjs';
import {taskElapsed,bindTaskClock} from './task-time.mjs';
import {createTopology} from './topology.mjs';
import {createAgentLinks} from './agent-links.mjs';
import {projectTopology,agentNodeID,machineDisplayName,assistantActivityState} from '../state/topology.mjs';

export function createPages({onApprovalsChanged=()=>{},createLocalAssistant=()=>{},client, host, approvalHost, chatApprovalHost, chatVisible, locale, notify, changed, titleChanged, openChat, routeChanged=()=>{}}) {
  const gate = createRequestGate(), approvalGate = createRequestGate();
  let assistantStartRefresh=null;
  let route = '', title = '', editor = null, disposed = false, pageVersion = 0, configurationSpace = '',taskView=null,taskOrigin=null,taskListView=null,scheduleView=null,detailParent=false,usageOwner=null;
  let filter = {taskScope:'active', taskQuery:'', taskOffset:0, scheduleOffset:0, taskTab:'tasks', usageScope:'all', usageSpace:'all',settingsTab:'general'};
  let pendingApprovals=[],notificationSettings=null;
  const taskDetails=new Map(),resourceBoots=new Map();
  const providerAllowanceMemory=new Map(),toolMemory=new Map(),usageOffsets=new Map(),usageCache=new Map(),approvalCards=new Map(),taskNames=new Map();
  const chatApprovals=createChatApprovals({host:chatApprovalHost,locale,visible:chatVisible});
  const cached = new Map(),spaceCache = new Map(),machineDrafts=new Map(),editorParents=[],taskDrafts=new Map(),taskDraftVersions=new Map(),taskWrites=new Map(),scheduleWrites=new Map();
  let extensionUI=null;const extensionMemory={};
  let pageView=null,currentViewKey='',viewReady=false,shortcutOpen=null,approvalOwner='',topologySelection='',agentOwnerSpace='';
  const tr = (en, zh) => locale() === 'zh-CN' ? zh : en;
  const scope = () => client.scope();
  const readTaskPages=createTaskPages(client);
  const request = (path, options = {}) => client.request(path, options);
  const read = (path,options={}) => (client.read||client.request)(path,options);
  const stopResources=client.resources?.subscribe((meta,error)=>{
    if(disposed||editor||!host.offsetParent)return;
    if(pageView&&!relevantChange(meta.topics,pageView.topics,pageView.space,{space:meta.space}))return;
    if(taskListView&&!meta.topics.includes('tasks'))return;
    if(error){const target=pageView?.root||taskListView?.rows;if(target){let status=target.querySelector('.resource-refresh-error');if(!status){status=n('p','inline-error resource-refresh-error');status.setAttribute('role','alert');target.prepend(status);}status.dataset.resource=JSON.stringify(meta);status.replaceChildren(n('span','',translate(locale(),'loading.stale')),action(translate(locale(),'retry'),()=>{client.resources.invalidate(meta.topics,{space:meta.space});if(pageView)pageView.queue.run();else refreshTaskList();}));}return;}
    const failure=host.querySelector('.resource-refresh-error');if(failure?.dataset.resource===JSON.stringify(meta))failure.remove();
    if(pageView&&relevantChange(meta.topics,pageView.topics,pageView.space,{space:meta.space}))pageView.queue.run();
    else if(taskListView&&meta.topics.includes('tasks'))refreshTaskList();
  });
  const uid = () => crypto.randomUUID();
  const list = value => Array.isArray(value) ? value : value?.items || [];
  function captureView(){if(!editor&&viewReady&&currentViewKey&&host.offsetParent)cached.set(currentViewKey,{scroll:host.scrollTop,expanded:[...host.querySelectorAll('details[data-cache-key][open]')].map(node=>node.dataset.cacheKey),scrolls:[...host.querySelectorAll('[data-scroll-key]')].map(node=>({key:node.dataset.scrollKey,top:node.scrollTop,left:node.scrollLeft}))});}
  function restoreView(key,token){currentViewKey=key;viewReady=true;requestAnimationFrame(()=>{if(!gate.current(token)||key!==currentViewKey)return;const value=cached.get(key);if(!value){host.scrollTop=0;taskView?.ui.start();return;}host.scrollTop=value.scroll;for(const node of host.querySelectorAll('details[data-cache-key]'))if(node.dataset.cacheKey!=='keyboard-shortcuts')node.open=value.expanded.includes(node.dataset.cacheKey);for(const node of host.querySelectorAll('[data-scroll-key]')){const saved=value.scrolls?.find(item=>item.key===node.dataset.scrollKey);if(saved){node.scrollTop=saved.top;node.scrollLeft=saved.left;}}taskView?.ui.restore();});}
  async function across(path, token, {spaceID='all', paginate=false,perAssistant=false,ownerID='',errorTarget=null}={}) {
    const spaces=(client.spaces||[]).filter(space=>spaceID==='all'||space.id===spaceID);
    if(!spaces.length)throw Error(tr('Space is unavailable.','Space 不可用。'));
    const targets=spaces.flatMap(space=>perAssistant?(client.assistants||[]).filter(item=>!item.origin&&item.space_id===space.id).map(item=>({space,owner:item.id})):[{space,owner:ownerID}]);
    const results=await Promise.allSettled(targets.map(async({space,owner})=>{
      const key=JSON.stringify([space.id,space.control_id,owner,path]);
      try {
        const shared=paginate&&client.resources;
        const options={owner,space:space.id,signal:shared?undefined:token.signal};
        const readPages=async()=>{
          let value=await (paginate?request:read)(path,options);
          if(paginate&&value?.next_offset!=null) {
            let next=value.next_offset,seen=new Set();const items=[...(value.items||[])];
            while(next!=null&&items.length<5000) {if(seen.has(next))throw Error('Invalid pagination cursor');seen.add(next);const url=new URL(path,location.origin);url.searchParams.set('offset',next);const page=await request(url.pathname+url.search,options);items.push(...(page.items||[]));next=page.next_offset;}
            if(next!=null)throw Error(tr('Too many records. Narrow the filter.','记录过多，请缩小筛选范围。'));value={...value,items,next_offset:null};
          }
          return value;
        };
        const value=shared?await client.resources.collection(path,{...options,signal:token.signal},readPages):await readPages();

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
  function connectView(root,token,read,update,{topics=[],space='',invalidate}={}) {
    const status=n('div','hint page-refresh-status',tr('Loading…','正在加载…'));status.setAttribute('role','status');root.prepend(status);
    const view={root,token,topics,space,invalidate};pageView=view;
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
  async function confirmAssistantDelete(name) {
    return new Promise(resolve=>{
      const dialog=n('dialog','preferences'),title=n('h2'),description=n('p','hint');
      const finish=value=>{dialog.close();resolve(value);};
      const cancel=action('',()=>finish(false)),confirm=action('',()=>finish(true),{danger:true});
      const refresh=()=>{title.textContent=translate(locale(),'assistant.deleteTitle').replace('{name}',name);description.textContent=translate(locale(),'assistant.deleteBody');cancel.textContent=translate(locale(),'assistant.deleteCancel');confirm.textContent=translate(locale(),'assistant.delete');};
      title.id='assistant-delete-title';dialog.setAttribute('aria-labelledby',title.id);description.id='assistant-delete-description';dialog.setAttribute('aria-describedby',description.id);
      dialog.append(title,description,toolbar(cancel,confirm));document.body.append(dialog);refresh();
      window.addEventListener('pot:locale',refresh);
      dialog.addEventListener('cancel',event=>{event.preventDefault();finish(false);});
      dialog.addEventListener('close',()=>{window.removeEventListener('pot:locale',refresh);dialog.remove();},{once:true});dialog.showModal();cancel.focus();
    });
  }
  async function leave({toParent=false}={}) {
 if(extensionUI&&!await extensionUI.leave())return false;extensionUI?.dispose();extensionUI=null;
    captureView();assistantStartRefresh=null;
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
    const controls=new Map(),lifecycle={setTitle(value){if(editor?.session===session){editor.label=value;heading(value);}}};
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
      status.textContent='';delete status.dataset.errorKey;for(const control of controls.values())control.disabled=true;cancel.disabled=submit.disabled=true;
      try {
        if(client.runtime.boot_id!==session.context.boot)throw Error(tr('Runtime identity changed. Reopen this editor.','运行时身份已变化，请重新打开编辑器。'));
        const saved=await save(attempt.values,{...captured,request_id:attempt.request_id},session);
        session.settle(true);changed(saved);if(lifecycle.afterSave)await lifecycle.afterSave(saved);else await back();
      } catch(reason) {session.settle(false);if(reason.uiKey){status.dataset.errorKey=reason.uiKey;status.replaceChildren(n('span','',translate(locale(),reason.uiKey)));if(reason.diagnostic){const details=n('details','detail-disclosure');details.append(n('summary','',translate(locale(),'agentLinks.diagnostics')),n('pre','',reason.diagnostic));status.append(details);}}else status.textContent=reason.message||String(reason);}
      finally {for(const control of controls.values())control.disabled=false;cancel.disabled=submit.disabled=false;lifecycle.settled?.();}
    };
    return {session,root,form,controls,lifecycle};
  }
  async function back({skipPicker=false}={}) {
    if(!editor&&route==='add-assistant'){await leave();route='';await openChat({directory:true});return true;}
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
      const [providers,thread,rules]=await Promise.all([read('/api/v1/providers',captured),request('/api/v1/agent',captured),request('/api/v1/instructions',captured)]);
      if(!gate.current(token)||disposed)return;
      let profiles=providers.profiles||[],revision=definition?.revision,generation=thread?.generation||0,configurationRevision=thread?.revision||0,rulesRevision=rules?.revision||'';
      const selection=thread?.selection||{},profileID=selection.profile_id||thread?.config?.source_profile_id||providers.default_provider_id||'',profile=profiles.find(value=>value.id===profileID);
      const initial={space_id:captured.space,name:definition.name,avatar:definition.avatar||'',profile_id:profileID,model:selection.model||thread?.config?.model||profile?.model||'',model_selection:'',context_window:selection.context_window??null,max_output_tokens:selection.max_output_tokens??null,reasoning_effort:selection.reasoning_effort||'',text:rules?.text||''};
      const selectValue=values=>{const profile=profiles.find(value=>value.id===values.profile_id);return profile?{profile_id:profile.id,profile_revision:profile.revision,model:values.model,context_window:values.context_window||null,max_output_tokens:values.max_output_tokens||null,reasoning_effort:values.reasoning_effort.trim()||null}:null;};
      let initialSelection=JSON.stringify({...selectValue(initial),profile_revision:selection.profile_revision||profile?.revision});
      const form=renderEditor(tr('Assistant settings','助手设置'),initial,[
        {name:'name',label:tr('Name','名称'),required:true},
        {name:'profile_id',label:tr('Provider','提供商'),required:true,choices:[['',tr('Select a Provider','选择提供商')],...profiles.map(value=>[value.id,value.name])]},
        {name:'model_selection',label:tr('Model','模型'),choices:[['',tr('Enter model ID','输入模型 ID')]]},
        {name:'model',label:tr('Model ID','模型 ID')},
        {name:'context_window',label:tr('Context window','上下文窗口'),kind:'number',parse:value=>value?Number(value):null,min:1,presets:[32000,64000,128000,200000,1000000]},
        {name:'max_output_tokens',label:tr('Maximum output tokens','最大输出 token'),kind:'number',parse:value=>value?Number(value):null,min:1,presets:[4000,8000,16000,32000]},
        {name:'reasoning_effort',label:tr('Reasoning effort','推理强度')},
        {name:'text',label:translate(locale(),'assistant.personality'),kind:'textarea',help:translate(locale(),'assistant.personalityHelp')},
      ],async(values,context,session)=>{
        const options={owner:captured.owner,space:captured.space,method:'POST'},selection=selectValue(values);
        if(!selection)throw Error(tr('Connect and select a Provider first.','请先连接并选择提供商。'));
        if(values.name!==initial.name||values.avatar!==initial.avatar){const saved=await request(`/api/v1/assistants/${encodeURIComponent(definition.id)}`,{...options,body:{name:values.name,...(values.avatar!==initial.avatar?{avatar:values.avatar}:{}),revision}});revision=saved.revision;initial.name=values.name;initial.avatar=values.avatar;session.acknowledge({name:values.name,avatar:values.avatar});}
        const signature=JSON.stringify(selection);
        if(selection&&signature!==initialSelection){const saved=await request('/api/v1/agent',{...options,body:{request_id:context.request_id,expected_generation:generation,expected_revision:configurationRevision,selection}});generation=saved.generation;configurationRevision=saved.revision;initialSelection=signature;session.acknowledge(Object.fromEntries(['profile_id','model','model_selection','context_window','max_output_tokens','reasoning_effort'].map(key=>[key,values[key]])));}
        if(values.text!==initial.text){const saved=await request('/api/v1/instructions',{...options,body:{text:values.text,revision:rulesRevision}});rulesRevision=saved.revision;initial.text=values.text;session.acknowledge({text:values.text});}
      },{returnChat:true});
      const controls=form.controls,session=form.session;
      if(definition){
        const t=key=>translate(locale(),key),more=n('details','editor-disclosure assistant-delete');more.dataset.section='delete';
        const summary=n('summary'),remove=action('',async()=>{
          if(!session.begin())return;
          const disabled=new Map([...form.form.querySelectorAll('input,select,textarea,button')].map(node=>[node,node.disabled]));
          for(const node of disabled.keys())node.disabled=true;
          deleteError='';refreshDelete();
          try{
            if(!await confirmAssistantDelete(definition.name))return;
            if(editor?.session!==session||disposed)return;
            if(client.runtime.boot_id!==session.context.boot){deleteError='assistant.deleteRuntimeChanged';return;}
            await request(`/api/v1/assistants/${encodeURIComponent(definition.id)}/delete`,{...captured,method:'POST',body:{revision}});
            session.settle(true);await leave();route='chat';
            history.replaceState({potRoute:'chat',potScope:{space:captured.space,owner:''},potDirectory:true},'',location.href);
            routeChanged('chat');await changed({deletedAssistant:{space:captured.space,owner:definition.id}});
          }catch(reason){deleteError=reason.code==='assistant_busy'?'assistant.deleteBusy':reason.code==='assistant_changed'?'assistant.deleteChanged':'assistant.deleteFailed';}
          finally{if(session.pending)session.settle(false);for(const [node,value]of disabled)node.disabled=value;refreshDelete();}
        },{danger:true});
        const status=n('p','inline-error');status.setAttribute('role','alert');let deleteError='';
        const refreshDelete=()=>{summary.textContent=t('assistant.moreActions');remove.textContent=t('assistant.delete');status.textContent=deleteError?t(deleteError):'';status.hidden=!deleteError;};
        more.append(summary,remove,status);form.form.querySelector('.editor-fields').append(more);refreshDelete();
        window.addEventListener('pot:locale',refreshDelete);form.lifecycle.dispose=()=>window.removeEventListener('pot:locale',refreshDelete);
      }

      editor.kind='assistant';
      const preview=avatar(56,definition.avatar);
      form.form.querySelector('.editor-fields').prepend(preview);
      const editAvatar=async work=>{
        if(!session.begin())return;
        const disabled=new Map([...form.form.querySelectorAll('input,select,textarea,button')].map(node=>[node,node.disabled]));
        for(const node of disabled.keys())node.disabled=true;
        avatarError.hidden=true;
        try{const {value,image}=await work();session.settle(false);session.set('avatar',value);preview.replaceChildren(...avatar(56,image).childNodes);}
        catch{session.settle(false);avatarError.hidden=false;avatarError.textContent=translate(locale(),'assistant.avatarFailed');}
        finally{for(const [node,value]of disabled)node.disabled=value;}
      };
      const avatarActions=toolbar(),upload=n('input');upload.type='file';upload.accept='image/png,image/jpeg,image/webp';upload.hidden=true;
      const changeAvatar=action('',()=>upload.click()),resetAvatar=action('',()=>editAvatar(async()=>{
        const generated=await request('/api/v1/assistants/default-avatar',captured);return {value:'',image:generated.avatar};
      }));
      const avatarHelp=n('p','hint'),avatarError=n('p','inline-error');avatarError.setAttribute('role','alert');avatarError.hidden=true;
      avatarActions.append(changeAvatar,resetAvatar,upload);preview.after(avatarActions,avatarHelp,avatarError);
      upload.onchange=async()=>{
        const file=upload.files?.[0];upload.value='';if(!file)return;
        await editAvatar(async()=>{const image=await avatarFromFile(file);return {value:image,image};});
      };
      const set=(name,value)=>{controls.get(name).value=value??'';session.set(name,value);};
      const defaults=()=>{const profile=profiles.find(value=>value.id===session.values.profile_id),catalog=profile?.model_settings?.catalog_provider||'custom',limits=modelDefaults(providers,catalog,session.values.model);const same=profile?.model===session.values.model;controls.get('context_window').placeholder=String(same?(profile?.model_settings?.context_window||limits.context):limits.context);controls.get('max_output_tokens').placeholder=String(same?(profile?.model_settings?.max_output_tokens||limits.output):limits.output);controls.get('reasoning_effort').placeholder=profile?.params?.reasoning_effort||tr('Provider default','提供商默认值');controls.get('model').required=!!profile;form.form.querySelector('.editor-actions .primary').disabled=!profile;};
      const updateModels=()=>{const profile=profiles.find(value=>value.id===session.values.profile_id),catalog=(providers.directory||[]).find(value=>value.id===profile?.model_settings?.catalog_provider);const options=new Map();if(profile?.model)options.set(profile.model,profile.model);for(const model of catalog?.models||[])options.set(model.id,model.name||model.id);for(const model of providers.configured_models?.[profile?.id]||profile?.models||[])options.set(typeof model==='string'?model:model.id,typeof model==='string'?model:model.name||model.id);const facts=(providers.connections||[]).find(value=>value.profile_id===profile?.id)?.catalog?.models||[];const allowed=new Set(providerConversationModels(facts,session.values.model).map(value=>value.id));for(const model of facts){if(allowed.has(model.id))options.set(model.id,model.name||model.id);else options.delete(model.id);}const input=controls.get('model_selection');input.replaceChildren();for(const [value,label]of [['',tr('Enter model ID','输入模型 ID')],...options]){const option=n('option','',label);option.value=value;input.append(option);}set('model_selection',options.has(session.values.model)?session.values.model:'');session.acknowledge({model_selection:session.values.model_selection});controls.get('model').closest('.form-row').hidden=!!input.value;defaults();};
      controls.get('profile_id').addEventListener('change',()=>{const selected=profiles.find(value=>value.id===session.values.profile_id);set('model',selected?.model||'');updateModels();});
      controls.get('model_selection').addEventListener('change',()=>{if(controls.get('model_selection').value)set('model',controls.get('model_selection').value);controls.get('model').closest('.form-row').hidden=!!controls.get('model_selection').value;defaults();});controls.get('model').addEventListener('input',defaults);updateModels();
      const instructionsRow=controls.get('text').closest('.form-row'),parameters=n('details','editor-disclosure');parameters.dataset.section='parameters';parameters.append(n('summary','',tr('Model parameters','模型参数')));for(const name of ['context_window','max_output_tokens','reasoning_effort'])parameters.append(controls.get(name).closest('.form-row'));instructionsRow.before(parameters);
      const instructions=n('details','editor-disclosure');instructions.dataset.section='instructions';instructions.append(n('summary'));instructionsRow.before(instructions);instructions.append(instructionsRow);
      form.lifecycle.locale=()=>{const t=key=>translate(locale(),key),label=t('assistant.personality');instructions.querySelector('summary').textContent=label;instructionsRow.querySelector('.form-label').textContent=label;controls.get('text').setAttribute('aria-label',label);instructionsRow.querySelector('small').textContent=t('assistant.personalityHelp');changeAvatar.textContent=t('assistant.changeAvatar');resetAvatar.textContent=t('assistant.resetAvatar');avatarHelp.textContent=t('assistant.avatarHelp');if(!avatarError.hidden)avatarError.textContent=t('assistant.avatarFailed');};form.lifecycle.locale();
      const providerHelp=n('p','hint',tr('Connect a Provider before creating an assistant.','请先连接提供商，再创建助手。'));form.form.querySelector('.editor-fields').prepend(providerHelp);providerHelp.hidden=profiles.length>0;
      const addProvider=action(tr('+ Add Provider','＋ 添加提供商'),()=>providerPicker(providers,{space:session.values.space_id||captured.space,owner:''}));controls.get('profile_id').closest('.field-body').append(addProvider);
      form.lifecycle.providersChanged=async saved=>{const value=await request('/api/v1/providers',{space:session.values.space_id||captured.space,owner:''});if(editor?.session!==session)return;profiles=value.profiles||[];Object.assign(providers,value);providerHelp.hidden=profiles.length>0;const input=controls.get('profile_id');input.replaceChildren();for(const profile of profiles){const option=n('option','',profile.name);option.value=profile.id;input.append(option);}const selected=profiles.find(profile=>profile.id===saved?.id)||profiles.find(profile=>profile.id===session.values.profile_id)||profiles[0];const changedProvider=selected?.id!==session.values.profile_id;set('profile_id',selected?.id||'');if(changedProvider||!session.values.model)set('model',selected?.model||'');updateModels();};
      form.lifecycle.settled=defaults;
      if(thread?.pending)form.root.prepend(n('p','hint',tr('Saved settings will apply to the next new turn. The current turn keeps its original configuration.','已保存的设置将在下一次新回合生效，当前回合保持原配置。')));
    }catch(reason){if(gate.current(token))errorView(reason);}
  }
  async function extensions(root,token){extensionUI=createExtensions({root,client,locale,memory:extensionMemory,onSettings:(section='integrations')=>{filter.settingsTab=section;routeChanged('settings');show('settings');},onPermissions:()=>{routeChanged('machines');show('machines');}});await extensionUI.load();}
  async function settings(root, token) {
    root.classList.add('settings-content');const settingsKeys=['settings.general','settings.providers','integrations.title','settings.tasks'];const tabs=segments(['general','providers','integrations','tasks'].map((key,i)=>[key,translate(locale(),settingsKeys[i])]),filter.settingsTab,value=>{filter.settingsTab=value;show('settings');});[...tabs.children].forEach((node,i)=>{node.dataset.settingsI18n=settingsKeys[i];});root.append(tabs);
    if(filter.settingsTab==='general'){
    const preferences=card(tr('General','通用'));
    const preference=(key,value)=>{try{localStorage.setItem(key,value);}catch{}window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key,value}}));};
    const language=field(tr('Language','语言'),locale(),{choices:[['en','English'],['zh-CN','简体中文']]});language.row.classList.add('preference-setting');language.input.onchange=()=>preference('pot-go-preview-locale',language.input.value);preferences.append(language.row);
    const appearance=n('div','preference-setting appearance-setting');appearance.append(n('span','',tr('Appearance','外观')),segments([['system',tr('System','跟随系统')],['light',tr('Light','浅色')],['dark',tr('Dark','深色')]],document.documentElement.dataset.theme||'system',value=>preference('pot-go-preview-theme',value)));preferences.append(appearance);if(client.notifications){notificationSettings??=notificationSetting({notifications:client.notifications,locale,desktop:client.desktop});preferences.append(notificationSettings.element);}root.append(preferences);

    const shortcuts=n('details','page-card shortcut-card');shortcuts.dataset.cacheKey='keyboard-shortcuts';shortcuts.open=shortcutOpen??innerWidth>=720;const shortcutSummary=n('summary','',tr('Keyboard shortcuts','快捷键'));shortcutSummary.onclick=()=>{shortcutOpen=!shortcuts.open;};shortcuts.append(shortcutSummary);for(const [label,key]of [[tr('Parent page','父页面'),'⌘ ['],[translate(locale(),'topology.title'),'⌘ D'],[tr('Settings','设置'),'⌘ ,'],[tr('New line','换行'),'⇧ ↵']]){const row=n('div','preference-row');row.append(n('span','hint',label),n('kbd','',key));shortcuts.append(row);}root.append(shortcuts);
    if(!client.desktop&&client.access){const access=n('section','page-card browser-actions');access.append(action(tr('Add to Home Screen','添加到主屏幕'),async()=>{if(!await requestPWAInstall())access.append(n('p','hint',tr('Use your browser menu to install Pot or add it to the Home Screen.','通过浏览器菜单安装 Pot 或添加到主屏幕。')));}));access.append(action(tr('Change access token','更改访问令牌'),()=>renderEditor(tr('Browser access','浏览器访问'),{token:''},[{name:'token',label:tr('Access token','访问令牌'),kind:'password',required:true}],values=>configureBrowserAccess(values.token))),action(tr('Disconnect this browser','断开此浏览器'),()=>forgetBrowserAccess()));root.append(access);}

    await renderAbout({client,root,scope:{owner:'',space:client.desktop?(client.spaces||[]).find(space=>space.kind==='local')?.id||'':scope().space},locale});return;
    }
    const locations=client.spaces||[];if(!configurationSpace)configurationSpace=scope().space;
    const settingsScope={space:configurationSpace,owner:''};
    if(client.desktop&&locations.length>1){const location=field(tr('Configuration location','配置位置'),configurationSpace,{choices:locations.map(space=>[space.id,space.name])});location.input.onchange=()=>{configurationSpace=location.input.value;show('settings');};root.append(location.row);}
    const current=()=>gate.current(token)&&!disposed,refresh=()=>pageView?.queue.run(),options={root,tr,locale,request,scope:settingsScope,current,refresh,allowanceMemory:providerAllowanceMemory,allowanceMemoryKey:client.runtime.boot_id};
    if(filter.settingsTab==='integrations'){
      const refreshIntegrations=()=>{if(!current())return Promise.resolve();client.resources?.invalidate(['integrations'],{space:settingsScope.space});return refresh();};
      const update=createIntegrations({root,request,scope:settingsScope,signal:token.signal,locale,openExternal:client.openExternal,refresh:refreshIntegrations});
      return connectView(root,token,async()=>{const [accounts,notifications]=await Promise.all([read('/api/v1/integrations',{...settingsScope,signal:token.signal}),request('/api/v1/plugin-notifications',{...settingsScope,signal:token.signal})]);return {...accounts,notifications};},update,{topics:['integrations','plugin-notifications','assistants','spaces'],space:settingsScope.space});
    }
    if(filter.settingsTab==='tasks'){
      const update=createTaskSettings({root,tr,locale,configure:(profile,policy)=>routingEditor(profile,policy,settingsScope),allowance:quota=>providerPolicyEditor(quota,settingsScope)});
      const data={};
      return connectView(root,token,async()=>{const results=await Promise.allSettled([['providers','/api/v1/providers'],['policy','/api/v1/agent/routing'],['allowances','/api/v1/usage/providers']].map(async([key,path])=>{const value=await read(path,{...settingsScope,signal:token.signal});if(current()){data[key]=value;update(data);}}));const failed=results.find(value=>value.status==='rejected');if(failed)throw failed.reason;return data;},update,{topics:['providers','settings','usage','spaces'],space:settingsScope.space});
    }
    const update=createProviderList({...options,pick:providers=>providerPicker(providers,settingsScope),edit:(profile,providers)=>providerEditor(profile,providers,settingsScope)});
    return connectView(root,token,async()=>({providers:await read('/api/v1/providers',{...settingsScope,signal:token.signal})}),update,{topics:['providers','extensions'],space:settingsScope.space,invalidate:topics=>{if(topics.includes('extensions'))update.invalidate();}});
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
  async function setupAI(target=scope()) {
    const captured={...target,owner:''};
    const snapshot=await read('/api/v1/providers',captured);
    await providerPicker(snapshot,captured);
    if(editor?.kind==='picker')editor.onboarding=true;
  }
  function providerFormLocale(form,titleKey,fields) {
    const t=key=>translate(locale(),'providers.'+key);
    form.lifecycle.locale=()=>{form.lifecycle.setTitle(t(titleKey));for(const [name,key]of Object.entries(fields)){const input=form.controls.get(name);if(!input)continue;input.closest('.form-row').querySelector('.form-label').textContent=t(key);input.setAttribute('aria-label',t(key));}for(const node of form.root.querySelectorAll('[data-provider-i18n]'))node.textContent=t(node.dataset.providerI18n);};
    const buttons=form.form.querySelector('.editor-actions').children;buttons[0].dataset.providerI18n='cancel';buttons[1].dataset.providerI18n='save';form.lifecycle.locale();
  }
  async function providerPicker(snapshot,captured) {
    if(editor?.kind==='assistant')suspendEditor();else if(!await leave())return;
    const t=key=>translate(locale(),'providers.'+key),form=renderEditor(t('add'),{search:''},[{name:'search',label:t('search'),kind:'search'}],()=>{}, {capturedScope:captured});editor.kind='picker';
    providerFormLocale(form,'add',{search:'search'});form.form.querySelector('.editor-actions .primary').remove();const grid=n('div','provider-grid'),hint=n('p','hint',t('choose'));hint.dataset.providerI18n='choose';form.form.querySelector('.editor-fields').prepend(hint);form.form.querySelector('.editor-fields').append(grid);let more=false;
    let types=[],typeVersion=0,typeController;const pickerCurrent=()=>editor?.session===form.session&&!disposed;
    const render=()=>{const query=form.controls.get('search').value.trim().toLowerCase();form.session.acknowledge({search:form.session.values.search});grid.replaceChildren();for(const provider of types){const directory=(snapshot.directory||[]).find(value=>value.id===provider.directory_provider);if(query?!`${provider.name} ${provider.id}`.toLowerCase().includes(query):!more&&directory&&!directory.common)continue;
      const choice=action(provider.name,()=>providerEditor(null,snapshot,captured,provider.id));choice.classList.add('provider-choice');const image=n('img','provider-logo');image.alt='';if(provider.icon){image.src=provider.icon;choice.prepend(image);}else{const fallback=icon('settings');fallback.classList.add('provider-logo');choice.prepend(fallback);}grid.append(choice);
    }if(!grid.children.length)grid.append(n('p','hint',t('noTypes')));toggle.textContent=t(more?'fewer':'more');};
    const toggle=action('',()=>{more=!more;render();});form.form.querySelector('.editor-fields').append(toggle);form.controls.get('search').addEventListener('input',render);const previous=form.lifecycle.locale;form.lifecycle.locale=()=>{previous();render();};
    const refreshTypes=async()=>{const version=++typeVersion;typeController?.abort();const controller=typeController=new AbortController();types=[];render();try{const value=await request('/api/v1/providers',{...captured,signal:controller.signal});if(!pickerCurrent()||version!==typeVersion)return;snapshot=value;types=(value.provider_types||[]).filter(value=>value.available!==false);render();}catch(reason){if(pickerCurrent()&&version===typeVersion&&reason.name!=='AbortError'){types=[];render();}}};
    form.lifecycle.providersInvalidated=refreshTypes;form.lifecycle.dispose=()=>{typeVersion++;typeController?.abort();};await refreshTypes();
  }
  async function providerEditor(profile,snapshot,settingsScope,providerOverride) {
    const t=key=>translate(locale(),'providers.'+key),onboarding=editor?.kind==='picker'&&editor.onboarding;
    if(editor?.kind==='picker')suspendEditor();else if(!await leave())return;
    const connection=(snapshot.connections||[]).find(c=>c.profile_id===profile?.id),typeID=providerOverride||connection?.type_id||'',type=(snapshot.provider_types||[]).find(value=>value.id===typeID)||{id:typeID,available:false};
    const providerID=type?.directory_provider||profile?.model_settings?.catalog_provider||'',selectedEntry=(snapshot.directory||[]).find(value=>value.id===providerID);
    let monitorView=null,monitorLoaded=!profile,monitorFailure=false;
    if(profile&&type?.capabilities?.allowances){const token=gate.next();body().append(n('p','hint',t('allowanceLoading')));try{monitorView=await request('/api/v1/providers/allowances?profile_id='+encodeURIComponent(profile.id),settingsScope);monitorLoaded=true;}catch{monitorFailure=true;}if(!gate.current(token)||disposed)return;}
    const inherited=modelDefaults(snapshot,providerID,profile?.model||'');let configuredModels=[...(snapshot.configured_models?.[profile?.id]||[])],discovered=[...(connection?.catalog?.models||[])];
    const inputDiscovery=createModelInputDiscovery(connection,profile?.model_settings?.input_modalities),baseName=type?.name||t('customName'),names=new Set((snapshot.profiles||[]).map(value=>value.name));let defaultName=baseName;for(let index=2;names.has(defaultName);index++)defaultName=`${baseName} ${index}`;
    const assistantChoices=[...(monitorView?.assistants||(client.assistants||[]).filter(value=>value.space_id===settingsScope.space))],monitor=monitorView?.monitor;if(monitor?.assistant&&!assistantChoices.some(value=>value.id===monitor.assistant))assistantChoices.push({id:monitor.assistant,name:t('status.assistant_missing'),missing:true});
    const initial={idle_compact_minutes:profile?.model_settings?.idle_compact_minutes??null,name:profile?.name||defaultName,endpoint:connection?.endpoint||type?.endpoint||'',api_key:'',model:profile?.model||'',model_selection:'',protocol:connection?.protocol||type?.protocol||'openai_responses',context_window:profile?.model_settings?.context_window??null,max_output_tokens:profile?.model_settings?.max_output_tokens??null,reasoning_effort:profile?.params?.reasoning_effort||'',...(type?.capabilities?.allowances&&monitorLoaded?{allowance_enabled:monitor?.enabled??true,allowance_threshold:monitor?.threshold??90,allowance_assistant:monitor?.assistant??assistantChoices[0]?.id??''}:{})};
    const protocolNames={openai_chat:'OpenAI Chat Completions',openai_responses:'OpenAI Responses',anthropic_messages:'Anthropic Messages',gemini:'Google Gemini'},protocols=[...new Set([...(type?.protocols?.length?type.protocols:[type?.protocol||initial.protocol]),initial.protocol])];
    const save=createProviderSave({profile,save:async(values,context,savedProfile)=>{if(!type||type.available===false)throw Object.assign(Error('provider unavailable'),{uiKey:'providers.unavailable'});const {model_selection,context_window,idle_compact_minutes,...connectionValues}=values;return request('/api/v1/providers',{...context,method:'POST',body:{request_id:context.request_id,provider_type_id:typeID,profile_id:savedProfile?.id||'',expected_revision:savedProfile?.revision||0,models:[...new Set([...configuredModels,values.model].filter(Boolean))],...connectionValues,reasoning_effort:values.reasoning_effort.trim()||null,max_output_tokens:values.max_output_tokens||limitsFor(values.model).output,model_settings:{idle_compact_minutes:idle_compact_minutes||null,catalog_provider:providerID||null,context_window:context_window||null,max_output_tokens:values.max_output_tokens||null,input_modalities:inputDiscovery.snapshot(values)}}});},configure:type?.capabilities?.allowances&&monitorLoaded?async(saved,values,context)=>{if(!type.capabilities?.allowances)return;if(!monitorView)monitorView=await request('/api/v1/providers/allowances?profile_id='+encodeURIComponent(saved.id),settingsScope);const revision=monitorView.monitor.revision;monitorView=await saveAllowanceConfiguration({request,scope:context,profileID:saved.id,values,revision});}:null});
    const definitions=[
      {name:'name',label:t('name'),required:true},{name:'endpoint',label:t('endpoint'),kind:'url',required:true,help:t('endpointHelp')},
      {name:'api_key',label:t('key'),kind:'password',required:!profile&&!type?.default_credential,help:profile?t('keepKey'):type?.default_credential?t('publicDefault'):''},
      {name:'model_selection',label:t('model'),choices:[['',t('enterModel')]]},{name:'model',label:t('modelID'),required:true},
      {name:'protocol',label:t('protocol'),choices:protocols.map(value=>[value,protocolNames[value]||value])},
      {name:'context_window',label:t('context'),kind:'number',parse:value=>value?Number(value):null,min:1,max:Number.MAX_SAFE_INTEGER,placeholder:String(inherited.context),presets:[128000,200000,256000,400000,512000,1000000]},
      {name:'max_output_tokens',label:t('output'),kind:'number',parse:value=>value?Number(value):null,min:1,max:Number.MAX_SAFE_INTEGER,placeholder:String(inherited.output),presets:[8000,16000,32000,64000,128000]},
      {name:'reasoning_effort',label:t('reasoning'),placeholder:t('providerDefault')},
      {name:'idle_compact_minutes',label:t('idleMinutes'),kind:'number',parse:value=>value?Number(value):null,min:1,max:525600,placeholder:'15',help:t('idleHelp')},
      ...(type?.capabilities?.allowances&&monitorLoaded?[{section:t('allowances')},{name:'allowance_enabled',label:t('allowanceEnabled'),kind:'checkbox',help:t('allowanceDefaults')},{name:'allowance_threshold',label:t('threshold'),kind:'number',parse:Number,min:1,max:100,required:true},{name:'allowance_assistant',label:t('assistant'),choices:[['',t('waitingAssistant')],...assistantChoices.map(value=>[value.id,value.name])]}]:[]),
    ];
    const form=renderEditor(profile?t('edit'):t('add'),initial,definitions,save,{capturedScope:settingsScope}),controls=form.controls,session=form.session;let endpointEdited=false;
    providerFormLocale(form,profile?'edit':'add',{name:'name',endpoint:'endpoint',api_key:'key',model_selection:'model',model:'modelID',protocol:'protocol',context_window:'context',max_output_tokens:'output',reasoning_effort:'reasoning',idle_compact_minutes:'idleMinutes',allowance_enabled:'allowanceEnabled',allowance_threshold:'threshold',allowance_assistant:'assistant'});
    for(const [name,key]of [['idle_compact_minutes','idleHelp'],['endpoint','endpointHelp'],['api_key',profile?'keepKey':'publicDefault'],['allowance_enabled','allowanceDefaults']]){const hint=controls.get(name)?.closest('.field-body').querySelector('small');if(hint)hint.dataset.providerI18n=key;}
    const section=form.form.querySelector('.page-section h2');if(section)section.dataset.providerI18n='allowances';
    if(monitorFailure){const warning=n('p','inline-error',t('allowanceLoadFailed'));warning.dataset.providerI18n='allowanceLoadFailed';form.form.querySelector('.editor-fields').append(warning);}
    const availabilityWarning=n('p','inline-error',t('unavailable'));availabilityWarning.dataset.providerI18n='unavailable';availabilityWarning.hidden=type.available!==false;form.form.prepend(availabilityWarning);if(type.available===false)form.form.querySelector('.primary').disabled=true;
    form.lifecycle.cancel=()=>back({skipPicker:true});form.lifecycle.afterSave=async saved=>{await back({skipPicker:true});await editor?.lifecycle.providersChanged?.(saved);if(onboarding){await leave();await createLocalAssistant();}};
    const set=(name,value)=>{controls.get(name).value=value??'';session.set(name,value);},showModel=()=>{controls.get('model').closest('.form-row').hidden=!!controls.get('model_selection').value;};
    function limitsFor(modelID){const entry=discovered.find(value=>value.id===modelID),base=modelDefaults(snapshot,providerID,modelID);return entry?{context:entry.context_window||base.context,input:entry.max_input||base.input,output:Math.min(entry.max_output||base.output,(entry.context_window||base.context)-1)}:base;}
    const inherit=()=>{const limits=limitsFor(session.values.model);controls.get('context_window').placeholder=String(limits.context);controls.get('max_output_tokens').placeholder=String(limits.output);};
    const models=(entries,selected='')=>{entries=providerConversationModels(entries,selected);const input=controls.get('model_selection');input.replaceChildren();for(const [value,label]of [['',t('enterModel')],...entries.map(model=>[typeof model==='string'?model:model.id,typeof model==='string'?model:model.name||model.id])]){const option=n('option','',label);option.value=value;input.append(option);}set('model_selection',entries.some(model=>(typeof model==='string'?model:model.id)===selected)?selected:'');session.acknowledge({model_selection:session.values.model_selection});showModel();};
    models([...new Map([...(selectedEntry?.models||[]),...configuredModels,...(profile?.model?[profile.model]:[]),...discovered].map(model=>[typeof model==='string'?model:model.id,model])).values()],initial.model);
    const keyURL={openai:'https://platform.openai.com/api-keys',anthropic:'https://platform.claude.com/settings/keys',google:'https://aistudio.google.com/api-keys',deepseek:'https://platform.deepseek.com/api_keys',opencode:'https://opencode.ai/auth','opencode-go':'https://opencode.ai/auth'}[providerID];
    if(keyURL){const link=n('a','provider-key-link',t('getKey'));link.dataset.providerI18n='getKey';link.href=keyURL;link.target='_blank';link.rel='noopener noreferrer';if(client.openExternal)link.onclick=event=>{event.preventDefault();client.openExternal(keyURL);};controls.get('api_key').closest('.field-body').append(link);}
    controls.get('model_selection').addEventListener('change',()=>{if(controls.get('model_selection').value){set('model',controls.get('model_selection').value);const model=selectedEntry?.models?.find(model=>model.id===session.values.model);if(model&&!endpointEdited){if(model.endpoint)set('endpoint',model.endpoint);if(model.protocol&&protocols.includes(model.protocol))set('protocol',model.protocol);}}showModel();inherit();});
    controls.get('endpoint').addEventListener('input',()=>{endpointEdited=true;});controls.get('protocol').addEventListener('change',()=>{endpointEdited=true;});controls.get('model').addEventListener('input',inherit);inherit();
    const refreshLocale=form.lifecycle.locale;form.lifecycle.locale=()=>{refreshLocale();controls.get('model_selection').options[0].textContent=t('enterModel');controls.get('reasoning_effort').placeholder=t('providerDefault');if(controls.has('allowance_assistant')){const choices=controls.get('allowance_assistant').options;choices[0].textContent=t('waitingAssistant');for(const item of assistantChoices.filter(value=>value.missing)){const option=[...choices].find(value=>value.value===item.id);if(option)option.textContent=t('status.assistant_missing');}}};
    let connectionRoute=JSON.stringify([initial.endpoint,initial.protocol]);
    connectProviderEditor({form,request,scope:settingsScope,profile,connection,type,locale,current:()=>editor?.session===session&&!disposed&&client.runtime.boot_id===session.context.boot,connectionChanged:value=>{inputDiscovery.connectionChanged(value);const next=JSON.stringify([value.endpoint,value.protocol]);if(connectionRoute!==next){connectionRoute=next;discovered=[];inherit();}},models:(entries,inputs)=>{inputDiscovery.adopt(session.values,inputs);configuredModels=entries.map(model=>typeof model==='string'?model:model.id);discovered=entries.filter(model=>typeof model!=='string');models([...new Map([...(selectedEntry?.models||[]),...entries].map(model=>[typeof model==='string'?model:model.id,model])).values()],controls.get('model_selection').value);inherit();}});
    let typeVersion=0,typeController;const priorDispose=form.lifecycle.dispose;
    form.lifecycle.providersInvalidated=async()=>{const version=++typeVersion;typeController?.abort();const controller=typeController=new AbortController();type.available=false;availabilityWarning.hidden=false;form.form.querySelector('.primary').disabled=true;form.lifecycle.connectionRefresh?.();discovered=[];inherit();
      try{const fresh=await request('/api/v1/providers',{...settingsScope,signal:controller.signal});if(editor?.session!==session||disposed||version!==typeVersion)return;const updated=(fresh.provider_types||[]).find(value=>value.id===typeID);if(updated)Object.assign(type,updated);type.available=updated?.available!==false&&!!updated;availabilityWarning.hidden=type.available;for(const name of ['allowance_enabled','allowance_threshold','allowance_assistant']){const input=controls.get(name);if(input)input.closest('.form-row').hidden=!type.capabilities?.allowances;}if(section)section.parentElement.hidden=!type.capabilities?.allowances;form.lifecycle.connectionRefresh?.();}
      catch(reason){if(editor?.session===session&&version===typeVersion&&reason.name!=='AbortError'){type.available=false;availabilityWarning.hidden=false;}}
    };
    form.lifecycle.dispose=()=>{typeVersion++;typeController?.abort();priorDispose?.();};
  }
  async function tasks(root,token) {
    root.classList.add('tasks-content');
    root.append(segments([['tasks',tr('Tasks','任务')],['schedules',tr('Schedules','定时计划')]],filter.taskTab,value=>{filter.taskTab=value;show('tasks');}));
    if(filter.taskTab==='schedules')return schedules(root,token);
    const controls=toolbar();controls.classList.add('task-filters');
    const scopes=segments(['active','attention','archived'].map(value=>[value,translate(locale(),`tasks.scope.${value}`)]),filter.taskScope,value=>{filter.taskScope=value;filter.taskOffset=0;restartTaskList();});controls.append(scopes);
    const search=field(translate(locale(),'tasks.search'),filter.taskQuery,{kind:'search'});search.row.classList.add('task-search');search.input.placeholder=translate(locale(),'tasks.search');controls.append(search.row);root.append(controls);
    const rows=n('div','task-list');rows.dataset.scrollKey='task-list';rows.append(n('p','hint',tr('Loading…','正在加载…')));root.append(rows);
    const summary=n('span','hint'),previous=action(tr('Previous','上一页'),()=>{filter.taskOffset=Math.max(0,filter.taskOffset-20);restartTaskList();},{disabled:!filter.taskOffset});
    const next=action(tr('Next','下一页'),()=>{filter.taskOffset=taskListView.result.next_offset;restartTaskList();},{disabled:true});
    const footer=toolbar(summary,previous,next);footer.classList.add('pagination');root.append(footer);
    const view={token,rows,scopes,summary,previous,next,nodes:new Map(),result:null};taskListView=view;
    view.queue=createRefreshQueue(()=>readTaskList(view),{active:()=>!disposed&&gate.current(view.token)&&taskListView===view});
    view.stopSearch=bindSearch(search.input,value=>{if(value===filter.taskQuery)return;filter.taskQuery=value;filter.taskOffset=0;restartTaskList();});
    view.clock=bindTaskClock(rows,{locale,active:()=>!disposed&&gate.current(view.token)&&taskListView===view&&!!rows.offsetParent});
    await refreshTaskList(view);
  }
  function refreshTaskList(view=taskListView){return view?.queue.run();}
  function restartTaskList(){
    const view=taskListView;if(!view)return;
    view.queue.close();view.token=gate.next();view.queue=createRefreshQueue(()=>readTaskList(view),{active:()=>!disposed&&gate.current(view.token)&&taskListView===view});
    currentViewKey=JSON.stringify(['tasks',[filter.taskTab,filter.taskScope,filter.taskQuery,filter.taskOffset,filter.scheduleOffset]]);
    return view.queue.run();
  }
  async function readTaskList(view) {
    const token=view.token,offset=filter.taskOffset,current=()=>gate.current(token)&&taskListView===view;
    view.rows.setAttribute('aria-busy','true');view.next.disabled=true;
    try {
      const {pages,partial}=await readTaskPages({scope:filter.taskScope,q:filter.taskQuery,offset,signal:token.signal,onProgress:pages=>{if(current())updateTaskList(view,pages,true);}});
      if(!current())return;
      const total=pages.reduce((sum,page)=>sum+(page.value.total??page.value.items?.length??0),0);
      if(!partial&&filter.taskOffset>=total&&filter.taskOffset){filter.taskOffset=Math.max(0,Math.floor((total-1)/20)*20);currentViewKey=JSON.stringify(['tasks',[filter.taskTab,filter.taskScope,filter.taskQuery,filter.taskOffset,filter.scheduleOffset]]);}
      updateTaskList(view,pages,false);
      view.rows.parentElement.querySelector('.space-availability')?.remove();
      if(partial){const notice=n('p','hint space-availability',translate(locale(),'tasks.partialSources'));notice.setAttribute('role','status');view.rows.before(notice);}
    }catch(reason){if(current()&&reason.name!=='AbortError'){if(!view.result){view.rows.replaceChildren();errorView(reason,view.rows);}else notify(reason.message);}}
    finally{if(current())view.rows.setAttribute('aria-busy','false');}
  }
  function updateTaskList(view,pages,loading){
      const result=combineTasks(pages,{offset:filter.taskOffset,limit:20});rememberTasks(result.items);
      if(!view.result)view.rows.replaceChildren();view.result=result;
      const text=(node,value)=>{if(node.textContent!==value)node.textContent=value;};
      for(const [index,value]of ['active','attention','archived'].entries()){const button=view.scopes.children[index];text(button,`${translate(locale(),`tasks.scope.${value}`)} ${result.counts[value]}`);button.classList.toggle('selected',filter.taskScope===value);button.setAttribute('aria-pressed',String(filter.taskScope===value));}
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
        entry.row.setAttribute('aria-label',task.title);entry.row.disabled=task._stale;entry.row.title=task.title;entry.row.onclick=()=>taskDetail(task,{returnTo:{route:'tasks'}});
        const source=taskAgentSource(task,locale());if(source)entry.logo.replaceChildren(source);else text(entry.logo,task.executor_icon==='anthropic'?'A':'◉');text(entry.title,task.title);entry.phase.className=`task-phase phase-${task.phase}`;
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
      text(view.summary,translate(locale(),'tasks.pageSummary').replace('{count}',result.total.toLocaleString(locale())).replace('{page}',Math.floor(filter.taskOffset/20)+1)+(loading?' · '+translate(locale(),'loading.pending'):''));view.previous.disabled=!filter.taskOffset;view.next.disabled=loading||result.next_offset==null;
      view.clock.refresh();
  }
  function closeTaskList(){taskListView?.stopSearch?.();taskListView?.queue.close();taskListView?.clock.dispose();taskListView=null;}
  function closeTaskView(){if(taskView)rememberTaskDetail(taskView);taskView?.eventsQueue?.close();taskView?.ui?.dispose();taskView=null;host.classList.remove('task-page-host');}
  function taskDetailKey(task){const space=client.spaces?.find(value=>value.id===task._space_id);return JSON.stringify([space?.control_id,resourceBoots.get(task._space_id)||resourceBoots.get('')||client.runtime.boot_id,task._space_id,task.assistant_id,task.id]);}
  function rememberTaskDetail(view){if(view.invalidated)return;taskDetails.delete(view.cacheKey);taskDetails.set(view.cacheKey,structuredClone({record:view.record,state:view.state,inputs:view.inputs}));if(taskDetails.size>64)taskDetails.delete(taskDetails.keys().next().value);}
  async function taskDetail(task,{recordHistory=true,returnTo={route:'chat',scope:{space:task._space_id,owner:task.assistant_id}}}={}) {
    rememberTasks([task]);taskOrigin=returnTo;captureView();pageView?.queue.close();pageView=null;closeTaskList();closeTaskView();scheduleView=null;detailParent=true;
    if(recordHistory){const replace=history.state?.potTask?.id===task.id&&history.state?.potScope?.space===task._space_id;history[replace?'replaceState':'pushState']({potRoute:'tasks',potScope:{space:task._space_id,owner:task.assistant_id},potTask:{...task},potTaskReturn:returnTo},'',location.href);}
    const path=`/api/v1/tasks/${encodeURIComponent(task.id)}`,fileScope={owner:task.assistant_id,space:task._space_id};
    const cacheKey=taskDetailKey(task),saved=taskDetails.get(cacheKey);
    const result=await load(token=>saved?.record||request(path,{...fileScope,signal:token.signal}));if(!result)return;
    const {root,token}=result,record=result.value;host.classList.add('task-page-host');
    const draftKey=JSON.stringify([task._space_id,task.assistant_id,task.id]);
    const view={task,record,token,returnTo,cacheKey,initialRead:!saved,scope:fileScope,state:saved?.state||createTaskTranscript(),inputs:saved?.inputs||[],busy:false,again:false,unavailable:false};
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
      onArchive:async()=>{await request('/api/v1/tasks/dispositions',{...fileScope,method:'POST',body:{decisions:[{task_id:task.id,run_id:view.record.run_id,archived:!view.record.info?.archived,reason:''}]}});if(current())await refreshTaskEvents(view);},
      onUsage:()=>openUsageView({...fileScope,name:task.title,scope:task.id,returnTask:{...task},taskReturn:returnTo}),
      onRefresh:()=>refreshTaskEvents(view),onSettings:()=>{configurationSpace=task._space_id;filter.settingsTab='providers';routeChanged('settings');return show('settings');},
    });
    view.ui=ui;view.transcript=ui.transcript;taskView=view;heading(task.title);permissions();
    if(saved)renderTaskTranscript(view);
    restoreView(`task:${draftKey}`,token);
    await refreshTaskEvents(view);
  }
  function refreshTaskEvents(view=taskView){if(!view)return Promise.resolve();view.eventsQueue||=createRefreshQueue(()=>readTaskEvents(view),{active:()=>!disposed&&gate.current(view.token)&&taskView===view});return view.eventsQueue.run();}
  async function readTaskEvents(view) {
    const generation=view.readGeneration||0,current=()=>gate.current(view.token)&&taskView===view&&generation===(view.readGeneration||0);
    let render=false;
    try{
      const path=`/api/v1/tasks/${encodeURIComponent(view.task.id)}`,options={...view.scope,signal:view.token.signal};
      const events=(async()=>{let count=0,page;
        do{page=await request(`${path}/events?${new URLSearchParams({after:view.state.cursor,limit:100})}`,options);if(!current())return;const previous=view.state.cursor;appendTaskEvents(view.state,page.events);if(page.has_more&&view.state.cursor===previous)throw Error('Task event cursor did not advance');count+=(page.events||[]).length;}while(page.has_more&&count<5000);
        return page;
      })();
      const updates=await Promise.allSettled([view.initialRead?Promise.resolve(view.record):request(path,options),request(`${path}/messages`,options),events]);view.initialRead=false;
      if(!current())return;
      if(updates[0].status==='fulfilled')view.record=updates[0].value;if(updates[1].status==='fulfilled')view.inputs=list(updates[1].value);
      view.connectionError=updates.filter(update=>update.status==='rejected'&&update.reason.name!=='AbortError').map(update=>update.reason.message).join('\n');
      const page=updates[2].status==='fulfilled'?updates[2].value:{};
      view.unavailable=!!page?.unavailable;render=true;
      if(page?.has_more)queueMicrotask(()=>refreshTaskEvents(view));
    }catch(reason){if(current()&&reason.name!=='AbortError'){view.connectionError=reason.message;render=true;}}
    if(render&&current())renderTaskTranscript(view);
  }
  function renderTaskTranscript(view){rememberTaskDetail(view);view.ui.update({record:view.record,state:view.state,inputs:view.inputs,unavailable:view.unavailable,connectionError:view.connectionError||'',approvals:pendingApprovals.filter(item=>item.request?.task===view.task.id)});renderApprovals();}
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
    root.classList.add('schedules-content');root.append(toolbar(action(translate(locale(),'schedule.new'),()=>scheduleEditor())));const content=n('div','schedule-projection');root.append(content);
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
      row.append(button,owner,...(item.spec.kind==='cron'?[n('code','schedule-rule',`${item.spec.cron} · ${item.spec.timezone}`)]:[]),n('span','hint',item._stale?tr('Stale','已过期'):item.spec.enabled?item.next_label||tr('Not scheduled','未安排'):tr('Paused','已暂停')));row.dataset.viewKey=JSON.stringify([item._space_id,item._assistant_id,item.id]);rows.append(row);
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
    await connectView(root,token,()=>read(`/api/v1/schedules?${new URLSearchParams({selected:item.id,runs_offset:runsOffset,limit:20})}`,{signal:token.signal,space:item._space_id,owner:item._assistant_id}),value=>{
      if(!value.selected||value.selected.deleted){scheduleView=null;return show('tasks',{force:true});}
      view.item={...item,...value.selected};const projection=n('div');renderScheduleHistory(projection,view,value,runsOffset);reconcile(content,projection);
    },{topics:['schedules','tasks','machines','spaces'],space:item._space_id});
    if(gate.current(token))restoreView(JSON.stringify(['schedule-history',item._space_id,item._assistant_id,item.id,runsOffset]),token);
  }
  function renderScheduleHistory(root,view,value,runsOffset) {
    const selected=view.item;heading(selected.spec.title);const info=card(tr('Schedule details','定时计划详情'));
    info.append(n('code','',selected.spec.kind==='cron'?selected.spec.cron:translate(locale(),`schedule.${selected.spec.kind}`)),n('p','hint',selected.spec.goal),n('p','hint',`${selected.spec.timezone||''} · ${selected.next_label||tr('Not scheduled','未安排')}`));if(selected.last_error)info.append(n('p','inline-error',selected.last_error));
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
    const t=key=>translate(locale(),`schedule.${key}`);
    const spec=item?.spec||{title:'',goal:'',machine_id:'',timezone:browserTimezone(),kind:'cron',cron:'0 9 * * *',at:'',every_minutes:60,misfire:'coalesce',notify:'changes',enabled:true,profile:null,model:null,requirements:{tools:false,desktop:false,exact_budget:false,live_steering:false}};
    const definitions=[
      ...(!item?[{name:'owner_ref',key:'assistant',choices:(client.assistants||[]).filter(owner=>!owner.origin).map(owner=>[JSON.stringify([owner.space_id,owner.id]),`${owner.name} · ${owner.space_name||''}`])}]:[]),
      {name:'title',key:'title',required:true},{name:'goal',key:'goal',kind:'textarea',required:true},{name:'machine_id',key:'machine'},
      {name:'timezone',key:'timezone',helpKey:'timezoneHelp',required:true},
      {name:'kind',key:'repeat',choiceKeys:{once:'once',interval:'interval',cron:'cron'},choices:['once','interval','cron'].map(key=>[key,t(key)])},
      {name:'at',key:'at',help:'YYYY-MM-DD HH:MM'},
      {name:'every_minutes',key:'minutes',kind:'number',min:1,max:525600,parse:Number},
      {name:'cron',key:'cron',helpKey:'cronHelp',placeholder:'0 8-20 * * *'},
      {name:'misfire',key:'misfire',choiceKeys:{coalesce:'coalesce',skip:'skip'},choices:['coalesce','skip'].map(key=>[key,t(key)])},
      {name:'notify',key:'notify',choiceKeys:{changes:'changes',all:'all',failures:'failures'},choices:['changes','all','failures'].map(key=>[key,t(key)])},
      {name:'enabled',key:'enabled',kind:'checkbox'},
    ].map(definition=>({...definition,label:t(definition.key),...(definition.helpKey?{help:t(definition.helpKey)}:{})}));
    let preview;
    const view=renderEditor(t(item?'edit':'new'),{...spec,owner_ref:JSON.stringify([item?item._space_id:scope().space,item?item._assistant_id:scope().owner])},definitions,async(values,context)=>{
      await preview.validate(values);
      const {owner_ref,...specValues}=values;const [space,owner]=JSON.parse(owner_ref);
      const canonical={...specValues,cron:values.kind==='cron'?values.cron:'',at:values.kind==='once'?values.at:'',every_minutes:values.kind==='interval'?values.every_minutes:0};
      return request('/api/v1/schedules',{...context,space,owner,method:'POST',body:{request_id:context.request_id,action:item?'update':'create',schedule_id:item?.id||'',spec:canonical}});
    },{capturedScope:item?{space:item._space_id,owner:item._assistant_id}:scope(),submitLabel:t('save')});
    preview=connectScheduleEditor({view,request,locale,definitions,current:()=>editor?.session===view.session&&!disposed});
    const refresh=view.lifecycle.locale;view.lifecycle.locale=()=>{refresh();view.lifecycle.setTitle(t(item?'edit':'new'));};
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
    const timezone=Intl.DateTimeFormat().resolvedOptions().timeZone,scopeID=usageOwner?.scope||usageOwner?.owner||'',params={days:365,timezone,scope:scopeID},spaceID=usageOwner?.space||filter.usageSpace;
    root.dataset.usageScope=JSON.stringify([spaceID,scopeID,usageOwner?.owner||'']);
    const listsRoot=n('div','usage-lists'),jobs=[];
    if(usageOwner)for(const space of (client.spaces||[]).filter(space=>spaceID==='all'||space.id===spaceID)){
      const owner={space:space.id,owner:usageOwner?.owner||''},base={timezone,scope:scopeID};
      for(const [kind,title]of [['models',tr('Lifetime model usage','累计模型用量')],['requests',tr('Model requests','模型请求')],['tools',tr('Tool statistics','工具统计')],['compactions',tr('Context compaction','上下文压缩')]])jobs.push(createUsageList({root:listsRoot,kind,title,params:base,owner,request:read,token,current:()=>gate.current(token),offsets:usageOffsets,cache:usageCache,locale:locale(),tr}));
    }
    const content=n('div'),limits=n('div','usage-limit-actions');headingActions.append(limits);root.append(content,listsRoot);
    let initial=true;
    return connectView(root,token,async()=>{
      const summary=across(`/api/v1/usage/summary?${new URLSearchParams(params)}`,token,{spaceID,ownerID:usageOwner?.owner||'',errorTarget:root});
      for(const job of jobs)if(!initial)job.refresh();initial=false;
      return summary;
    },pages=>{const projection=n('div'),limitProjection=n('div','usage-limit-actions');
    const data=pages.length===1?{...pages[0].value,spaces:pages}:combineUsage(pages);
    const {exact,compact}=usageNumbers(locale()),total=(usageOwner?data.lifetime||data.total:data.total)||{},metrics=n('div','usage-metrics'),totalCard=n('section','page-card usage-total');
    for(const [label,value]of [[tr('Total tokens','总 token'),total.input+total.output],[tr('Input (includes cache)','输入（含缓存）'),total.input],[tr('Output','输出'),total.output]]){const item=n('div','metric');item.append(n('span','hint',label),n('strong','',compact(value)));usageTooltip(item,{title:label,rows:usageRows(total,locale()),note:tr('Cached tokens are included in input. Token counts are not billed cost.','缓存已计入输入，token 数量不代表实际费用。')});metrics.append(item);}
    totalCard.append(metrics,n('p','usage-cache-breakdown',`${tr('Cached input','缓存输入')} ${exact(total.cache)} · ${tr('Cache hit rate','缓存命中率')} ${usageNumbers(locale()).hitRate(total)}`),n('p','hint',`${tr('Model requests','模型请求')} ${exact(total.requests||0)}`));projection.append(totalCard);
    const routing=data.routing_total||{};
    if(routing.requests){const routingCard=card(tr('Task routing turns','任务路由回合'));routingCard.append(usageTooltip(n('p','usage-number',`${compact(routing.input+routing.output)} tokens`),{title:tr('Task routing turns','任务路由回合'),rows:usageRows(routing,locale()),note:tr('Includes the entire main-chat turn that delegated a task. Already included in totals.','包含委派任务的整个主聊天回合，已计入总量。')}));projection.append(routingCard);}
    projection.append(usageCalendar(data,{locale:locale(),timezone}));
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
  async function addAssistantPage(root,token) {
    assistantStartRefresh=null;
    const t=key=>translate(locale(),'assistantStart.'+key),captured={space:client.spaces.find(item=>item.kind==='local')?.id||scope().space,owner:''};
    const first=!(client.assistants||[]).length;
    heading(t(first?'firstTitle':'title'));root.classList.add('assistant-start');
    const intro=n('div','assistant-start-intro');intro.append(n('h1','',t(first?'firstTitle':'title')),n('p','hint',t('description')));root.append(intro);
    const options=n('div','assistant-start-options');root.append(options);
    for(const kind of ['api','pot','codex']) {
      const option=n('section','page-card assistant-start-option');
      const header=n('div','assistant-start-option-header'),mark=n('span','assistant-start-icon assistant-start-icon-'+kind),content=n('div','assistant-start-option-content');
      mark.setAttribute('aria-hidden','true');mark.append(kind==='api'?icon('plug'):connectionBrand(kind));
      content.append(n('h2','',t(kind+'Title')),n('p','hint',t(kind+'Description')));header.append(mark,content);option.append(header);
      const button=action(t(kind+'Action'),async()=>{
        if(kind==='api'){button.disabled=true;try{await createLocalAssistant();}finally{button.disabled=false;}return;}
        await connection(kind);
      },{primary:kind==='api'});option.append(button);options.append(option);
    }
    async function connection(kind) {
      if(!gate.current(token))return;
      let connectedID='',connections;const lifetime=new AbortController();
      async function openDirectory() {
        const currentEditor=editor,currentRoot=host.firstElementChild;
        const snapshot=await request('/api/v1/agent-links',captured);
        await changed();
        if(disposed||editor!==currentEditor||host.firstElementChild!==currentRoot)return;
        const link=(snapshot.items||[]).find(item=>item.id===connectedID);
        if(!link)throw Error(translate(locale(),'agentLinks.failed'));
        const assistants=link.assistants||link.directory?.assistants||[],defaultID=link.directory?.default_assistant_id;
        const chosen=assistants.length===1?assistants[0]:assistants.find(item=>(item.address?.agent_id||item.id)===defaultID);
        const open=assistant=>openChat({space:captured.space,owner:'agent-link:'+connectedID+':'+(assistant.address?.agent_id||assistant.id)});
        if(chosen){await leave();await open(chosen);return;}
        await leave();route='add-assistant';assistantStartRefresh=openDirectory;const list=body();
        heading(t(assistants.length?'chooseTitle':'emptyTitle'));
        list.append(n('h1','',t(assistants.length?'chooseTitle':'emptyTitle')),n('p','hint',t(assistants.length?'chooseDescription':'emptyDescription')));
        for(const assistant of assistants)list.append(action(assistant.name||assistant.address.agent_id,()=>open(assistant)));
        const status=n('p','inline-error');status.setAttribute('role','alert');list.append(status);
        list.append(action(t('refresh'),async()=>{try{await openDirectory();}catch{status.textContent=t('refreshFailed');}}),action(t('back'),()=>show('add-assistant',{force:true})));
      }
      connections=createAgentLinks({root:n('div'),request,locale,scope:()=>captured,signal:lifetime.signal,
        edit:({title,initial,fields,save,scope:context,submitLabel})=>{
          const form=renderEditor(title,initial,fields,save,{capturedScope:context,submitLabel});
          form.lifecycle.afterSave=()=>openDirectory();return form;
        },onConnected:async result=>{connectedID=result?.id||result?.link?.id;}});
      const form=connections.addAgent({guideKey:'assistantStart.'+kind+'Guide'}),dispose=form.lifecycle.dispose;
      form.lifecycle.dispose=()=>{dispose?.();lifetime.abort();};
    }
  }
  async function machines(root,token) {
    const t=key=>translate(locale(),key);root.classList.add('topology-page');
    const toolbar=n('div','topology-toolbar'),connectionRoot=n('div','topology-connection'),storage=n('div','topology-machine-storage'),rows=n('div'),entries=new Map(),linkNotice=n('p','inline-error');storage.hidden=true;storage.append(rows);connectionRoot.className='agent-links-container';toolbar.append(connectionRoot);root.append(toolbar,linkNotice,storage);
    let model,view,currentNode=null,currentEdgeID='';
    const timestamp=value=>value?new Date(value).toLocaleString(locale()):t('topology.activity.unknown');
    function showActivity(node){
      const observation=node.observation;
      if(!observation)return;
      const section=n('section','topology-activity');
      section.append(n('h3','section-heading',t('topology.workload')));
      if(observation.value){const counts=n('p','topology-workload-counts',t('topology.taskLoad').replace('{running}',String(observation.counts.running)).replace('{queued}',String(observation.counts.queued)));section.append(counts,n('p','hint',t('topology.waitingTasks').replace('{approval}',String(observation.counts.waiting_approval)).replace('{allowance}',String(observation.counts.waiting_allowance))));}
      if(!observation.fresh)section.append(n('p','hint',t('topology.issue.stale')));
      if(node.issue&&node.issue!=='stale'){const notice=n('p','topology-issue-notice',t('topology.issueDetail.'+node.issue));section.append(notice);}
      section.append(n('p','hint',t('topology.sampleTime').replace('{time}',timestamp(observation.observedAt))),n('p','hint',t('topology.workloadScope')));
      view.inspector.append(section);
    }
    function showQueues(link,fromName,toName){
      const section=n('section','topology-queues');section.append(n('h3','section-heading',t('topology.messageQueues')));
      for(const [from,to,queue,stale]of [[fromName,toName,link.outbox,false],[toName,fromName,link.observation?.value?.outbox,link.status!=='connected'||!!link.observation?.error||Date.now()-(link.observation?.observed_at_ms||0)>=30000]]){
        const row=n('div','topology-queue-row');row.append(n('p','',t('topology.queueDirection').replace('{from}',from).replace('{to}',to).replace('{count}',queue?String(queue.pending):t('topology.activity.unknown'))));
        if(queue?.pending>0)row.append(n('p','hint',t('topology.oldestQueued').replace('{time}',timestamp(queue.oldest_at_ms))));
        if(stale)row.append(n('p','hint',t('topology.cachedQueue')));section.append(row);
      }
      section.append(n('p','hint',t('topology.lastExchange').replace('{time}',timestamp(link.last_exchange_at_ms))),n('p','hint',t('topology.receiptMeaning')));view.inspector.append(section);
    }
    const data={inventory:[],permissionPages:[],permissionsPending:true,linkPages:[]};
    agentOwnerSpace=client.spaces.some(space=>space.id===agentOwnerSpace)?agentOwnerSpace:scope().space||client.spaces[0]?.id;
    const selectedScope=()=>({space:agentOwnerSpace,owner:''});
    const edit=({title,initial,fields,save,scope:captured,submitLabel})=>renderEditor(title,initial,fields,save,{capturedScope:captured,submitLabel});
    if(client.spaces.length>1){const owner=field(t('agentLinks.selectControl'),agentOwnerSpace,{choices:client.spaces.map(space=>[space.id,space.name||space.id])});connectionRoot.append(owner.row);owner.input.onchange=()=>{agentOwnerSpace=owner.input.value;connections.load();};}
    const connections=createAgentLinks({root:connectionRoot,request,locale,notify,scope:selectedScope,edit,signal:token.signal,onConnected:async result=>{const id=result?.id||result?.link?.id;if(id){const peer=result.peer||result.link?.peer;const identity=peer?.address?{id:peer.address.agent_id,node_id:peer.address.node_id,kind:peer.adapter}:peer;const owned=identity?.kind==='pot'&&client.spaces.find(space=>space.control_id===identity.node_id);topologySelection=owned?'control:'+owned.id:agentNodeID(identity);}client.resources?.invalidate(['agent-links'],selectedScope());await changed(result);}});
    function related(node){const section=n('div','topology-related');section.append(n('p','hint',t('topology.related')));for(const edge of model.edges.filter(item=>item.from===node.id||item.to===node.id)){const a=model.nodes.find(item=>item.id===edge.from),b=model.nodes.find(item=>item.id===edge.to);section.append(action(a.name+(edge.kind==='agent'?' ↔ ':' → ')+b.name,()=>showEdge(edge)));}return section;}
    function preserveInspectorFocus(){
      const active=document.activeElement,nodeID=active?.closest('[data-node-id]')?.dataset.nodeId,edgeID=active?.closest('[data-edge-id]')?.dataset.edgeId;
      const focused=view.inspector.contains(document.activeElement)?document.activeElement:null;
      const label=focused?.getAttribute('aria-label'),text=focused?.textContent;
      return ()=>{if(nodeID||edgeID){const target=[...root.querySelectorAll('[data-node-id],[data-edge-id]')].find(node=>nodeID?node.dataset.nodeId===nodeID:node.dataset.edgeId===edgeID);target?.focus({preventScroll:true});return;}if(!focused)return;const target=focused.isConnected&&view.inspector.contains(focused)?focused:[...view.inspector.querySelectorAll('button,summary')].find(node=>label?node.getAttribute('aria-label')===label:node.textContent===text);if(target&&document.activeElement!==target)target.focus({preventScroll:true});};
    }
    function keepMachine(){for(const card of view.inspector.querySelectorAll('.machine-card'))rows.append(card);}
    function showNode(node){if(!node)return;currentEdgeID='';const restore=currentNode?.id===node.id?preserveInspectorFocus():()=>{};currentNode=node;topologySelection=node.id;keepMachine();view.inspector.replaceChildren();
      if(node.kind==='machine'){const entry=entries.get(JSON.stringify([node.space,node.identity]));if(entry){entry.renderPermissions?.();view.inspector.append(entry.element);}}
      else {
        const paused=node.link?.state==='disconnected'||node.link?.status==='disconnected';
        view.inspector.append(view.brand(node),n('p','hint',t('topology.kind.'+node.kind)),n('h2','',node.name));
        if(node.subtitle)view.inspector.append(n('p','hint topology-host',node.subtitle));
        view.inspector.append(n('p',node.status==='connected'?'success-text':'hint',t(paused?'agentLinks.disconnected':node.status==='stale'?'topology.status.stale':'agentLinks.status.'+(node.status==='connected'?'connected':node.status==='connecting'?'connecting':'offline'))),n('p','hint',t(paused?'agentLinks.disconnectedDescription':'topology.messageScope')));
        showActivity(node);
        const assistants=node.link?.assistants||node.link?.directory?.assistants||(!node.link?(client.assistants||[]).filter(item=>!item.origin&&item.space_id===node.space):[]),directory=n('section','topology-assistants');
        directory.append(n('h3','section-heading',t('agentLinks.providedAssistants')));
        if(!assistants.length)directory.append(n('p','hint',t('agentLinks.noAssistants')));
        for(const assistant of assistants){
          const remoteID=assistant.address?.agent_id||assistant.id,id=node.link?'agent-link:'+node.link.id+':'+remoteID:assistant.id;
          const open=action('',()=>openChat({space:node.space,owner:id}));open.className='control topology-assistant';
          open.append(avatar(24,assistant.avatar),n('span','topology-assistant-name',assistant.name||remoteID));
          const activity=node.activities?.get(remoteID),state=node.observation?.fresh?assistantActivityState(activity):'unknown';
          open.append(n('span','topology-assistant-state state-'+state,t('topology.activity.'+state)));
          if(remoteID===(node.link?.default_assistant_id||node.link?.directory?.default_assistant_id))open.append(n('span','hint',t('agentLinks.defaultAssistant')));
          open.setAttribute('aria-label',t('agentLinks.openChat')+' · '+(assistant.name||remoteID));directory.append(open);
        }
        view.inspector.append(directory);
        if(node.link){
          const local=model.nodes.find(item=>item.self&&item.space===node.space);showQueues(node.link,local?.name||'',node.name);
          if(node.link.last_error==='agent_link_reconnect_required')view.inspector.append(n('p','hint',t('agentLinks.reconnectRequired')));
          const link={...node.link,_space:node.space},actions=n('div','topology-link-actions');
          actions.append(action(t(paused?'agentLinks.reconnect':'agentLinks.disconnect'),()=>{(paused?connections.reconnect(link):connections.disconnect(link)).catch(()=>{});}),action(t('agentLinks.delete'),()=>connections.remove(link),{danger:true}));view.inspector.append(actions);
        }
      }
      view.inspector.append(related(node));restore();
    }
    function showEdge(edge){currentEdgeID=edge.id;keepMachine();view.inspector.replaceChildren();const a=model.nodes.find(node=>node.id===edge.from),b=model.nodes.find(node=>node.id===edge.to);view.inspector.append(n('p','hint',t(edge.kind==='agent'?'topology.agentLegend':'topology.machineLegend')),n('h2','',a.name+(edge.kind==='agent'?' ↔ ':' → ')+b.name),n('p','hint',t(edge.kind==='agent'?'topology.messageScope':'topology.executionScope')),action(t('topology.viewSource'),()=>view.select(a.id)),action(t('topology.viewTarget'),()=>view.select(b.id)));if(edge.kind==='agent')showQueues(edge.link,a.name,b.name);}
    view=createTopology({root,locale,initialSelection:topologySelection,onSelect:showNode,onEdge:showEdge,signal:token.signal});
    const freshnessTimer=setInterval(()=>{if(!document.hidden&&gate.current(token)&&!editor)pageView?.queue.run();},10000);
    token.signal.addEventListener('abort',()=>clearInterval(freshnessTimer),{once:true});
    const update=async({inventory,permissionPages,permissionsPending,linkPages})=>{
      if(!gate.current(token)||disposed)return;
      const restore=preserveInspectorFocus(),items=inventory.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner));
      keyedRows(rows,entries,items,{key:machine=>JSON.stringify([machine._space_id,machine.runtime_id||machine.id]),create:()=>{
        const element=n('section','page-card machine-card'),header=n('div','machine-header'),mark=n('span','machine-icon'),name=n('span','machine-name'),status=n('span'),tags=n('p','hint machine-tags'),location=n('p','hint machine-location'),addresses=n('p','hint machine-addresses'),permissions=n('div','machine-permissions');mark.append(icon('machines'));header.append(mark,name,status);element.append(location,header,addresses,tags,permissions);return {element,name,status,tags,location,addresses,permissions};
      },update:(entry,machine)=>{
        const id=machine.runtime_id||machine.id,permissionPage=permissionPages.find(page=>page.space.id===machine._space_id),policy=permissionPage?.value.policy;
        const stale=machine._stale||permissionPage?.stale||client.spaces.find(space=>space.id===machine._space_id)?.connected===false,permissions=entry.permissions;
        entry.element.dataset.machineId=id;entry.element.dataset.spaceId=machine._space_id;entry.name.textContent=machineDisplayName(machine)||t('topology.kind.machine');entry.location.textContent=machine._space_name;entry.status.textContent=t(stale?'topology.status.stale':machine.connected?'agentLinks.status.connected':'agentLinks.status.offline');entry.status.className=machine.connected&&!stale?'success-text':'hint';entry.tags.textContent=(machine.tags||[]).join(' · ');entry.tags.hidden=!machine.tags?.length;
        entry.addresses.textContent=(machine.ip_addresses||[])[0]||'';entry.addresses.hidden=!entry.addresses.textContent;
        entry.renderPermissions=()=>{
        if(permissionsPending&&!policy?.machines?.[id]){permissions.replaceChildren(n('p','hint',t('loading.pending')));return;}
        if(!policy?.machines?.[id]){permissions.replaceChildren(n('p','inline-error',t('topology.permissionsUnavailable')),action(t('retry'),()=>show('machines')));return;}
        const captured={space:machine._space_id,owner:''},controlID=permissionPage.space.control_id,current=policy.machines[id],key=JSON.stringify([machine._space_id,controlID,id]);
        let draft=machineDrafts.get(key);
        if(!draft||!draft.dirty&&!draft.pending){const error=draft?.error;draft=createEditorSession({...captured,boot:client.runtime.boot_id},current);draft.error=error;machineDrafts.set(key,draft);}
        renderMachinePermissions({root:permissions,machine,policy,request,scope:captured,locale,draft,stale,isCurrent:()=>client.runtime.boot_id===draft.context.boot&&client.spaces.some(space=>space.id===captured.space&&space.control_id===controlID),refresh:()=>{if(route==='machines'&&!editor)return pageView?.queue.run();},edit});
        };
        if(currentNode?.kind==='machine'&&currentNode.space===machine._space_id&&currentNode.identity===id)entry.renderPermissions();
      }});
      const edgeSelection=currentEdgeID;model=projectTopology(client.spaces,items,linkPages);view.update(model);if(currentNode?.kind==='machine')showNode(currentNode);if(edgeSelection){const edge=model.edges.find(item=>item.id===edgeSelection);if(edge)showEdge(edge);}restore();
    };
    return connectView(root,token,async()=>{
      const connectionRequest=connections.load();
      data.permissionsPending=true;
      const permissions=across('/api/v1/permissions',token).catch(()=>[]).then(pages=>{data.permissionPages=pages;data.permissionsPending=false;if(data.inventory.length)return update(data);});
      const links=across('/api/v1/agent-links',token,{errorTarget:connectionRoot}).then(pages=>{data.linkPages=pages;linkNotice.hidden=true;}).catch(reason=>{if(reason.name==='AbortError')return;linkNotice.hidden=false;linkNotice.replaceChildren(n('span','',t('topology.connectionUnavailable')),action(t('retry'),()=>pageView?.queue.run()));});
      data.inventory=await across('/api/v1/machines',token);await links;await update(data);await permissions;await connectionRequest;return data;
    },update,{topics:['machines','spaces','permissions','agent-links','tasks','assistant-mail','runtime'],space:client.spaces?.length===1?client.spaces[0].id:''});
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
 if(extensionUI&&next==='extensions')return extensionUI.refresh();extensionUI?.dispose();extensionUI=null;
    if(!keepUsageOwner)usageOwner=null;
    if(next==='settings'&&filter.settingsTab!=='general'&&!configurationSpace)configurationSpace=scope().space;
    const viewKey=JSON.stringify([next,next==='tasks'?[filter.taskTab,filter.taskScope,filter.taskQuery,filter.taskOffset,filter.scheduleOffset]:next==='usage'?[filter.usageSpace,usageOwner]:next==='settings'?[filter.settingsTab,configurationSpace]:[]]);
    if(!force&&route===next&&currentViewKey===viewKey){if(taskListView)return refreshTaskList();if(pageView)return pageView.queue.run();}
    captureView();viewReady=false;pageView?.queue.close();pageView=null;

    closeTaskView();taskOrigin=null;closeTaskList();scheduleView=null;detailParent=false;if(!keepUsageOwner)usageOwner=null;
    route=next;heading(next==='extensions'?translate(locale(),'extensions.title'):tr({tasks:filter.taskTab==='schedules'?'Schedules':'Tasks',machines:translate(locale(),'topology.title'),usage:'Usage',settings:'Settings'}[next]||next,{tasks:filter.taskTab==='schedules'?'定时计划':'任务',machines:translate(locale(),'topology.title'),usage:'用量',settings:'设置'}[next]||next));
    const version=++pageVersion,token=gate.next(),root=body();currentViewKey=viewKey;
    root.append(n('p','hint',tr('Loading…','正在加载…')));
    try {root.replaceChildren();await ({settings,tasks,machines,usage,extensions,'add-assistant':addAssistantPage}[next]||settings)(root,token);if(gate.current(token))restoreView(viewKey,token);}
    catch(reason){if(gate.current(token)&&version===pageVersion&&!disposed)errorView(reason,root);}
  }
  function openUsageView(context,record=true){usageOwner={...context};route='usage';if(record)history.pushState({potRoute:'usage',potScope:{space:context.space,owner:context.owner},potUsage:{...usageOwner}},'',location.href);routeChanged('usage');return show('usage',{keepUsageOwner:true});}
  const popstate=async event=>{
    if(!editor)return false;if(event.state?.potEditor===editor.id)return true;
    const current=editor;if(!await back())history.pushState({...history.state,potEditor:current.id},'',location.href);return true;
  };
  const beforeUnload=event=>{if([...(editor?[editor.session]:[]),...editorParents.map(value=>value.session),...machineDrafts.values()].some(session=>session.dirty||session.pending)){event.preventDefault();event.returnValue='';}};
  const preference=()=>{if(extensionUI){heading(translate(locale(),'extensions.title'));return;}if(editor){editor.lifecycle.locale?.();for(const status of editor.root.querySelectorAll('[data-error-key]')){const span=status.querySelector(':scope>span');if(span)span.textContent=translate(locale(),status.dataset.errorKey);const summary=status.querySelector('details>summary');if(summary)summary.textContent=translate(locale(),'agentLinks.diagnostics');}return;}if(route==='add-assistant'&&assistantStartRefresh){assistantStartRefresh().catch(()=>notify(translate(locale(),'assistantStart.refreshFailed')));return;}if(route==='settings'&&filter.settingsTab==='integrations'&&!editor){heading(translate(locale(),'settings'));for(const node of host.querySelectorAll('[data-settings-i18n]'))node.textContent=translate(locale(),node.dataset.settingsI18n);return;}if(taskView){taskDetail(taskView.task,{recordHistory:false,returnTo:taskView.returnTo});return;}if(scheduleView&&!editor){scheduleHistory(scheduleView.item,false,scheduleView.runsOffset);return;}if(!editor&&route)show(route,{keepUsageOwner:!!usageOwner,force:true});};
  window.addEventListener('beforeunload',beforeUnload);window.addEventListener('pot:locale',preference);
  return {get title(){return title;},get editing(){return !!editor;},get approvalSpace(){return approvalScope().space;},get notificationContext(){return taskView?{space:taskView.scope.space,owner:taskView.scope.owner,task:taskView.task.id}:null;},get taskOwner(){return taskView?.scope.owner||'';},get hasParent(){return !!editor||detailParent||!!usageOwner||route==='add-assistant'&&!host.hidden;},show,assistant,setupAI,leave,back,permissions,syncApprovals:renderApprovals,taskMenu(){taskView?.ui.openMenu();},handleHistory:popstate,openSchedule:scheduleHistory,openUsage:openUsageView,clearApprovals(){approvalQueue?.close();approvalQueue=null;approvalQueueKey='';approvalGate.close();approvalOwner='';pendingApprovals=[];approvalCards.clear();approvalHost.replaceChildren();chatApprovals.update(JSON.stringify(scope()),[]);},approvalFor,decideApproval,taskNeedsApproval,rememberTasks,renderApprovals,conversationUsage(name,record=true){return openUsageView({...scope(),name},record);},openTask(task,options={}){route='tasks';return taskDetail(task,options);},
    showProviders(){filter.settingsTab='providers';return show('settings');},
    refresh(topics=[],source={}){
 if(extensionUI){if(topics.includes('extensions')||topics.includes('spaces'))extensionUI.refresh();return;}
      if(source.boot&&(resourceBoots.get(source.space)||client.runtime.boot_id)!==source.boot){readTaskPages.clear();if(source.boot)resourceBoots.set(source.space,source.boot);for(const key of taskDetails.keys())if(!source.space||JSON.parse(key)[2]===source.space)taskDetails.delete(key);spaceCache.clear();usageCache.clear();if(taskView&&(!source.space||taskView.scope.space===source.space)){taskView.invalidated=true;taskView.readGeneration=(taskView.readGeneration||0)+1;}}
      if(topics.includes('spaces')){readTaskPages.clear();spaceCache.clear();usageCache.clear();taskDetails.clear();if(taskView){taskView.invalidated=true;taskView.readGeneration=(taskView.readGeneration||0)+1;}}
      if(editor){if(topics.includes('extensions')&&(!source.space||source.space===editor.session.context.space))editor.lifecycle.providersInvalidated?.();return;}if(disposed||!route||!host.offsetParent)return;
      if(pageView){if(relevantChange(topics,pageView.topics,pageView.space,source)){pageView.invalidate?.(topics);return pageView.queue.run();}return;}
      if(route==='settings')return;
      if(route==='tasks'&&!relevantChange(topics,['tasks','runner','spaces','assistants','machines',...(filter.taskTab==='schedules'?['schedules']:[])],taskView?.scope.space||'',source))return;
      if(taskListView)return refreshTaskList();if(detailParent&&!taskView&&!scheduleView)return;
      if(taskView)return refreshTaskEvents();

    },
    dispose(){extensionUI?.dispose();stopResources?.();notificationSettings?.dispose();for(const value of [...editorParents,...(editor?[editor]:[])])value.lifecycle?.dispose?.();disposed=true;closeTaskList();closeTaskView();approvalQueue?.close();pageView?.queue.close();gate.close();approvalGate.close();window.removeEventListener('beforeunload',beforeUnload);window.removeEventListener('pot:locale',preference);},
  };
}
