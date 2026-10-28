import {domainsFor} from '../state/domains/index.mjs';
import {mergeTaskHistory} from '../state/task-repository.mjs';
import {taskIdentityMark,taskIdentityShort} from './task-identity.mjs';
import {recordOwner,groupRecords,groupHeading,ownerChoices,createRecordPagination} from './record-groups.mjs';
import {taskIdentities,taskAddress} from '../state/task-identity.mjs';
import {linkDisplayName,linkHostLabel,machinePresentation,machineDisplayName,runtimeMachine} from '../state/machine-identity.mjs';
import {connectionBrand} from './connection-visuals.mjs';
import {createExtensions} from './extensions.mjs';
import {browserTimezone,connectScheduleEditor,beforeRunDraft,hookHistory} from './schedule-editor.mjs';
import {usageCalendar,usageDistributions} from './usage-calendar.mjs';
import {usageRange,usageRangeControls,usageToday} from './usage-period.mjs';
import {notificationSetting} from './notification-setting.mjs';
import {usageNumbers,usageRows,usageTooltip,usageTrend,createUsageList} from './usage.mjs';
import {createTaskConversation} from './task-conversation.mjs';
import {createProviderSave} from '../state/provider-save.mjs';
import {connectProviderEditor} from './provider-connection.mjs';
import {avatar,avatarFromFile} from './avatar.mjs';
import {translate} from '../locales/messages.mjs';
import {icon} from './icons.mjs';
import {node as n, action, field, section, table, details} from './primitives.mjs';
import {modelDefaults,createModelInputDiscovery,providerConversationModels} from '../state/models.mjs';
import {ownedItems,combineTasks,combineUsage} from '../state/aggregate.mjs';
import {bindSearch} from './search.mjs';
import {createEditorSession, createRequestGate} from '../state/editor.mjs';
import {createTaskTranscript} from '../state/task-transcript.mjs';
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
import {supportsCapability} from '../state/agent-links.mjs';
import {createAgentLinks} from './agent-links.mjs';
import {captureConfigurationScope,configurationIdentityCurrent} from '../state/settings-access.mjs';
import {projectTopology,agentNodeID,assistantActivityState} from '../state/topology.mjs';

export function createPages({updates,onApprovalsChanged=()=>{},client, host, approvalHost, chatApprovalHost, chatVisible, locale, notify, changed, titleChanged, openChat, routeChanged=()=>{}}) {
  const machineInfo=machinePresentation(client);
  const gate = createRequestGate(), approvalGate = createRequestGate();
  let assistantStartRefresh=null,assistantLoad=null,assistantCatalogLoad=null;
  const catalogAuthority=new WeakMap();
  let route = '', title = '', editor = null, disposed = false, pageVersion = 0, configurationSpace = '',taskView=null,taskOrigin=null,taskListView=null,scheduleView=null,detailParent=false,usageOwner=null;
  let filter = {taskScope:'active', taskQuery:'', taskPhase:'', listOwner:'', scheduleQuery:'', taskOffset:0, scheduleOffset:0, taskTab:'tasks', usageScope:'all', usageSpace:'all',usageRange:usageRange(),usageYear:Number(usageToday().slice(0,4)),settingsTab:'general'};
  let pendingApprovals=[],approvalFresh=false,notificationSettings=null;
  const domains=domainsFor(client),releaseDomains=domains.retain(),taskDomain=domains.tasks,ownerDisclosures=new Map();
  const providerAllowanceMemory=new Map(),toolMemory=new Map(),usageOffsets=new Map(),approvalCards=new Map(),taskNames=new Map();
  const chatApprovals=createChatApprovals({host:chatApprovalHost,locale,visible:chatVisible,deferred:true});
  const cached = new Map(),machineDrafts=new Map(),editorParents=[],taskDrafts=new Map(),taskDraftVersions=new Map();
  let extensionUI=null;const extensionMemory={};
  let pageView=null,currentViewKey='',viewReady=false,shortcutOpen=null,approvalOwner='',topologySelection='',agentOwnerSpace='';
  const tr = (en, zh) => locale() === 'zh-CN' ? zh : en;
  const scope = () => client.scope();
  const request = (path, options = {}) => client.request(path, options);
  const read = (path,options={}) => client.read(path,options);
  const stopResources=client.resources?.subscribe((meta,error)=>{
    if(disposed||editor||!host.offsetParent||pageView?.domain||taskListView)return;
    if(pageView&&!relevantChange(meta.topics,pageView.topics,pageView.space,{space:meta.space}))return;
    if(taskListView&&!meta.topics.some(topic=>['tasks','machines'].includes(topic)))return;
    if(error){const target=pageView?.root||taskListView?.rows;if(target){let status=target.querySelector('.resource-refresh-error');if(!status){status=n('p','inline-error resource-refresh-error');status.setAttribute('role','alert');target.prepend(status);}status.dataset.resource=JSON.stringify(meta);status.replaceChildren(n('span','',translate(locale(),'loading.stale')),action(translate(locale(),'retry'),()=>{client.resources.invalidate(meta.topics,{space:meta.space});if(pageView)pageView.queue.run();else refreshTaskList();}));}return;}
    const failure=host.querySelector('.resource-refresh-error');if(failure?.dataset.resource===JSON.stringify(meta))failure.remove();
    if(pageView&&relevantChange(meta.topics,pageView.topics,pageView.space,{space:meta.space}))pageView.queue.run();
    else if(taskListView&&meta.topics.some(topic=>['tasks','machines'].includes(topic)))refreshTaskList();
  });
  const uid = () => crypto.randomUUID();
  const list = value => Array.isArray(value) ? value : value?.items || [];
  function captureView(){if(!editor&&viewReady&&currentViewKey&&host.offsetParent)cached.set(currentViewKey,{scroll:host.scrollTop,reading:taskView?.ui.position(),expanded:[...host.querySelectorAll('details[data-cache-key][open]')].map(node=>node.dataset.cacheKey),scrolls:[...host.querySelectorAll('[data-scroll-key]')].map(node=>({key:node.dataset.scrollKey,top:node.scrollTop,left:node.scrollLeft}))});}
  function restoreView(key,token){currentViewKey=key;viewReady=true;requestAnimationFrame(()=>{if(!gate.current(token)||key!==currentViewKey)return;const value=cached.get(key);if(!value){host.scrollTop=0;taskView?.ui.start();return;}host.scrollTop=value.scroll;for(const node of host.querySelectorAll('details[data-cache-key]'))if(node.dataset.cacheKey!=='keyboard-shortcuts')node.open=value.expanded.includes(node.dataset.cacheKey);for(const node of host.querySelectorAll('[data-scroll-key]')){const saved=value.scrolls?.find(item=>item.key===node.dataset.scrollKey);if(saved){node.scrollTop=saved.top;node.scrollLeft=saved.left;}}taskView?.ui.restore(value.reading);});}
  async function across(path, token, {spaceID='all', paginate=false,perAssistant=false,ownerID='',errorTarget=null}={}) {
    const spaces=(client.spaces||[]).filter(space=>spaceID==='all'||space.id===spaceID);
    if(!spaces.length)throw Error(tr('Space is unavailable.','Space 不可用。'));
    const targets=spaces.flatMap(space=>perAssistant?(client.assistants||[]).filter(item=>supportsCapability(item,'assistant.usage')&&item.space_id===space.id).map(item=>({space,owner:item.id})):[{space,owner:ownerID}]);
    const results=await Promise.allSettled(targets.map(async({space,owner})=>{
      try {
        const shared=paginate&&client.resources;
        const options={owner,space:space.id,signal:shared?undefined:token.signal};
        const readPage=paginate?(client.transport||request):read;
        const readPages=async()=>{
          let value=await readPage(path,options);
          if(paginate&&value?.next_offset!=null) {
            let next=value.next_offset,seen=new Set();const items=[...(value.items||[])];
            while(next!=null&&items.length<5000) {if(seen.has(next))throw Error('Invalid pagination cursor');seen.add(next);const url=new URL(path,location.origin);url.searchParams.set('offset',next);const page=await readPage(url.pathname+url.search,options);items.push(...(page.items||[]));next=page.next_offset;}
            if(next!=null)throw Error(tr('Too many records. Narrow the filter.','记录过多，请缩小筛选范围。'));value={...value,items,next_offset:null};
          }
          return value;
        };
        const value=path==='/api/v1/permissions'&&client.resources?await client.resources.policySummary({...options,signal:token.signal}):shared?await client.resources.collection(path,{...options,signal:token.signal},readPages):await readPages();

        const freshness=client.resources?.state(value)?.freshness;return {space,owner,value,stale:freshness==='retained'||freshness==='stale'};
      }catch(reason){if(reason.name==='AbortError')throw reason;const saved=await client.resources?.query(path==='/api/v1/permissions'?'@permissions-policy':path,{space:space.id,owner,collection:paginate||path==='/api/v1/permissions'}).catch(()=>null);if(saved)return {space,owner,value:saved.value,stale:true,error:reason.message};throw Object.assign(reason,{space});}
    }));
    if(!gate.current(token))return [];
    const failed=results.filter(value=>value.status==='rejected');
    const pages=results.filter(value=>value.status==='fulfilled').map(value=>value.value);
    (errorTarget||host).querySelector('.space-availability')?.remove();
    if((failed.length||pages.some(page=>page.stale))&&!(errorTarget||host).querySelector('.space-availability')) {const message=n('p','inline-error space-availability',route==='usage'?tr('Some Spaces are unavailable; totals may be incomplete and cached rows are marked stale.','部分 Space 不可用，用量可能不完整，缓存项目标记为过期。'):tr('Some Spaces are unavailable. Cached rows are marked stale.','部分 Space 不可用，缓存项目标记为过期。'));(errorTarget||host.querySelector('.page-content'))?.prepend(message);}
    if(!pages.length&&targets.length)throw failed[0]?.reason||Error('No Space data');pages.partial=failed.length>0||pages.some(page=>page.stale);return pages;
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
  function connectDomain(root,token,watch,refresh,render){
    const status=n('p','hint page-refresh-status',translate(locale(),'loading.pending'));root.prepend(status);
    let stop,settled=false;const ready=new Promise(resolve=>{
      stop=watch(snapshot=>{if(disposed||!gate.current(token))return;root.setAttribute('aria-busy',String(snapshot.status==='loading'));status.hidden=snapshot.status!=='loading'&&snapshot.status!=='error';status.setAttribute('role',snapshot.error?'alert':'status');status.classList.toggle('inline-error',!!snapshot.error);
        if(snapshot.error){status.replaceChildren(n('span','',translate(locale(),'loading.stale')),action(translate(locale(),'retry'),refresh));}
        if(snapshot.data!==undefined)render(snapshot.data,snapshot);
        if(!settled&&(snapshot.data!==undefined||snapshot.status==='error')){settled=true;resolve();}
      });token.signal.addEventListener('abort',()=>{stop();resolve();},{once:true});
    });pageView={root,token,domain:true,topics:[],queue:{run:refresh,close:()=>stop?.()}};return ready;
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
  async function confirmAssistantDelete(name,remote=false) {
    return new Promise(resolve=>{
      const dialog=n('dialog','preferences'),title=n('h2'),description=n('p','hint');
      const finish=value=>{dialog.close();resolve(value);};
      const cancel=action('',()=>finish(false)),confirm=action('',()=>finish(true),{danger:true});
      const refresh=()=>{title.textContent=translate(locale(),'assistant.deleteTitle').replace('{name}',name);description.textContent=translate(locale(),remote?'assistant.deleteRemoteBody':'assistant.deleteBody');cancel.textContent=translate(locale(),'assistant.deleteCancel');confirm.textContent=translate(locale(),'assistant.delete');};
      title.id='assistant-delete-title';dialog.setAttribute('aria-labelledby',title.id);description.id='assistant-delete-description';dialog.setAttribute('aria-describedby',description.id);
      dialog.append(title,description,toolbar(cancel,confirm));document.body.append(dialog);refresh();
      window.addEventListener('pot:locale',refresh);
      dialog.addEventListener('cancel',event=>{event.preventDefault();finish(false);});
      dialog.addEventListener('close',()=>{window.removeEventListener('pot:locale',refresh);dialog.remove();},{once:true});dialog.showModal();cancel.focus();
    });
  }
  async function leave({toParent=false}={}) {
 if(extensionUI&&!await extensionUI.leave())return false;extensionUI?.dispose();extensionUI=null;
    captureView();assistantStartRefresh=null;assistantLoad=null;
    const sessions=[...(editor?[editor]:[]),...(!toParent?[...editorParents,...[...machineDrafts.values()].map(session=>({session}))]:[])];
    if(sessions.some(value=>value.session.pending)) {notify(tr('Please wait for the save result.','请等待保存结果。'));return false;}
    if(sessions.some(value=>value.session.dirty)&&!await confirmDiscard())return false;
    assistantCatalogLoad?.close();
    for(const value of sessions)value.lifecycle?.dispose?.();
    pageView?.queue.close();pageView=null;editor=null;if(!toParent){editorParents.length=0;machineDrafts.clear();}closeTaskView();taskOrigin=null;detailParent=false;closeTaskList();scheduleView=null;viewReady=false;gate.close();host.replaceChildren();return true;
  }
  function suspendEditor(){if(editor){editor.scroll=host.scrollTop;editor.focus=innerWidth>=720&&editor.root.contains(document.activeElement)?document.activeElement:null;editorParents.push(editor);editor=null;}}
  const editorAuthorityReady=()=>!editor?.capability||!client.supports||client.supports(editor.capability,editor.session.context);
  function renderEditor(label, initial, definitions, save, {returnChat=false,capturedScope,capability='',submitLabel=tr('Save','保存'),editableWhilePending:pendingEdits}={}) {
    captureView();viewReady=false;
    pageView?.queue.close();pageView=null;closeTaskList();gate.close();
    const captured=captureConfigurationScope(client,capturedScope||scope()), caller=route || 'settings';
    const editableWhilePending=pendingEdits??(caller==='settings'||['assistant.configure','assistant.profile'].includes(capability));
    const session=createEditorSession({...captured,route:caller,boot:client.runtime.boot_id},initial,{editableWhilePending});
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
    editor={session,caller,returnChat,capability,root,form,controls,lifecycle,label,returnUsageOwner:usageOwner?{...usageOwner}:null,id:uid()};
    // All configuration steps share the Add assistant navigation entry.
    history[caller==='add-assistant'?'replaceState':'pushState']({...history.state,potEditor:editor.id},'',location.href);heading(label);
    form.onsubmit=async event=>{
      event.preventDefault();if(editor?.session!==session||disposed||lifecycle.creationRetired||!form.reportValidity())return;
      const attempt=session.begin();if(!attempt)return;
      const submission=lifecycle.captureSave?.();
      const disabled=new Map([...controls.values()].map(control=>[control,control.disabled]));
      if(!editableWhilePending)for(const control of controls.values())control.disabled=true;
      status.textContent='';status.className='inline-error';status.setAttribute('role','alert');delete status.dataset.errorKey;cancel.disabled=submit.disabled=true;
      if(lifecycle.opensCreatedChat){submit.setAttribute('aria-busy','true');submit.textContent=translate(locale(),'assistant.creating');}
      try {
        if(lifecycle.opensCreatedChat&&!lifecycle.creationCurrent()){lifecycle.creationRetired=true;throw Object.assign(Error('Creation identity changed'),{uiKey:'identityChanged'});}
        if(client.runtime.boot_id!==session.context.boot)throw Object.assign(Error('Configuration boot changed'),{uiKey:'identityChanged'});
        if(client.production){
          if(!configurationIdentityCurrent(client,captured))throw Object.assign(Error('Configuration identity changed'),{uiKey:'identityChanged'});
          if(client.onlineReady===false)throw Object.assign(Error('Control unavailable'),{uiKey:'startup.local'});
          if(client.operationScopeReady?.(captured)===false)await domains.directory.refresh();
          if(editor?.session!==session||disposed||!configurationIdentityCurrent(client,captured))throw Object.assign(Error('Configuration identity changed'),{uiKey:'identityChanged'});
          if(client.operationScopeReady?.(captured)===false)throw Object.assign(Error('Authority unavailable'),{uiKey:'startup.local'});
          const required=lifecycle.requiredCapabilities?.(attempt.values,submission)||(capability?[[capability]]:[]);
          if(required.some(group=>!group.some(operation=>client.supports?.(operation,captured)!==false)))throw Object.assign(Error('Capability revoked'),{uiKey:'agentLinks.capabilityUnavailable'});
        }
        const saved=await save(attempt.values,{...captured,request_id:attempt.request_id},session,submission);
        if(editor?.session!==session||disposed||!configurationIdentityCurrent(client,captured))throw Object.assign(Error('Configuration identity changed'),{uiKey:'identityChanged'});
        session.settle(true,attempt.values);
        if(lifecycle.opensCreatedChat){if(!lifecycle.creationCurrent()){lifecycle.creationRetired=true;throw Object.assign(Error('Creation identity changed'),{uiKey:'identityChanged'});}await lifecycle.afterSave(saved);Promise.resolve().then(()=>changed(saved)).catch(()=>{});}
        else{try{await changed(saved);}catch{}if(session.dirty){status.className='hint';status.setAttribute('role','status');status.dataset.errorKey='settings.savedDraftRemaining';status.replaceChildren(n('span','',translate(locale(),'settings.savedDraftRemaining')));}else if(lifecycle.afterSave)await lifecycle.afterSave(saved);else await back();}
      } catch(reason) {if(lifecycle.opensCreatedChat&&!lifecycle.creationCurrent())lifecycle.creationRetired=true;session.settle(false);const key=reason.uiKey||'settings.saveFailed';status.dataset.errorKey=key;status.replaceChildren(n('span','',translate(locale(),key)));const diagnostic=reason.diagnostic||(!reason.uiKey&&reason.message);if(diagnostic){const details=n('details','detail-disclosure');details.append(n('summary','',translate(locale(),'agentLinks.diagnostics')),n('pre','',diagnostic));status.append(details);}}
      finally {if(!editableWhilePending)for(const [control,value]of disabled)control.disabled=value||!!lifecycle.creationRetired;cancel.disabled=false;submit.disabled=!!lifecycle.creationRetired;if(lifecycle.opensCreatedChat){submit.removeAttribute('aria-busy');lifecycle.locale?.();if(lifecycle.creationRetired)form.querySelector('.assistant-runtime-summary button').disabled=true;}lifecycle.settled?.();lifecycle.refreshDelete?.();}
    };
    return {session,root,form,controls,lifecycle};
  }
  async function back({skipPicker=false}={}) {
    if(assistantLoad){await leave();route='';await openChat();return true;}
    if(!editor&&route==='add-assistant'){await leave();route='';await openChat({directory:true});return true;}
    const current=editor,taskReturn=taskView?.returnTo||taskOrigin,returnToConversation=!editor&&usageOwner;
    if(!await leave({toParent:true}))return false;
    if(skipPicker&&editorParents.at(-1)?.kind==='picker')editorParents.pop();
    if(editorParents.length){editor=editorParents.pop();host.replaceChildren(editor.root);heading(editor.label);editor.lifecycle.locale?.();history.replaceState({...history.state,potEditor:editor.id},'',location.href);host.scrollTop=editor.scroll||0;editor.focus?.focus({preventScroll:true});return true;}
    if(returnToConversation&&usageOwner.returnTask){const task=usageOwner.returnTask,returnTo=usageOwner.taskReturn;usageOwner=null;route='tasks';routeChanged('tasks');const state={potRoute:'tasks',potScope:{space:task._space_id,owner:task.assistant_id},potTask:task,potTaskReturn:returnTo};history.replaceState(state,'',location.href);await taskDetail(task,{recordHistory:false,returnTo});return true;}
    if(!current&&taskReturn?.route==='chat'){const target=taskReturn.scope;usageOwner=null;detailParent=false;route='';history.replaceState({potRoute:'chat',potScope:target,potDirectory:false},'',location.href);await openChat(target);return true;}
    if(returnToConversation){usageOwner=null;const state={...history.state,potRoute:'chat'};delete state.potUsage;history.replaceState(state,'',location.href);openChat();return true;}
    if(current?.returnSchedule){const state={...history.state};delete state.potEditor;history.replaceState(state,'',location.href);await scheduleHistory(current.returnSchedule,false);return true;}
    const state={...history.state};for(const key of ['potEditor','potTask','potTaskReturn','potSchedule','potResource','potUsage'])delete state[key];state.potRoute=current?.returnChat||returnToConversation?'chat':current?.caller||route||'settings';history.replaceState(state,'',location.href);
    if(current?.returnChat)openChat();else{usageOwner=current?.returnUsageOwner||usageOwner;show(current?.caller||route||'settings',{keepUsageOwner:!!usageOwner});}return true;
  }
  function assistantDeleteEditor(form,definition,captured,getRevision) {
    if(!supportsCapability(definition,'assistant.delete'))return;
    const session=form.session;
    const t=key=>translate(locale(),key),more=n('details','editor-disclosure assistant-delete');more.dataset.section='delete';
    const summary=n('summary'),remove=action('',async()=>{
      if(!session.begin())return;
      const disabled=new Map([...form.form.querySelectorAll('input,select,textarea,button')].map(node=>[node,node.disabled]));
      for(const node of disabled.keys())node.disabled=true;
      deleteError='';refreshDelete();
      try{
        if(!await confirmAssistantDelete(definition.name,!!definition.origin))return;
        if(editor?.session!==session||disposed)return;
        if(!available()){deleteError='assistant.deleteOffline';return;}
        if(client.runtime.boot_id!==session.context.boot){deleteError='assistant.deleteRuntimeChanged';return;}
        await domains.directory.remove(captured,definition.id,getRevision());
        session.settle(true);await leave();route='chat';
        history.replaceState({potRoute:'chat',potScope:{space:captured.space,owner:''},potDirectory:true},'',location.href);
        routeChanged('chat');await changed({deletedAssistant:{space:captured.space,owner:definition.id}});
      }catch(reason){deleteError=reason.code==='assistant_busy'?'assistant.deleteBusy':reason.code==='assistant_changed'?'assistant.deleteChanged':'assistant.deleteFailed';}
      finally{if(session.pending)session.settle(false);for(const [node,value]of disabled)node.disabled=value;refreshDelete();}
    },{danger:true});
    const status=n('p','inline-error');status.setAttribute('role','alert');let deleteError='';
    const available=()=>{const item=client.assistants?.find(item=>item.id===definition.id&&item.space_id===captured.space)||(!client.assistants?definition:null);return !!item&&!item.offline&&supportsCapability(item,'assistant.delete');};
    const refreshDelete=()=>{summary.textContent=t('assistant.moreActions');remove.textContent=t('assistant.delete');remove.disabled=session.pending||!available();const key=!available()?'assistant.deleteOffline':deleteError;status.textContent=key?t(key):'';status.hidden=!key;};
    more.append(summary,remove,status);form.form.querySelector('.editor-fields').append(more);refreshDelete();
    window.addEventListener('pot:locale',refreshDelete);const dispose=form.lifecycle.dispose;form.lifecycle.dispose=()=>{dispose?.();window.removeEventListener('pot:locale',refreshDelete);};form.lifecycle.refreshDelete=refreshDelete;
  }
  function renderAssistantLoad() {
    const view=assistantLoad;if(!view)return;
    const t=key=>translate(locale(),key);
    heading(t('assistantSettings'));view.root.setAttribute('aria-busy',String(!view.error));
    if(!view.error){view.root.replaceChildren(n('p','hint',t('loading.pending')));return;}
    const box=n('div','inline-error');box.setAttribute('role','alert');
    const diagnostic=n('details','detail-disclosure');diagnostic.append(n('summary','',t('agentLinks.diagnostics')),n('pre','',view.error.message||String(view.error)));
    box.append(n('p','',t('assistant.settingsLoadFailed')),action(t('retry'),()=>assistant(view.definition,view.scope)),diagnostic);view.root.replaceChildren(box);
  }
  async function assistant(definition,capturedScope={...scope(),owner:definition.id,space:definition.space_id??scope().space}) {
    if(!await leave())return;
    const token=gate.next(),captured=captureConfigurationScope(client,capturedScope),loading=body();assistantLoad={definition,scope:captured,root:loading};renderAssistantLoad();
    try{
      if(supportsCapability(definition,'assistant.profile')&&!supportsCapability(definition,'assistant.configure')){
        assistantLoad=null;
        await linkedProfile(definition,{name:definition.name,avatar:definition.avatar||'',revision:definition.revision},captured);return;
      }
      assistantLoad=null;
      let providers={},thread,rules,configurationReady=false,instructionsReady=false;
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
      ],async(values,context,session,sections)=>{
        const selection=sections.configurationReady?selectValue(values):null;
        const selectionFields=['profile_id','model','context_window','max_output_tokens','reasoning_effort'];
        const selectionChanged=sections.configurationReady&&selectionFields.some(key=>JSON.stringify(values[key])!==JSON.stringify(initial[key]));
        if(selectionChanged&&(!selection||!values.model.trim()))throw Object.assign(Error('Provider and model required'),{uiKey:'settings.providerRequired'});
        if(values.name!==initial.name||values.avatar!==initial.avatar){const saved=await domains.directory.update(captured,definition.id,{name:values.name,...(values.avatar!==initial.avatar?{avatar:values.avatar}:{}),revision});revision=saved.revision;initial.name=values.name;initial.avatar=values.avatar;session.acknowledge({name:values.name,avatar:values.avatar});}
        const signature=JSON.stringify(selection);
        if(selectionChanged&&selection&&signature!==initialSelection){const saved=await domains.directory.configure(captured,{request_id:context.request_id,expected_generation:generation,expected_revision:configurationRevision,selection});generation=saved.generation;configurationRevision=saved.revision;initialSelection=signature;const fields=Object.fromEntries([...selectionFields,'model_selection'].map(key=>[key,values[key]]));Object.assign(initial,fields);session.acknowledge(fields);}
        if(sections.instructionsReady&&values.text!==initial.text){const saved=await domains.directory.saveInstructions(captured,{text:values.text,revision:rulesRevision});rulesRevision=saved.revision;initial.text=values.text;session.acknowledge({text:values.text});}
      },{returnChat:true,capturedScope:captured,capability:'assistant.configure'});
      const controls=form.controls,session=form.session;
      form.lifecycle.captureSave=()=>({configurationReady,instructionsReady});
      form.lifecycle.requiredCapabilities=(values,sections)=>{
        const required=[];
        if(values.name!==initial.name||values.avatar!==initial.avatar)required.push(['assistant.configure','assistant.profile']);
        if(sections.configurationReady&&['profile_id','model','context_window','max_output_tokens','reasoning_effort'].some(key=>JSON.stringify(values[key])!==JSON.stringify(initial[key]))||sections.instructionsReady&&values.text!==initial.text)required.push(['assistant.configure']);
        return required;
      };
      assistantDeleteEditor(form,definition,captured,()=>revision);

      editor.kind='assistant';
      const preview=avatar(56,definition.avatar);
      form.form.querySelector('.editor-fields').prepend(preview);
      let avatarPending=false;
      const editAvatar=async work=>{
        if(avatarPending||session.pending)return;avatarPending=true;
        const submit=form.form.querySelector('.editor-actions .primary'),disabled=submit.disabled;submit.disabled=true;changeAvatar.disabled=resetAvatar.disabled=true;
        avatarError.hidden=true;
        try{const {value,image}=await work();if(editor?.session!==session||disposed||!configurationIdentityCurrent(client,captured))return;session.set('avatar',value);preview.replaceChildren(...avatar(56,image).childNodes);}
        catch{avatarError.hidden=false;avatarError.textContent=translate(locale(),'assistant.avatarFailed');}
        finally{avatarPending=false;submit.disabled=disabled;changeAvatar.disabled=resetAvatar.disabled=false;}
      };
      form.form.addEventListener('submit',event=>{if(avatarPending){event.preventDefault();event.stopImmediatePropagation();}},{capture:true});
      const avatarActions=toolbar(),upload=n('input');upload.type='file';upload.accept='image/png,image/jpeg,image/webp';upload.hidden=true;
      const changeAvatar=action('',()=>upload.click()),resetAvatar=action('',()=>editAvatar(async()=>{
        const generated=await domains.directory.defaultAvatar(captured);return {value:'',image:generated.avatar};
      }));
      const avatarHelp=n('p','hint'),avatarError=n('p','inline-error');avatarError.setAttribute('role','alert');avatarError.hidden=true;
      avatarActions.append(changeAvatar,resetAvatar,upload);preview.after(avatarActions,avatarHelp,avatarError);
      upload.onchange=async()=>{
        const file=upload.files?.[0];upload.value='';if(!file)return;
        await editAvatar(async()=>{const image=await avatarFromFile(file);return {value:image,image};});
      };
      const set=(name,value)=>{controls.get(name).value=value??'';session.set(name,value);};
      const defaults=()=>{const profile=profiles.find(value=>value.id===session.values.profile_id),catalog=profile?.model_settings?.catalog_provider||'custom',limits=modelDefaults(providers,catalog,session.values.model);const same=profile?.model===session.values.model;controls.get('context_window').placeholder=String(same?(profile?.model_settings?.context_window||limits.context):limits.context);controls.get('max_output_tokens').placeholder=String(same?(profile?.model_settings?.max_output_tokens||limits.output):limits.output);controls.get('reasoning_effort').placeholder=profile?.params?.reasoning_effort||tr('Provider default','提供商默认值');controls.get('profile_id').required=false;controls.get('model').required=false;};
      const updateModels=()=>{const profile=profiles.find(value=>value.id===session.values.profile_id),catalog=(providers.directory||[]).find(value=>value.id===profile?.model_settings?.catalog_provider);const options=new Map();if(profile?.model)options.set(profile.model,profile.model);for(const model of catalog?.models||[])options.set(model.id,model.name||model.id);for(const model of providers.configured_models?.[profile?.id]||profile?.models||[])options.set(typeof model==='string'?model:model.id,typeof model==='string'?model:model.name||model.id);const facts=(providers.connections||[]).find(value=>value.profile_id===profile?.id)?.catalog?.models||[];const allowed=new Set(providerConversationModels(facts,session.values.model).map(value=>value.id));for(const model of facts){if(allowed.has(model.id))options.set(model.id,model.name||model.id);else options.delete(model.id);}const input=controls.get('model_selection');input.replaceChildren();for(const [value,label]of [['',tr('Enter model ID','输入模型 ID')],...options]){const option=n('option','',label);option.value=value;input.append(option);}set('model_selection',options.has(session.values.model)?session.values.model:'');session.acknowledge({model_selection:session.values.model_selection});controls.get('model').closest('.form-row').hidden=!!input.value;defaults();};
      controls.get('profile_id').addEventListener('change',()=>{const selected=profiles.find(value=>value.id===session.values.profile_id);set('model',selected?.model||'');updateModels();});
      controls.get('model_selection').addEventListener('change',()=>{if(controls.get('model_selection').value)set('model',controls.get('model_selection').value);controls.get('model').closest('.form-row').hidden=!!controls.get('model_selection').value;defaults();});controls.get('model').addEventListener('input',defaults);updateModels();
      const instructionsRow=controls.get('text').closest('.form-row'),parameters=n('details','editor-disclosure');parameters.dataset.section='parameters';parameters.append(n('summary','',tr('Model parameters','模型参数')));for(const name of ['context_window','max_output_tokens','reasoning_effort'])parameters.append(controls.get(name).closest('.form-row'));instructionsRow.before(parameters);
      const instructions=n('details','editor-disclosure');instructions.dataset.section='instructions';instructions.append(n('summary'));instructionsRow.before(instructions);instructions.append(instructionsRow);
      form.lifecycle.locale=()=>{const t=key=>translate(locale(),key),label=t('assistant.personality');instructions.querySelector('summary').textContent=label;instructionsRow.querySelector('.form-label').textContent=label;controls.get('text').setAttribute('aria-label',label);instructionsRow.querySelector('small').textContent=t('assistant.personalityHelp');changeAvatar.textContent=t('assistant.changeAvatar');resetAvatar.textContent=t('assistant.resetAvatar');avatarHelp.textContent=t('assistant.avatarHelp');if(!avatarError.hidden)avatarError.textContent=t('assistant.avatarFailed');};form.lifecycle.locale();
      const providerHelp=n('p','hint',tr('Connect a Provider before creating an assistant.','请先连接提供商，再创建助手。'));form.form.querySelector('.editor-fields').prepend(providerHelp);providerHelp.hidden=true;
      const addProvider=action(tr('+ Add Provider','＋ 添加提供商'),()=>providerPicker(providers,{space:session.values.space_id||captured.space,owner:''}));controls.get('profile_id').closest('.field-body').append(addProvider);
      form.lifecycle.providersChanged=async saved=>{const value=await domains.providers.query({space:session.values.space_id||captured.space},{authoritative:true});if(editor?.session!==session)return;profiles=value.profiles||[];providers=value;providerHelp.hidden=profiles.length>0;const input=controls.get('profile_id');input.replaceChildren();for(const profile of profiles){const option=n('option','',profile.name);option.value=profile.id;input.append(option);}const selected=profiles.find(profile=>profile.id===saved?.id)||profiles.find(profile=>profile.id===session.values.profile_id)||profiles[0];const changedProvider=selected?.id!==session.values.profile_id;set('profile_id',selected?.id||'');if(changedProvider||!session.values.model)set('model',selected?.model||'');updateModels();};
      form.lifecycle.settled=defaults;
      const configurationFields=['profile_id','model','model_selection','context_window','max_output_tokens','reasoning_effort'];
      const loadingStatus=n('p','hint'),rulesStatus=n('p','hint');
      let pendingHint;
      controls.get('profile_id').closest('.form-row').before(loadingStatus);instructions.append(rulesStatus);
      const controller=new AbortController(),dispose=form.lifecycle.dispose;form.lifecycle.dispose=()=>{controller.abort();dispose?.();};
      const current=()=>editor?.session===session&&!disposed&&!controller.signal.aborted;
      const adopt=values=>{for(const [key,value]of Object.entries(values)){set(key,value);initial[key]=value;}session.acknowledge(values);};
      const loadSection=async(status,fields,work)=>{
        for(const key of fields)controls.get(key).disabled=true;
        status.hidden=false;status.className='hint';status.dataset.sectionState='loading';status.textContent=translate(locale(),'loading.pending');
        try{await work();if(current())status.hidden=true;}
        catch(reason){if(current()){status.className='inline-error';status.dataset.sectionState='error';status.replaceChildren(n('span','',translate(locale(),'assistant.settingsLoadFailed')),action(translate(locale(),'retry'),()=>loadSection(status,fields,work)));}}
        finally{if(current())for(const key of fields)controls.get(key).disabled=status.className==='inline-error';}
      };
      const refreshLocale=form.lifecycle.locale;form.lifecycle.locale=()=>{refreshLocale();for(const status of [loadingStatus,rulesStatus]){if(status.dataset.sectionState==='loading')status.textContent=translate(locale(),'loading.pending');else if(status.dataset.sectionState==='error'){status.querySelector('span').textContent=translate(locale(),'assistant.settingsLoadFailed');status.querySelector('button').textContent=translate(locale(),'retry');}}if(pendingHint)pendingHint.textContent=translate(locale(),'settings.nextTurn');};
      await Promise.all([
        loadSection(loadingStatus,configurationFields,async()=>{
          const options={...captured,signal:controller.signal};
          const [catalog,configuration]=await Promise.all([domains.providers.query(options,{authoritative:true}),domains.directory.configuration(captured,controller.signal)]);
          if(!current())return;
          if(!configurationIdentityCurrent(client,captured))throw Object.assign(Error('Configuration identity changed'),{uiKey:'identityChanged'});
          providers=catalog;thread=configuration;profiles=providers.profiles||[];generation=thread.generation||0;configurationRevision=thread.revision||0;
          const selection=thread.selection||{},profileID=selection.profile_id||thread.config?.source_profile_id||providers.default_provider_id||'',profile=profiles.find(value=>value.id===profileID);
          const input=controls.get('profile_id');input.replaceChildren();for(const [value,label]of [['',tr('Select a Provider','选择提供商')],...profiles.map(value=>[value.id,value.name])]){const option=n('option','',label);option.value=value;input.append(option);}
          adopt({profile_id:profileID,model:selection.model||thread.config?.model||profile?.model||'',context_window:selection.context_window??null,max_output_tokens:selection.max_output_tokens??null,reasoning_effort:selection.reasoning_effort||''});
          configurationReady=true;initialSelection=JSON.stringify({...selectValue(session.values),profile_revision:selection.profile_revision||profile?.revision});updateModels();providerHelp.hidden=profiles.length>0;
          if(thread.pending){pendingHint=n('p','hint',translate(locale(),'settings.nextTurn'));form.root.prepend(pendingHint);}
        }),
        loadSection(rulesStatus,['text'],async()=>{const value=await domains.directory.instructions(captured,controller.signal);if(!current())return;if(!configurationIdentityCurrent(client,captured))throw Object.assign(Error('Configuration identity changed'),{uiKey:'identityChanged'});rulesRevision=value.revision;adopt({text:value.text||''});instructionsReady=true;}),
      ]);
    }catch(reason){if(gate.current(token)&&!disposed){assistantLoad={definition,scope:captured,root:loading,error:reason};renderAssistantLoad();}}
  }
  async function linkedProfile(definition,profile,captured){
    const t=key=>translate(locale(),key);
    let revision=profile.revision,loaded=false,profileConflict=false,readFlight;
    const initial={name:profile.name,avatar:profile.avatar};
    const form=renderEditor(t('assistantSettings'),initial,[{name:'name',label:t('assistant.name'),required:true}],async(values,context,session,submitted)=>{
      try{
        if(!loaded)await loadProfile();
        if(profileConflict)throw Object.assign(Error('Profile changed while editing'),{uiKey:'agentLinks.assistantChanged'});
        if(editor?.session!==form.session||disposed||!configurationIdentityCurrent(client,captured))throw Object.assign(Error('Configuration identity changed'),{uiKey:'identityChanged'});
        if(!Object.keys(submitted).length)return profile;
        const saved=await domains.directory.update(captured,definition.id,{...submitted,revision});
        revision=saved.revision;Object.assign(initial,submitted);Object.assign(profile,saved);session.acknowledge(submitted);return saved;
      }catch(error){throw Object.assign(error,{uiKey:error.uiKey||'assistant.profileFailed',diagnostic:error.message});}
    },{returnChat:true,capturedScope:captured,capability:'assistant.profile'});
    form.lifecycle.captureSave=()=>Object.fromEntries(['name','avatar'].filter(key=>form.session.values[key]!==initial[key]).map(key=>[key,form.session.values[key]]));
    editor.kind='assistant';
    const preview=avatar(56,profile.avatar),actions=toolbar(),upload=n('input'),help=n('p','hint'),error=n('p','inline-error');
    upload.type='file';upload.accept='image/png,image/jpeg,image/webp';upload.hidden=true;error.hidden=true;error.setAttribute('role','alert');
    const edit=async work=>{
      if(!form.session.begin())return;
      const controls=[...form.form.querySelectorAll('input,button')];for(const node of controls)node.disabled=true;
      error.hidden=true;
      try{const {value,image}=await work();form.session.settle(false);form.session.set('avatar',value);preview.replaceChildren(...avatar(56,image).childNodes);}
      catch{form.session.settle(false);error.hidden=false;error.textContent=t('assistant.avatarFailed');}
      finally{for(const node of controls)node.disabled=false;form.lifecycle.refreshDelete?.();}
    };
    const change=action('',()=>upload.click()),reset=action('',()=>edit(async()=>{const value=await domains.directory.defaultAvatar(captured);return {value:'',image:value.avatar};}));
    upload.onchange=async()=>{const file=upload.files?.[0];upload.value='';if(file)await edit(async()=>{const image=await avatarFromFile(file);return {value:image,image};});};
    actions.append(change,reset,upload);form.form.querySelector('.editor-fields').prepend(preview,actions,help,error);
    form.lifecycle.locale=()=>{
      form.lifecycle.setTitle?.(t('assistantSettings'));
      const name=form.controls.get('name'),buttons=form.form.querySelector('.editor-actions').children;
      name.closest('.form-row').querySelector('.form-label').textContent=t('assistant.name');name.setAttribute('aria-label',t('assistant.name'));
      buttons[0].textContent=t('assistant.cancel');buttons[1].textContent=t('assistant.save');
      change.textContent=t('assistant.changeAvatar');reset.textContent=t('assistant.resetAvatar');help.textContent=t('assistant.avatarHelp');
      if(!error.hidden)error.textContent=t('assistant.avatarFailed');
    };form.lifecycle.locale();
    assistantDeleteEditor(form,definition,captured,()=>revision);
    const loading=n('p','hint'),controller=new AbortController(),priorDispose=form.lifecycle.dispose;
    form.form.querySelector('.editor-fields').prepend(loading);
    form.lifecycle.dispose=()=>{controller.abort();priorDispose?.();};
    const current=()=>editor?.session===form.session&&!disposed&&!controller.signal.aborted;
    const renderLoading=()=>{if(loaded){loading.hidden=true;return;}loading.hidden=false;if(loading.dataset.error){loading.className='inline-error';loading.replaceChildren(n('span','',t('assistant.settingsLoadFailed')),action(t('retry'),()=>loadProfile().catch(()=>{})));}else{loading.className='hint';loading.textContent=t('loading.pending');}};
    const refreshLocale=form.lifecycle.locale;form.lifecycle.locale=()=>{refreshLocale();renderLoading();};
    function loadProfile(){
      if(readFlight)return readFlight;
      delete loading.dataset.error;renderLoading();
      readFlight=(async()=>{
        try{
          const value=await domains.directory.profile(captured,controller.signal);if(!current())return;
          if(!configurationIdentityCurrent(client,captured))throw Object.assign(Error('Configuration identity changed'),{uiKey:'identityChanged'});
          profileConflict=form.session.dirty&&(revision!=null&&revision!==value.revision||['name','avatar'].some(key=>(value[key]||'')!==initial[key]&&form.session.values[key]!==initial[key]));
          revision=value.revision;
          Object.assign(profile,value);
          const adopted={};for(const key of ['name','avatar'])if(form.session.values[key]===initial[key]){const next=value[key]||'';form.session.set(key,next);initial[key]=next;adopted[key]=next;if(key==='name')form.controls.get(key).value=next;else preview.replaceChildren(...avatar(56,next).childNodes);}
          form.session.acknowledge(adopted);loaded=true;renderLoading();
        }catch(reason){if(current()){loading.dataset.error='true';renderLoading();}throw reason;}
        finally{readFlight=null;}
      })();return readFlight;
    }
    await loadProfile().catch(()=>{});
  }
  async function extensions(root,token){extensionUI=createExtensions({root,client,locale,memory:extensionMemory,onSettings:(section='integrations')=>{filter.settingsTab=section;routeChanged('settings');show('settings');},onPermissions:()=>{routeChanged('machines');show('machines');}});await extensionUI.load();}
  async function settings(root, token) {
    root.classList.add('settings-content');const settingsKeys=['settings.general','settings.providers','integrations.title','settings.tasks'];const tabs=segments(['general','providers','integrations','tasks'].map((key,i)=>[key,translate(locale(),settingsKeys[i])]),filter.settingsTab,value=>{filter.settingsTab=value;show('settings');});[...tabs.children].forEach((node,i)=>{node.dataset.settingsI18n=settingsKeys[i];});root.append(tabs);
    if(filter.settingsTab==='general'){
    const preferences=card(tr('General','通用'));
    const preference=(key,value)=>{try{localStorage.setItem(key,value);}catch{}window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key,value}}));};
    const language=field(tr('Language','语言'),locale(),{choices:[['en','English'],['zh-CN','简体中文']]});language.row.classList.add('preference-setting');language.input.onchange=()=>preference('pot-ui-locale',language.input.value);preferences.append(language.row);
    const appearance=n('div','preference-setting appearance-setting');appearance.append(n('span','',tr('Appearance','外观')),segments([['system',tr('System','跟随系统')],['light',tr('Light','浅色')],['dark',tr('Dark','深色')]],document.documentElement.dataset.theme||'system',value=>preference('pot-ui-theme',value)));preferences.append(appearance);if(client.notifications){notificationSettings??=notificationSetting({notifications:client.notifications,locale,desktop:client.desktop});preferences.append(notificationSettings.element);}root.append(preferences);

    const shortcuts=n('details','page-card shortcut-card');shortcuts.dataset.cacheKey='keyboard-shortcuts';shortcuts.open=shortcutOpen??innerWidth>=720;const shortcutSummary=n('summary','',tr('Keyboard shortcuts','快捷键'));shortcutSummary.onclick=()=>{shortcutOpen=!shortcuts.open;};shortcuts.append(shortcutSummary);for(const [label,key]of [[tr('Parent page','父页面'),'⌘ ['],[translate(locale(),'topology.title'),'⌘ D'],[tr('Settings','设置'),'⌘ ,'],[tr('New line','换行'),'⇧ ↵']]){const row=n('div','preference-row');row.append(n('span','hint',label),n('kbd','',key));shortcuts.append(row);}root.append(shortcuts);
    if(!client.desktop&&client.access){const access=n('section','page-card browser-actions');access.append(action(tr('Add to Home Screen','添加到主屏幕'),async()=>{if(!await requestPWAInstall())access.append(n('p','hint',tr('Use your browser menu to install Pot or add it to the Home Screen.','通过浏览器菜单安装 Pot 或添加到主屏幕。')));}));access.append(action(tr('Change access token','更改访问令牌'),()=>renderEditor(tr('Browser access','浏览器访问'),{token:''},[{name:'token',label:tr('Access token','访问令牌'),kind:'password',required:true}],values=>configureBrowserAccess(values.token))),action(tr('Disconnect this browser','断开此浏览器'),()=>forgetBrowserAccess()));root.append(access);}

    await renderAbout({client,root,scope:{owner:'',space:scope().space},locale,updates,signal:token.signal});return;
    }
    const locations=client.spaces||[];if(!configurationSpace)configurationSpace=scope().space;
    const settingsScope=captureConfigurationScope(client,{space:configurationSpace,owner:''});
    if(client.desktop&&locations.length>1){const location=field(tr('Configuration location','配置位置'),configurationSpace,{choices:locations.map(space=>[space.id,space.name])});location.input.onchange=()=>{configurationSpace=location.input.value;show('settings');};root.append(location.row);}
    const current=()=>gate.current(token)&&!disposed,refresh=()=>pageView?.queue.run(),options={root,tr,locale,request,scope:settingsScope,current,refresh,allowanceMemory:providerAllowanceMemory,allowanceMemoryKey:client.runtime.boot_id};
    if(filter.settingsTab==='integrations'){
      const view=createIntegrations({root,request,scope:settingsScope,signal:token.signal,locale,openExternal:client.openExternal,resources:client.resources});
      pageView={root,token,domain:true,topics:[],queue:{run:view.refresh,close:()=>{}}};
      return view.refresh();
    }
    if(filter.settingsTab==='tasks'){
      const update=createTaskSettings({root,tr,locale,configure:(profile,policy)=>routingEditor(profile,policy,settingsScope),allowance:quota=>providerPolicyEditor(quota,settingsScope)});
      return connectDomain(root,token,listener=>domains.providers.watchSettings(settingsScope,listener),()=>domains.providers.refreshSettings(settingsScope),update);
    }
    const update=createProviderList({...options,providersDomain:domains.providers,pick:providers=>providerPicker(providers,settingsScope),edit:(profile,providers)=>providerEditor(profile,providers,settingsScope)});
    return connectDomain(root,token,listener=>domains.providers.watchCatalog(settingsScope,listener),()=>domains.providers.refresh(settingsScope),providers=>update({providers}));
  }
  function providerPolicyEditor(quota,owner) {
    renderEditor(tr('Provider allowance','提供商配额'),quota.policy,[
      {name:'period',label:tr('Period','周期'),choices:[['none',tr('Unlimited','无限制')],['day',tr('Daily','每日')],['week',tr('Weekly','每周')]]},
      {name:'limit',label:tr('Token limit','Token 上限'),kind:'number',parse:Number,required:true,min:1,max:Number.MAX_SAFE_INTEGER},
      {name:'timezone',label:tr('Time zone','时区'),required:true},{name:'auto_resume',label:tr('Automatically resume','自动恢复'),kind:'checkbox'},
    ],(values,context)=>domains.providers.saveAllowance(context,quota,values),{capturedScope:owner});
  }
  function routingEditor(profile,policy,owner) {
    renderEditor(profile.name,{profile_limit:policy.profile_limits?.[profile.id]??null,provider_limit:policy.provider_limits?.[profile.provider]??null},[
      {name:'profile_limit',label:tr('Model concurrency','模型并发数'),kind:'number',parse:value=>value?Number(value):null,min:1,max:1000,help:tr('Leave blank for no additional limit.','留空表示不另设上限。')},
      {name:'provider_limit',label:tr('Provider concurrency','提供商并发数'),kind:'number',parse:value=>value?Number(value):null,min:1,max:1000},
    ],(values,context)=>domains.providers.saveRouting(context,profile,policy,values),{capturedScope:owner});
  }
  async function setupAI(target=scope()) {
    const captured={...target,owner:''};
    const snapshot=await domains.providers.query(captured,{authoritative:true});
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
    const refreshTypes=async()=>{const version=++typeVersion;typeController?.abort();const controller=typeController=new AbortController();types=[];render();try{const value=await domains.providers.query(captured,{authoritative:true});if(!pickerCurrent()||version!==typeVersion)return;snapshot=value;types=(value.provider_types||[]).filter(value=>value.available!==false);render();}catch(reason){if(pickerCurrent()&&version===typeVersion&&reason.name!=='AbortError'){types=[];render();}}};
    form.lifecycle.providersInvalidated=refreshTypes;form.lifecycle.dispose=()=>{typeVersion++;typeController?.abort();};await refreshTypes();
  }
  async function providerEditor(profile,snapshot,settingsScope,providerOverride) {
    settingsScope=captureConfigurationScope(client,settingsScope);
    snapshot=structuredClone(snapshot);
    const t=key=>translate(locale(),'providers.'+key),onboarding=editor?.kind==='picker'&&editor.onboarding;
    if(editor?.kind==='picker')suspendEditor();else if(!await leave())return;
    const connection=(snapshot.connections||[]).find(c=>c.profile_id===profile?.id),typeID=providerOverride||connection?.type_id||'',type=(snapshot.provider_types||[]).find(value=>value.id===typeID)||{id:typeID,available:false};
    const providerID=type?.directory_provider||profile?.model_settings?.catalog_provider||'',selectedEntry=(snapshot.directory||[]).find(value=>value.id===providerID);
    let monitorView=null,monitorLoaded=!profile,monitorFailure=false;
    if(profile&&type?.capabilities?.allowances){const token=gate.next();body().append(n('p','hint',t('allowanceLoading')));try{monitorView=await domains.providers.allowance(settingsScope,profile.id);monitorLoaded=true;}catch{monitorFailure=true;}if(!gate.current(token)||disposed)return;}
    const inherited=modelDefaults(snapshot,providerID,profile?.model||'');let configuredModels=[...(snapshot.configured_models?.[profile?.id]||[])],discovered=[...(connection?.catalog?.models||[])];
    const inputDiscovery=createModelInputDiscovery(connection,profile?.model_settings?.input_modalities),baseName=type?.name||t('customName'),names=new Set((snapshot.profiles||[]).map(value=>value.name));let defaultName=baseName;for(let index=2;names.has(defaultName);index++)defaultName=`${baseName} ${index}`;
    const assistantChoices=[...(monitorView?.assistants||(client.assistants||[]).filter(value=>value.space_id===settingsScope.space))],monitor=monitorView?.monitor;if(monitor?.assistant&&!assistantChoices.some(value=>value.id===monitor.assistant))assistantChoices.push({id:monitor.assistant,name:t('status.assistant_missing'),missing:true});
    const initial={prompt_cache_minutes:profile?.model_settings?.prompt_cache_minutes??null,name:profile?.name||defaultName,endpoint:connection?.endpoint||type?.endpoint||'',api_key:'',model:profile?.model||'',model_selection:'',protocol:connection?.protocol||type?.protocol||'openai_responses',context_window:profile?.model_settings?.context_window??null,max_output_tokens:profile?.model_settings?.max_output_tokens??null,reasoning_effort:profile?.params?.reasoning_effort||'',...(type?.capabilities?.allowances&&monitorLoaded?{allowance_enabled:monitor?.enabled??true,allowance_threshold:monitor?.threshold??90,allowance_assistant:monitor?.assistant??assistantChoices[0]?.id??''}:{})};
    const protocolNames={openai_chat:'OpenAI Chat Completions',openai_responses:'OpenAI Responses',anthropic_messages:'Anthropic Messages',gemini:'Google Gemini'},protocols=[...new Set([...(type?.protocols?.length?type.protocols:[type?.protocol||initial.protocol]),initial.protocol])];
    const save=createProviderSave({profile,save:async(values,context,savedProfile)=>{if(!type||type.available===false)throw Object.assign(Error('provider unavailable'),{uiKey:'providers.unavailable'});return domains.providers.saveProfile(context,{values,typeID,profile:savedProfile,configuredModels,catalogProvider:providerID,defaultOutput:limitsFor(values.model).output,inputModalities:inputDiscovery.snapshot(values)},context.request_id);},configure:type?.capabilities?.allowances&&monitorLoaded?async(saved,values,context)=>{if(!type.capabilities?.allowances)return;if(!monitorView)monitorView=await domains.providers.allowance(settingsScope,saved.id);const revision=monitorView.monitor.revision;monitorView=await domains.providers.configureAllowance(context,saved.id,values,revision);}:null});
    const definitions=[
      {name:'name',label:t('name'),required:true},{name:'endpoint',label:t('endpoint'),kind:'url',required:true,help:t('endpointHelp')},
      {name:'api_key',label:t('key'),kind:'password',required:!profile&&!type?.default_credential,help:profile?t('keepKey'):type?.default_credential?t('publicDefault'):''},
      {name:'model_selection',label:t('model'),choices:[['',t('enterModel')]]},{name:'model',label:t('modelID'),required:true},
      {name:'protocol',label:t('protocol'),choices:protocols.map(value=>[value,protocolNames[value]||value])},
      {name:'context_window',label:t('context'),kind:'number',parse:value=>value?Number(value):null,min:1,max:Number.MAX_SAFE_INTEGER,placeholder:String(inherited.context),presets:[128000,200000,256000,400000,512000,1000000]},
      {name:'max_output_tokens',label:t('output'),kind:'number',parse:value=>value?Number(value):null,min:1,max:Number.MAX_SAFE_INTEGER,placeholder:String(inherited.output),presets:[8000,16000,32000,64000,128000]},
      {name:'reasoning_effort',label:t('reasoning'),placeholder:t('providerDefault')},
      {name:'prompt_cache_minutes',label:t('cacheMinutes'),kind:'number',parse:value=>value?Number(value):null,min:1,max:525600,placeholder:'15',help:t('cacheHelp')},
      ...(type?.capabilities?.allowances&&monitorLoaded?[{section:t('allowances')},{name:'allowance_enabled',label:t('allowanceEnabled'),kind:'checkbox',help:t('allowanceDefaults')},{name:'allowance_threshold',label:t('threshold'),kind:'number',parse:Number,min:1,max:100,required:true},{name:'allowance_assistant',label:t('assistant'),choices:[['',t('waitingAssistant')],...assistantChoices.map(value=>[value.id,value.name])]}]:[]),
    ];
    const form=renderEditor(profile?t('edit'):t('add'),initial,definitions,save,{capturedScope:settingsScope}),controls=form.controls,session=form.session;let endpointEdited=false;
    providerFormLocale(form,profile?'edit':'add',{name:'name',endpoint:'endpoint',api_key:'key',model_selection:'model',model:'modelID',protocol:'protocol',context_window:'context',max_output_tokens:'output',reasoning_effort:'reasoning',prompt_cache_minutes:'cacheMinutes',allowance_enabled:'allowanceEnabled',allowance_threshold:'threshold',allowance_assistant:'assistant'});
    for(const [name,key]of [['prompt_cache_minutes','cacheHelp'],['endpoint','endpointHelp'],['api_key',profile?'keepKey':'publicDefault'],['allowance_enabled','allowanceDefaults']]){const hint=controls.get(name)?.closest('.field-body').querySelector('small');if(hint)hint.dataset.providerI18n=key;}
    const section=form.form.querySelector('.page-section h2');if(section)section.dataset.providerI18n='allowances';
    if(monitorFailure){const warning=n('p','inline-error',t('allowanceLoadFailed'));warning.dataset.providerI18n='allowanceLoadFailed';form.form.querySelector('.editor-fields').append(warning);}
    const availabilityWarning=n('p','inline-error',t('unavailable'));availabilityWarning.dataset.providerI18n='unavailable';availabilityWarning.hidden=type.available!==false;form.form.prepend(availabilityWarning);if(type.available===false)form.form.querySelector('.primary').disabled=true;
    form.lifecycle.cancel=()=>back({skipPicker:true});form.lifecycle.afterSave=async saved=>{await back({skipPicker:true});await editor?.lifecycle.providersChanged?.(saved);if(onboarding){await leave();await localAssistantEditor();}};
    const set=(name,value)=>{controls.get(name).value=value??'';session.set(name,value);},showModel=()=>{controls.get('model').closest('.form-row').hidden=!!controls.get('model_selection').value;};
    function limitsFor(modelID){const entry=discovered.find(value=>value.id===modelID),base=modelDefaults(snapshot,providerID,modelID);return entry?{context:entry.context_window||base.context,input:entry.max_input||base.input,output:Math.min(entry.max_output||base.output,(entry.context_window||base.context)-1)}:base;}
    const inherit=()=>{const limits=limitsFor(session.values.model);controls.get('context_window').placeholder=String(limits.context);controls.get('max_output_tokens').placeholder=String(limits.output);};
    const models=(entries,selected='')=>{entries=providerConversationModels(entries,selected);const input=controls.get('model_selection');input.replaceChildren();for(const [value,label]of [['',t('enterModel')],...entries.map(model=>[typeof model==='string'?model:model.id,typeof model==='string'?model:model.name||model.id])]){const option=n('option','',label);option.value=value;input.append(option);}set('model_selection',entries.some(model=>(typeof model==='string'?model:model.id)===selected)?selected:'');session.acknowledge({model_selection:session.values.model_selection});showModel();};
    models([...new Map([...(selectedEntry?.models||[]),...configuredModels,...(profile?.model?[profile.model]:[]),...discovered].map(model=>[typeof model==='string'?model:model.id,model])).values()],initial.model);
    const keyURL={openai:'https://platform.openai.com/api-keys',anthropic:'https://platform.claude.com/settings/keys',google:'https://aistudio.google.com/api-keys',deepseek:'https://platform.deepseek.com/api_keys',xai:'https://console.x.ai/',opencode:'https://opencode.ai/auth','opencode-go':'https://opencode.ai/auth'}[providerID];
    if(keyURL){const link=n('a','provider-key-link',t('getKey'));link.dataset.providerI18n='getKey';link.href=keyURL;link.target='_blank';link.rel='noopener noreferrer';if(client.openExternal)link.onclick=event=>{event.preventDefault();client.openExternal(keyURL);};controls.get('api_key').closest('.field-body').append(link);}
    controls.get('model_selection').addEventListener('change',()=>{if(controls.get('model_selection').value){set('model',controls.get('model_selection').value);const model=selectedEntry?.models?.find(model=>model.id===session.values.model);if(model&&!endpointEdited){if(model.endpoint)set('endpoint',model.endpoint);if(model.protocol&&protocols.includes(model.protocol))set('protocol',model.protocol);}}showModel();inherit();});
    controls.get('endpoint').addEventListener('input',()=>{endpointEdited=true;});controls.get('protocol').addEventListener('change',()=>{endpointEdited=true;});controls.get('model').addEventListener('input',inherit);inherit();
    const refreshLocale=form.lifecycle.locale;form.lifecycle.locale=()=>{refreshLocale();controls.get('model_selection').options[0].textContent=t('enterModel');controls.get('reasoning_effort').placeholder=t('providerDefault');if(controls.has('allowance_assistant')){const choices=controls.get('allowance_assistant').options;choices[0].textContent=t('waitingAssistant');for(const item of assistantChoices.filter(value=>value.missing)){const option=[...choices].find(value=>value.value===item.id);if(option)option.textContent=t('status.assistant_missing');}}};
    let connectionRoute=JSON.stringify([initial.endpoint,initial.protocol]);
    connectProviderEditor({form,request,scope:settingsScope,profile,connection,type,locale,current:()=>editor?.session===session&&!disposed&&client.runtime.boot_id===session.context.boot,connectionChanged:value=>{inputDiscovery.connectionChanged(value);const next=JSON.stringify([value.endpoint,value.protocol]);if(connectionRoute!==next){connectionRoute=next;discovered=[];inherit();}},models:(entries,inputs)=>{inputDiscovery.adopt(session.values,inputs);configuredModels=entries.map(model=>typeof model==='string'?model:model.id);discovered=entries.filter(model=>typeof model!=='string');models([...new Map([...(selectedEntry?.models||[]),...entries].map(model=>[typeof model==='string'?model:model.id,model])).values()],controls.get('model_selection').value);inherit();}});
    let typeVersion=0,typeController;const priorDispose=form.lifecycle.dispose;
    form.lifecycle.providersInvalidated=async()=>{const version=++typeVersion;typeController?.abort();const controller=typeController=new AbortController();type.available=false;availabilityWarning.hidden=false;form.form.querySelector('.primary').disabled=true;form.lifecycle.connectionRefresh?.();discovered=[];inherit();
      try{const fresh=await domains.providers.query(settingsScope,{authoritative:true});if(editor?.session!==session||disposed||version!==typeVersion)return;const updated=(fresh.provider_types||[]).find(value=>value.id===typeID);if(updated)Object.assign(type,updated);type.available=updated?.available!==false&&!!updated;availabilityWarning.hidden=type.available;for(const name of ['allowance_enabled','allowance_threshold','allowance_assistant']){const input=controls.get(name);if(input)input.closest('.form-row').hidden=!type.capabilities?.allowances;}if(section)section.parentElement.hidden=!type.capabilities?.allowances;form.lifecycle.connectionRefresh?.();}
      catch(reason){if(editor?.session===session&&version===typeVersion&&reason.name!=='AbortError'){type.available=false;availabilityWarning.hidden=false;}}
    };
    form.lifecycle.dispose=()=>{typeVersion++;typeController?.abort();priorDispose?.();};
  }
  const taskListKey=()=>JSON.stringify(['tasks',[filter.taskTab,filter.taskScope,filter.taskQuery,filter.taskPhase,filter.listOwner,filter.scheduleQuery,filter.taskOffset,filter.scheduleOffset]]);
  function paginationPair(root,label,move) {
    const top=createRecordPagination({locale,label,move:direction=>move(direction,top)}),bottom=createRecordPagination({locale,label,move:direction=>move(direction,bottom)});
    top.root.dataset.viewKey='pagination-top';bottom.root.dataset.viewKey='pagination-bottom';
    return {top,bottom,update:value=>{top.update(value);bottom.update(value);}};
  }
  function focusPagination(pager,direction) {
    const selected=direction<0?pager.previous:pager.next,other=direction<0?pager.next:pager.previous;
    const target=selected.disabled&&other.disabled?taskListView?.rows:selected.disabled?other:selected;
    target?.focus({preventScroll:true});
    target?.scrollIntoView({block:'nearest'});
  }
  async function tasks(root,token) {
    const t=key=>translate(locale(),key);root.classList.add('tasks-content');
    root.append(segments([['tasks',t('tasks.tab')],['schedules',t('tasks.schedulesTab')]],filter.taskTab,value=>{filter.taskTab=value;if(value==='tasks'&&filter.listOwner==='unknown-owner')filter.listOwner='';show('tasks');}));
    if(filter.taskTab==='schedules')return schedules(root,token);
    const controls=toolbar();controls.classList.add('task-filters');
    const scopes=segments(['active','complete','archived'].map(value=>[value,t(`tasks.scope.${value}`)]),filter.taskScope,value=>{filter.taskScope=value;filter.taskPhase='';filter.taskOffset=0;restartTaskList();});controls.append(scopes);
    const phase=field(t('tasks.phaseFilter'),filter.taskPhase|| (filter.taskScope==='unarchived'?'unarchived':''),{choices:[['',t('tasks.allStages')],['unarchived',t('tasks.unarchived')],...['running','queued','waiting','waiting_approval','complete','failed','interrupted','cancelled'].map(value=>[value,taskPhaseLabel(value)])]});phase.row.classList.add('record-filter');
    phase.input.onchange=()=>{const value=phase.input.value;filter.taskPhase=value==='unarchived'?'':value;if(filter.taskScope!=='archived'||value==='unarchived')filter.taskScope='unarchived';filter.taskOffset=0;restartTaskList();};controls.append(phase.row);
    const search=field(t('tasks.search'),filter.taskQuery,{kind:'search'});search.row.classList.add('task-search');search.input.placeholder=t('tasks.search');
    const owner=field(t('tasks.assistantFilter'),filter.listOwner,{choices:ownerChoices(client,locale()).filter(([key])=>key!=='unknown-owner')});owner.row.classList.add('record-filter','record-owner-filter');owner.input.onchange=()=>{filter.listOwner=owner.input.value;filter.taskOffset=0;restartTaskList();};
    const searchLine=toolbar(search.row,owner.row);searchLine.classList.add('record-search-line');root.append(controls,searchLine);
    const help=n('p','record-view-help'),reminders=n('div','task-reminders');reminders.setAttribute('aria-label',t('tasks.reminders'));root.append(help,reminders);
    const rows=n('div','task-list record-group-list');rows.tabIndex=-1;rows.append(n('p','hint',t('loading.pending')));
    const pager=paginationPair(root,t('tasks.pagination'),(direction,origin)=>{filter.taskOffset=direction<0?Math.max(0,filter.taskOffset-20):taskListView.result.next_offset;taskListView.pageFocus={origin,direction};restartTaskList();});
    pager.update({loading:true});root.append(pager.top.root,rows,pager.bottom.root,n('p','record-page-note',t('tasks.pageNote')));
    const view={token,rows,scopes,phase:phase.input,help,reminders,pager,nodes:new Map(),groups:new Map(),seen:new Map(),result:null};taskListView=view;
    view.queue=createRefreshQueue(()=>readTaskList(view),{active:()=>!disposed&&gate.current(view.token)&&taskListView===view});
    view.stopSearch=bindSearch(search.input,value=>{if(value===filter.taskQuery)return;filter.taskQuery=value;filter.taskOffset=0;restartTaskList();});
    view.clock=bindTaskClock(rows,{locale,active:()=>!disposed&&gate.current(view.token)&&taskListView===view&&!!rows.offsetParent});
    await refreshTaskList(view);
  }
  function refreshTaskList(view=taskListView){return view?.queue.run();}
  function restartTaskList(){
    const view=taskListView;if(!view)return;
    view.stopQuery?.();view.stopQuery=null;view.queue.close();view.token=gate.next();view.queue=createRefreshQueue(()=>readTaskList(view),{active:()=>!disposed&&gate.current(view.token)&&taskListView===view});
    currentViewKey=taskListKey();
    return view.queue.run();
  }
  async function readTaskList(view) {
    const token=view.token,offset=filter.taskOffset,current=()=>gate.current(token)&&taskListView===view;
    const queryKey=JSON.stringify([filter.taskScope,filter.taskQuery,filter.taskPhase,filter.listOwner,offset]);
    if(view.queryKey!==queryKey){if(view.queryKey&&!view.pageFocus)host.scrollTop=0;view.queryKey=queryKey;view.result=null;view.nodes.clear();view.groups.clear();view.rows.replaceChildren(n('p','hint',translate(locale(),'loading.pending')));}
    for(const [index,value]of ['active','complete','archived'].entries()){const button=view.scopes.children[index];button.classList.toggle('selected',filter.taskScope===value);button.setAttribute('aria-pressed',String(filter.taskScope===value));}
    view.rows.setAttribute('aria-busy','true');view.pager.update({offset,loading:true});
    const query={scope:filter.taskScope,q:filter.taskQuery,phase:filter.taskPhase,owner:filter.listOwner,offset};
    if(view.stopQuery)return taskDomain.refreshList(query);
    return new Promise(resolve=>{view.stopQuery=taskDomain.watchList(query,snapshot=>{
      if(!current())return;view.rows.setAttribute('aria-busy',String(snapshot.status==='loading'));
      if(snapshot.data){const {pages,partial}=snapshot.data,total=pages.reduce((sum,page)=>sum+(page.value.total??page.value.items?.length??0),0);
        if(!partial&&filter.taskOffset>=total&&filter.taskOffset){filter.taskOffset=Math.max(0,Math.floor((total-1)/20)*20);currentViewKey=taskListKey();}
        updateTaskList(view,pages,snapshot.status==='loading',partial);view.rows.parentElement.querySelector('.space-availability')?.remove();
        if(partial){const notice=n('div','hint space-availability');notice.setAttribute('role','status');notice.append(n('span','',translate(locale(),'tasks.partialSources')),action(translate(locale(),'tasks.retry'),()=>refreshTaskList(view)));view.rows.before(notice);}
        if(view.pageFocus){focusPagination(view.pageFocus.origin,view.pageFocus.direction);view.pageFocus=null;}
      }
      if(snapshot.error){if(!view.result){view.rows.replaceChildren();errorView(snapshot.error,view.rows);}else notify(snapshot.error.message);}
      if(snapshot.status==='ready'||snapshot.error)resolve();
    });});
  }

  function updateTaskList(view,pages,loading,partial=false){
    const result=combineTasks(pages,{offset:filter.taskOffset,limit:20});rememberTasks(pages.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner)));
    if(!view.result)view.rows.replaceChildren();view.result=result;
    const text=(node,value)=>{if(node.textContent!==value)node.textContent=value;},t=key=>translate(locale(),key);
    for(const [index,value]of ['active','complete','archived'].entries()){const button=view.scopes.children[index];text(button,`${t(`tasks.scope.${value}`)}${partial?'':' '+result.counts[value].toLocaleString(locale())}`);button.classList.toggle('selected',filter.taskScope===value);button.setAttribute('aria-pressed',String(filter.taskScope===value));}
    text(view.help,t(filter.taskScope==='complete'?'tasks.completeHelp':filter.taskScope==='archived'?'tasks.archivedHelp':'tasks.activeHelp'));
    view.phase.value=filter.taskPhase||(filter.taskScope==='unarchived'?'unarchived':'');
    const reminderProjection=n('div','task-reminders');reminderProjection.setAttribute('aria-label',t('tasks.reminders'));reminderProjection.append(n('span','',t('tasks.reminders')));
    for(const stage of ['failed','interrupted','waiting_approval']){const button=action(t('tasks.reminder').replace('{stage}',taskPhaseLabel(stage)).replace('{count}',partial?'—':result.counts[stage].toLocaleString(locale())),()=>{filter.taskScope='unarchived';filter.taskPhase=stage;filter.taskOffset=0;restartTaskList();});button.dataset.viewKey=stage;reminderProjection.append(button);}reconcile(view.reminders,reminderProjection);
    const signature=JSON.stringify([filter.taskScope,filter.taskQuery,filter.taskPhase,filter.listOwner]);if(view.seenSignature!==signature){view.seen.clear();view.seenSignature=signature;}
    const groups=groupRecords(result.items,client),retainedGroups=new Set(),retainedTasks=new Set(),scroll=host.scrollTop,focused=view.rows.contains(document.activeElement)?document.activeElement:null;
    for(const [index,group]of groups.entries()){
      retainedGroups.add(group.key);let container=view.groups.get(group.key);
      if(!container){const row=n('section','record-group'),head=groupHeading(group,locale(),ownerDisclosures),records=n('div','record-group-records');row.dataset.viewKey=group.key;row.setAttribute('aria-labelledby',head.querySelector('h2').id);row.append(head,records);container={row,head,records};view.groups.set(group.key,container);}else reconcile(container.head,groupHeading(group,locale(),ownerDisclosures));
      if(view.rows.children[index]!==container.row)view.rows.insertBefore(container.row,view.rows.children[index]||null);
      const seenOffset=view.seen.get(group.key);let continuation=container.row.querySelector('.record-continuation');
      if(seenOffset!=null&&seenOffset<filter.taskOffset){if(!continuation){continuation=n('p','record-continuation',t('tasks.continued'));container.records.before(continuation);}}else continuation?.remove();
      if(!loading)view.seen.set(group.key,Math.min(seenOffset??filter.taskOffset,filter.taskOffset));
      for(const [position,task]of group.items.entries()){
        const taskKey=JSON.stringify([task._space_id,task._assistant_id,task.id]);retainedTasks.add(taskKey);let entry=view.nodes.get(taskKey);
        if(!entry){
          const row=n('button','task-row');row.type='button';row.dataset.viewKey=taskKey;
          const line=n('span','task-title-line'),title=n('span','task-title'),elapsed=n('span','task-list-elapsed'),chevron=n('span','hint task-chevron','›'),detail=n('span','task-meta'),phase=n('span'),mark=taskIdentityMark(task),short=taskIdentityShort(task),meta=n('span','hint'),archived=n('span','task-archived');
          short.classList.add('task-short-id');elapsed.setAttribute('role','timer');elapsed.setAttribute('aria-live','off');line.append(title);detail.append(phase,mark,short,archived,meta);row.append(line,elapsed,chevron,detail);entry={row,title,phase,mark,short,meta,elapsed,archived};view.nodes.set(taskKey,entry);
        }
        entry.row.setAttribute('aria-label',task.title);entry.row.disabled=task._stale;entry.row.title=task.title;entry.row.dataset.phase=task.phase;entry.row.onclick=()=>taskDetail(task,{returnTo:{route:'tasks'}});
        text(entry.title,task.title);entry.phase.className=`task-phase phase-${task.phase}`;text(entry.phase,taskPhaseLabel(task.phase)+(task.paused?' · '+t('tasks.schedulePaused'):''));
        reconcile(entry.mark,taskIdentityMark(task));text(entry.short,taskIdentities.short(task)||t('tasks.identityUnknown'));entry.short.title=taskAddress(task)?JSON.stringify(taskAddress(task)):t('tasks.identityUnknown');text(entry.archived,task.archived?t('tasks.scope.archived'):'');
        const time=['queued','running','waiting','waiting_approval'].includes(task.phase)?taskElapsed(task.started_at_ms,Date.now(),locale()):'';entry.elapsed.hidden=!time;text(entry.elapsed,time);entry.elapsed.id=`task-elapsed-${encodeURIComponent(taskKey)}`;
        if(time){entry.row.setAttribute('aria-describedby',entry.elapsed.id);entry.elapsed.dataset.startedAt=task.started_at_ms;}else{entry.row.removeAttribute('aria-describedby');delete entry.elapsed.dataset.startedAt;}entry.elapsed.title=t('tasks.elapsedHelp');
        text(entry.meta,[task.machine_id?machineInfo.label(task.machine_id,{space:task._space_id},locale()):'',task.pending?t('tasks.pendingInstructions').replace('{count}',task.pending):'',task._stale?t('loading.stale'):''].filter(Boolean).join(' · '));
        if(container.records.children[position]!==entry.row)container.records.insertBefore(entry.row,container.records.children[position]||null);
      }
    }
    for(const [key,entry]of view.nodes)if(!retainedTasks.has(key)){entry.row.remove();view.nodes.delete(key);}
    for(const [key,entry]of view.groups)if(!retainedGroups.has(key)){entry.row.remove();view.groups.delete(key);}
    let empty=view.rows.querySelector('.page-empty');if(!result.items.length){if(!empty){empty=n('div','page-empty');view.rows.append(empty);}empty.replaceChildren(n('h2','',t('tasks.emptyView')),n('p','hint',t('tasks.emptyHelp')));}else empty?.remove();
    if(focused?.isConnected&&document.activeElement!==focused)focused.focus({preventScroll:true});host.scrollTop=scroll;
    view.pager.update({offset:filter.taskOffset,count:result.items.length,total:result.total,nextOffset:result.next_offset,loading,partial});view.clock.refresh();
  }
  function closeTaskList(){taskListView?.stopQuery?.();taskListView?.stopSearch?.();taskListView?.queue.close();taskListView?.clock.dispose();taskListView=null;}
  function closeTaskView(){taskView?.stop?.();taskView?.ui?.dispose();taskView=null;host.classList.remove('task-page-host');}
  async function taskDetail(task,{recordHistory=true,returnTo={route:'chat',scope:{space:task._space_id,owner:task.assistant_id}}}={}) {
    rememberTasks([task]);taskOrigin=returnTo;captureView();viewReady=false;assistantCatalogLoad?.close();pageView?.queue.close();pageView=null;closeTaskList();closeTaskView();scheduleView=null;detailParent=true;
    if(recordHistory){const replace=history.state?.potTask?.id===task.id&&history.state?.potScope?.space===task._space_id;history[replace?'replaceState':'pushState']({potRoute:'tasks',potScope:{space:task._space_id,owner:task.assistant_id},potTask:{...task},potTaskReturn:returnTo},'',location.href);}
    const fileScope={owner:task.assistant_id,space:task._space_id};
    let saved;
    const result=await load(async token=>{saved=await taskDomain.readTranscript(task,fileScope);if(token.signal.aborted)throw token.signal.reason;const [record]=await Promise.all([taskDomain.metadata(task,fileScope,{signal:token.signal,authoritative:!saved}),machineInfo.load({...fileScope,signal:token.signal})]);return record;});if(!result)return;
    const {root,token}=result,record=result.value;if(saved?.run_id&&saved.run_id!==record.run_id)saved=null;host.classList.add('task-page-host');
    const draftKey=JSON.stringify([task._space_id,task.assistant_id,task.id]);
    const view={task,record,token,returnTo,initialRead:!saved,scope:fileScope,state:saved||createTaskTranscript(),inputs:[],fresh:!saved,busy:false,again:false,unavailable:false};
    const current=()=>gate.current(token)&&taskView===view;

    const ui=createTaskConversation({root,task,record,locale,client,scope:fileScope,draft:taskDrafts.get(draftKey)||'',onDraft:text=>{taskDraftVersions.set(draftKey,(taskDraftVersions.get(draftKey)||0)+1);if(text)taskDrafts.set(draftKey,text);else taskDrafts.delete(draftKey);},
      onOpenTask:child=>taskDetail({...child,_space_id:child._space_id||fileScope.space,assistant_id:child.assistant_id||fileScope.owner}),
      assistantName:(client.assistants||[]).find(item=>item.id===task.assistant_id&&item.space_id===task._space_id)?.name||task.assistant_id,
      downloadFile:({task:id,path})=>client.downloadFile(id,path,fileScope),toolMemory,approvalFor:tool=>approvalFor(tool,task.id),onDecision:decideApproval,
      onSend:async text=>{
        const version=taskDraftVersions.get(draftKey);
        await taskDomain.sendInput({...task,run_id:view.record.run_id},fileScope,text);
        if(taskDrafts.get(draftKey)===text&&taskDraftVersions.get(draftKey)===version)taskDrafts.delete(draftKey);

      },
      onStop:()=>taskDomain.interrupt({...task,run_id:view.record.run_id},fileScope),
      onCancel:()=>taskDomain.cancel({...task,run_id:view.record.run_id},fileScope),
      onPause:()=>taskDomain.pause({...task,run_id:view.record.run_id},fileScope,!view.record.info?.paused),
      onArchive:()=>taskDomain.archive({...task,run_id:view.record.run_id},fileScope,!view.record.info?.archived),
      onUsage:()=>openUsageView({...fileScope,name:task.title,scope:task.id,returnTask:{...task},taskReturn:returnTo}),
      onEarlier:async({before,signal})=>{const generation=view.windowGeneration=(view.windowGeneration||0)+1,run=view.record.run_id;view.historyBefore=Number(view.state.messages.filter(message=>!message.context_only).at(-1)?.sequence||0)+1;
        const earlier=await taskDomain.loadEarlier({...view.task,run_id:run},fileScope,before,{signal});if(!current()||generation!==view.windowGeneration||run!==view.record.run_id||signal.aborted)return;
        if(earlier?.messages.length){const anchor=view.ui.position()?.position?.anchor?.sequence;view.state=mergeTaskHistory(view.state,earlier,anchor||before);}else view.state={...view.state,has_more:false};
        view.historyBefore=Number(view.state.messages.filter(message=>!message.context_only).at(-1)?.sequence||0)+1;renderTaskTranscript(view);
      },onLatest:async()=>{const generation=view.windowGeneration=(view.windowGeneration||0)+1;view.historyBefore=null;const state=await taskDomain.latest({...view.task,run_id:view.record.run_id},fileScope);if(current()&&generation===view.windowGeneration){view.state=state||view.state;renderTaskTranscript(view);}},onRefresh:()=>refreshTaskEvents(view),onSettings:()=>{configurationSpace=task._space_id;filter.settingsTab='providers';routeChanged('settings');return show('settings');},
    });
    view.ui=ui;view.transcript=ui.transcript;taskView=view;heading(task.title);permissions();
    if(saved)renderTaskTranscript(view);
    restoreView(`task:${draftKey}`,token);
    view.stop=taskDomain.watchDetail(task,fileScope,snapshot=>{
      if(!current())return;const data=snapshot.data;if(!data)return;
      if(view.record.run_id!==data.record.run_id){view.windowGeneration=(view.windowGeneration||0)+1;view.historyBefore=null;}view.record=data.record;view.inputs=data.inputs;view.inputsTruncated=data.inputsTruncated;view.fresh=snapshot.fresh&&data.fresh;view.unavailable=data.unavailable;view.storageError=!!snapshot.error?.storage||data.storageError;view.connectionError=snapshot.error?.message||data.connectionError||'';
      if(!view.historyBefore)view.state=data.state;
      else{const updates=new Map(data.state.messages.map(message=>[message.id,message]));view.state={...view.state,cursor:data.state.cursor,inputs:data.state.inputs,messages:view.state.messages.map(message=>updates.get(message.id)||message)};}
      rememberTasks([{...view.record.info,_space_id:fileScope.space,assistant_id:fileScope.owner}]);renderTaskTranscript(view);
    });
  }
  function refreshTaskEvents(view=taskView){return view?taskDomain.refreshDetail(view.task,view.scope):Promise.resolve();}
  function renderTaskTranscript(view){view.ui.update({storageError:view.storageError,cacheTruncated:view.inputsTruncated,fresh:view.fresh,record:view.record,state:view.state,inputs:view.inputs,unavailable:view.unavailable,connectionError:view.connectionError||'',approvals:pendingApprovals.filter(item=>item.request?.task===view.task.id)});renderApprovals();}
  function approvalFor(tool,task=''){if(approvalOwner!==approvalContext())return;const scope=approvalScope();const approval=pendingApprovals.find(item=>item.request.owner===scope.owner&&(item.request.task||'')===task&&item.request.call_id===tool.id&&item.request.turn===tool.turn_id);return approval?{...approval,fresh:approvalFresh,task_title:taskNames.get(taskNameKey(scope,task))}:undefined;}
  function taskNeedsApproval(id,phase){return approvalOwner===approvalContext()?pendingApprovals.some(item=>item.request.owner===scope().owner&&item.request.task===id):phase==='waiting_approval';}
  const taskNameKey=(captured,id)=>JSON.stringify([captured.space,captured.owner,id]);
  function rememberTasks(items){taskIdentities.register(items);for(const task of items)taskNames.set(taskNameKey({space:task._space_id,owner:task.assistant_id||task._assistant_id},task.id),task.title);}
  function taskPhaseLabel(phase){return translate(locale(),phase==='waiting'?'tasks.stage.waiting':`schedule.state.${phase}`);}
  async function decideApproval(approval,action){
    const captured={...approvalScope()};
    if(disposed||!approvalFresh||approvalOwner!==approvalContext()||approval.request.owner!==captured.owner||(taskView&&approval.request.task!==taskView.task.id)||!pendingApprovals.some(item=>item.id===approval.id))throw Error(translate(locale(),'approvals.contextChanged'));
    await request('/api/v1/permissions',{...captured,method:'POST',body:{id:approval.id,action}});
    await permissions();
  }
  async function schedules(root,token) {
    const t=key=>translate(locale(),key);root.classList.add('schedules-content');root.append(toolbar(action(t('schedule.new'),()=>scheduleEditor())));
    const search=field(t('tasks.scheduleSearch'),filter.scheduleQuery,{kind:'search'});search.row.classList.add('task-search');search.input.placeholder=t('tasks.scheduleSearch');
    const owner=field(t('tasks.assistantFilter'),filter.listOwner,{choices:ownerChoices(client,locale())});owner.row.classList.add('record-filter','record-owner-filter');
    const line=toolbar(search.row,owner.row);line.classList.add('record-search-line');root.append(line,n('p','record-view-help',t('tasks.scheduleHelp')));
    const content=n('div','schedule-projection');root.append(content);let currentPages=null;
    const render=()=>{if(!currentPages)return;const scroll=host.scrollTop,projection=n('div','schedule-projection');renderSchedules(projection,currentPages,render);currentViewKey=taskListKey();reconcile(content,projection);host.scrollTop=scroll;};
    owner.input.onchange=()=>{filter.listOwner=owner.input.value;filter.scheduleOffset=0;render();host.scrollTop=0;};
    const stopSearch=bindSearch(search.input,value=>{filter.scheduleQuery=value;filter.scheduleOffset=0;render();host.scrollTop=0;});
    const loading=paginationPair(content,t('tasks.schedulePagination'),()=>{});loading.update({loading:true});content.append(loading.top.root,n('p','hint',t('loading.pending')),loading.bottom.root);
    const connected=await connectDomain(root,token,listener=>domains.schedules.watchList(listener),()=>domains.schedules.refreshList(),pages=>{currentPages=pages;render();});
    if(pageView&&gate.current(token)){const close=pageView.queue.close.bind(pageView.queue);pageView.queue.close=()=>{stopSearch();close();};}else stopSearch();return connected;
  }
  function renderSchedules(root,pages,render) {
    const t=key=>translate(locale(),key),partial=pages.partial||pages.some(page=>page.stale);
    // across() has exhausted each source cursor before this projection; filtering
    // applies to complete collections, never only the visible page.
    const query=filter.scheduleQuery.trim().toLocaleLowerCase(locale());
    const items=pages.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner)).filter(item=>(!filter.listOwner||recordOwner(item,client).key===filter.listOwner)&&(!query||item.spec.title.toLocaleLowerCase(locale()).includes(query))).sort((a,b)=>(a.next_at_ms||0)-(b.next_at_ms||0)||a._assistant_id.localeCompare(b._assistant_id)||a.id.localeCompare(b.id));
    filter.scheduleOffset=Math.min(filter.scheduleOffset,Math.max(0,Math.floor((items.length-1)/20)*20));
    const visible=items.slice(filter.scheduleOffset,filter.scheduleOffset+20),rows=n('div','schedule-list record-group-list');rows.tabIndex=-1;
    if(!visible.length)rows.append(n('div','page-empty',t('tasks.scheduleEmpty')));
    for(const group of groupRecords(visible,client)){
      const section=n('section','record-group');section.dataset.viewKey=group.key;const head=groupHeading(group,locale(),ownerDisclosures);section.setAttribute('aria-labelledby',head.querySelector('h2').id);section.append(head);const records=n('div','record-group-records');
      // Earlier sources are known in full here, so continuation has evidence.
      if(items.slice(0,filter.scheduleOffset).some(item=>recordOwner(item,client).key===group.key))section.append(n('p','record-continuation',t('tasks.continued')));
      for(const item of group.items){
        const row=action('',()=>scheduleHistory(item),{disabled:item._stale});row.classList.add('schedule-record');row.setAttribute('aria-label',item.spec.title);row.dataset.viewKey=JSON.stringify([item._space_id,item._assistant_id,item.id]);
        const title=n('span','schedule-record-title',item.spec.title),meta=n('span','schedule-record-meta'),state=n('span',item.spec.enabled?'schedule-enabled':'hint',t(item.spec.enabled?'tasks.scheduleEnabled':'tasks.schedulePaused'));
        const next=item.spec.enabled?t('tasks.scheduleNext').replace('{time}',item.next_label||t('tasks.scheduleNoNext')):t('tasks.schedulePausedNext');meta.append(state,n('span','',next));
        const rule=item.spec.kind==='cron'?item.spec.cron:item.spec.kind==='interval'?t('tasks.scheduleInterval').replace('{minutes}',item.spec.every_minutes.toLocaleString(locale())):t('tasks.scheduleOnce').replace('{time}',item.spec.at);
        row.append(title,n('span','schedule-record-chevron','›'),meta,n('span','schedule-record-rule',`${rule} · ${t('tasks.scheduleTimezone').replace('{zone}',item.spec.timezone||'')}`));records.append(row);
      }
      section.append(records);rows.append(section);
    }
    const pager=paginationPair(root,t('tasks.schedulePagination'),(direction,origin)=>{const position=origin===pager.top?'pagination-top':'pagination-bottom';filter.scheduleOffset=Math.max(0,filter.scheduleOffset+direction*20);render();const nav=host.querySelector(`[data-view-key="${position}"]`),target=nav?.querySelector(`[data-page-direction="${direction<0?'previous':'next'}"]`),other=nav?.querySelector(`[data-page-direction="${direction<0?'next':'previous'}"]`);const focus=target&&!target.disabled?target:other&&!other.disabled?other:host.querySelector('.schedule-list');focus?.focus({preventScroll:true});focus?.scrollIntoView({block:'nearest'});});
    pager.update({offset:filter.scheduleOffset,total:items.length,count:visible.length,nextOffset:filter.scheduleOffset+20<items.length?filter.scheduleOffset+20:null,partial:!!partial});root.append(pager.top.root,rows,pager.bottom.root,n('p','record-page-note',t('tasks.pageNote')));
  }
  async function scheduleHistory(item,record=true,runsOffset=0) {
    if(!recordOwner(item,client).address){
      captureView();pageView?.queue.close();pageView=null;closeTaskList();detailParent=true;
      const root=body();heading(item.spec.title);const info=card(translate(locale(),'tasks.ownerUnknown'));
      info.append(n('p','',item.spec.goal),n('p','hint',item.spec.timezone),n('p','hint',translate(locale(),'tasks.ownerMissing')));root.append(info);return;
    }
    if(scheduleView&&scheduleView.item.id===item.id&&scheduleView.item._space_id===item._space_id&&scheduleView.item._assistant_id===item._assistant_id&&scheduleView.runsOffset===runsOffset&&pageView)return pageView.queue.run();
    captureView();pageView?.queue.close();pageView=null;closeTaskList();taskView=null;detailParent=true;filter.taskTab='schedules';
    if(record)history.pushState({potRoute:'tasks',potSchedule:{...item},potScope:{space:item._space_id,owner:item._assistant_id}},'',location.href);
    const token=gate.next(),root=body(),content=n('div');root.append(content);
    const view={item,runsOffset,token,busy:false,confirmed:false,content};scheduleView=view;
    const selectedScope={space:item._space_id,owner:item._assistant_id},selectedQuery={selected:item.id,runsOffset,limit:20};
    await connectDomain(root,token,listener=>domains.schedules.watch(selectedScope,selectedQuery,listener),()=>domains.schedules.refresh(selectedScope,selectedQuery),(value,snapshot)=>{
      if(!value.selected||value.selected.deleted){scheduleView=null;return show('tasks',{force:true});}
      view.fresh=snapshot.fresh;view.busy=!!snapshot.pending;view.item={...item,...value.selected};const projection=n('div');renderScheduleHistory(projection,view,value,runsOffset);reconcile(content,projection);
    });
    if(gate.current(token))restoreView(JSON.stringify(['schedule-history',item._space_id,item._assistant_id,item.id,runsOffset]),token);
  }
  function renderScheduleHistory(root,view,value,runsOffset) {
    const selected=view.item;heading(selected.spec.title);const info=card(tr('Schedule details','定时计划详情'));
    info.append(n('code','',selected.spec.kind==='cron'?selected.spec.cron:translate(locale(),`schedule.${selected.spec.kind}`)),n('p','hint',selected.spec.goal),n('p','hint',`${selected.spec.timezone||''} · ${selected.next_label||tr('Not scheduled','未安排')}`));if(selected.last_error)info.append(n('p','inline-error',selected.last_error));
    const actions=toolbar();
    for(const name of ['run',selected.spec.enabled?'pause':'resume','edit','delete']) {
      const button=action(tr({run:'Run now',pause:'Pause',resume:'Resume',edit:'Edit',delete:'Remove'}[name],{run:'立即运行',pause:'暂停',resume:'恢复',edit:'编辑',delete:'移除'}[name]),async event=>{
        const button=event.currentTarget,actions=button.parentElement;
        if(!view.fresh||view.busy||scheduleView!==view||!gate.current(view.token))return;
        if(name==='edit'){scheduleEditor(selected);return;}
        if(name==='delete'&&!view.confirmed){view.confirmed=true;button.textContent=tr('Confirm removal','确认移除');return;}
        view.busy=true;for(const control of actions.querySelectorAll('button'))control.disabled=true;
        const captured={space:selected._space_id,owner:selected._assistant_id};
        try{await (name==='run'?domains.schedules.run(captured,selected.id):name==='delete'?domains.schedules.remove(captured,selected.id):domains.schedules.pause(captured,selected.id,name==='pause'));if(scheduleView===view&&gate.current(view.token)&&name==='delete'){scheduleView=null;await show('tasks',{force:true});}}
        catch(reason){if(scheduleView===view&&gate.current(view.token))errorView(reason,view.content);}
        finally{view.busy=false;for(const control of actions.querySelectorAll('button'))control.disabled=!view.fresh;}
      },{primary:name==='run',danger:name==='delete',disabled:!view.fresh||selected._stale||recordOwner(selected,client).profile?.offline||!recordOwner(selected,client).address||view.busy});actions.append(button);
    }
    if(view.confirmed){const remove=actions.lastChild;remove.textContent=tr('Confirm removal','确认移除');}info.append(actions,n('p','hint',translate(locale(),'tasks.scheduleStopHelp')));root.append(info,n('p','hint',tr('Run history','运行历史')));
    const rows=(value.runs||[]).map(run=>{const summary=n('span','',run.summary),hook=hookHistory(run,locale());if(hook)summary.append(hook);if(run.task_id)summary.append(action(translate(locale(),'schedule.viewTask'),()=>taskDetail({id:run.task_id,title:run.summary||selected.spec.title,assistant_id:selected._assistant_id,_assistant_id:selected._assistant_id,_space_id:selected._space_id})));return [run.scheduled_label,translate(locale(),`schedule.state.${run.state}`),summary];});
    root.append(table(['scheduled','state','summary'].map(key=>translate(locale(),`schedule.${key}`)),rows,(value.runs||[]).map(run=>run.id||JSON.stringify([run.scheduled_label,run.task_id]))));
    root.append(toolbar(action(tr('Newer entries','较新记录'),()=>scheduleHistory(selected,false,Math.max(0,runsOffset-20)),{disabled:!runsOffset}),action(tr('Older entries','较早记录'),()=>scheduleHistory(selected,false,value.runs_next_offset),{disabled:value.runs_next_offset==null})));
  }
  function scheduleEditor(item) {
    const t=key=>translate(locale(),`schedule.${key}`);
    const spec=item?.spec||{title:'',goal:'',machine_id:'',timezone:browserTimezone(),kind:'cron',cron:'0 9 * * *',at:'',every_minutes:60,misfire:'coalesce',notify:'all',enabled:true,profile:null,model:null,requirements:{tools:false,desktop:false,exact_budget:false,live_steering:false}};
    const definitions=[
      ...(!item?[{name:'owner_ref',key:'assistant',choices:(client.assistants||[]).filter(owner=>supportsCapability(owner,'schedules.manage')).map(owner=>[JSON.stringify([owner.space_id,owner.id]),`${owner.name} · ${owner.space_name||''}`])}]:[]),
      {name:'title',key:'title',required:true},{name:'goal',key:'goal',kind:'textarea',required:true},{name:'machine_id',key:'machine',choices:[['',t('defaultMachine')],...(spec.machine_id?[[spec.machine_id,machineInfo.label(spec.machine_id,{space:item?._space_id||scope().space},locale())]]:[])]},
      {name:'timezone',key:'timezone',helpKey:'timezoneHelp',required:true},
      {name:'kind',key:'repeat',choiceKeys:{once:'once',interval:'interval',cron:'cron'},choices:['once','interval','cron'].map(key=>[key,t(key)])},
      {name:'at',key:'at',help:'YYYY-MM-DD HH:MM'},
      {name:'every_minutes',key:'minutes',kind:'number',min:1,max:525600,parse:Number},
      {name:'cron',key:'cron',helpKey:'cronHelp',placeholder:'0 8-20 * * *'},
      {name:'misfire',key:'misfire',choiceKeys:{coalesce:'coalesce',skip:'skip'},choices:['coalesce','skip'].map(key=>[key,t(key)])},
      {name:'notify',key:'notify',choiceKeys:{all:'all',failures:'failures'},choices:['all','failures'].map(key=>[key,t(key)])},
      {name:'before_run_command',key:'hookCommand',helpKey:'hookHelp',kind:'textarea',placeholder:'["python3", "/path/to/check_updates.py"]'},
      {name:'before_run_cwd',key:'hookCwd',helpKey:'hookCwdHelp'},
      {name:'before_run_timeout',key:'hookTimeout',kind:'number',min:1,max:300,parse:Number},
      {name:'enabled',key:'enabled',kind:'checkbox'},
    ].map(definition=>({...definition,label:t(definition.key),...(definition.helpKey?{help:t(definition.helpKey)}:{})}));
    let preview;
    const view=renderEditor(t(item?'edit':'new'),{...spec,before_run_command:spec.before_run?JSON.stringify(spec.before_run.command):'',before_run_cwd:spec.before_run?.cwd||'',before_run_timeout:spec.before_run?.timeout_seconds||30,owner_ref:JSON.stringify([item?item._space_id:scope().space,item?item._assistant_id:scope().owner])},definitions,async(values,context)=>{
      await preview.validate(values);
      const {owner_ref,before_run_command,before_run_cwd,before_run_timeout,...specValues}=values;const [space,owner]=JSON.parse(owner_ref);
      const canonical={...specValues,before_run:beforeRunDraft(values,locale()),cron:values.kind==='cron'?values.cron:'',at:values.kind==='once'?values.at:'',every_minutes:values.kind==='interval'?values.every_minutes:0};
      return item?domains.schedules.update({space,owner},item.id,canonical,context.request_id):domains.schedules.create({space,owner},canonical,context.request_id);
    },{capturedScope:item?{space:item._space_id,owner:item._assistant_id}:scope(),submitLabel:t('save')});
    preview=connectScheduleEditor({view,preview:(scope,rule,signal)=>domains.schedules.preview(scope,rule,signal),locale,definitions,machineInfo,current:()=>editor?.session===view.session&&!disposed});
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
    const t=key=>translate(locale(),'usage.'+key),onRange=range=>{filter.usageRange=range;show('usage',{keepUsageOwner:true});};
    const titleRow=toolbar(n('h2','page-heading',usageOwner?t('scopedTitle'):tr('Overall statistics','总体统计')),headingActions);titleRow.classList.add('usage-heading');root.append(titleRow);
    if(usageOwner)root.append(n('p','hint',usageOwner.name));
    const timezone=Intl.DateTimeFormat().resolvedOptions().timeZone,scopeID=usageOwner?.scope||usageOwner?.owner||'',range={...filter.usageRange},year=filter.usageYear,params={start:range.start,end:range.end,timezone,scope:scopeID},calendarParams={start:`${year}-01-01`,end:`${year}-12-31`,timezone,scope:scopeID},spaceID=usageOwner?.space||filter.usageSpace;
    root.append(usageRangeControls(range,{locale:locale(),onRange}));
    const selectedNotices=n('div'),calendarNotices=n('div');root.append(selectedNotices);
    root.dataset.usageScope=JSON.stringify([spaceID,scopeID,usageOwner?.owner||'']);
    const listsRoot=n('div','usage-lists'),jobs=[];
    if(usageOwner)listsRoot.append(n('h2','usage-lifetime-heading',t('lifetimeDetails')));
    if(usageOwner)for(const space of (client.spaces||[]).filter(space=>spaceID==='all'||space.id===spaceID)){
      const owner={space:space.id,owner:usageOwner?.owner||''},base={timezone,scope:scopeID};
      for(const [kind,title]of [['models',tr('Lifetime model usage','累计模型用量')],['requests',tr('Model requests','模型请求')],['tools',tr('Tool statistics','工具统计')],['compactions',tr('Context compaction','上下文压缩')]])jobs.push(createUsageList({root:listsRoot,kind,title,params:base,owner,request:read,token,current:()=>gate.current(token),offsets:usageOffsets,query:client.resources?.query,locale:locale(),tr}));
    }
    const content=n('div'),limits=n('div','usage-limit-actions');headingActions.append(limits);root.append(content,calendarNotices,listsRoot);
    let initial=true;
    return connectView(root,token,async()=>{
      const options={spaceID,ownerID:usageOwner?.owner||''};
      const summaries=Promise.allSettled([across(`/api/v1/usage/summary?${new URLSearchParams(params)}`,token,{...options,errorTarget:selectedNotices}),across(`/api/v1/usage/summary?${new URLSearchParams(calendarParams)}`,token,{...options,errorTarget:calendarNotices})]);
      for(const job of jobs)if(!initial)job.refresh();initial=false;
      const [selected,calendar]=await summaries;if(selected.status==='rejected')throw selected.reason;
      return {pages:selected.value,calendarPages:calendar.status==='fulfilled'?calendar.value:null};
    },({pages,calendarPages})=>{const projection=n('div'),limitProjection=n('div','usage-limit-actions');
    const data=pages.length===1?{...pages[0].value,spaces:pages}:combineUsage(pages);
    const {exact,compact}=usageNumbers(locale()),total=data.total||{},metrics=n('div','usage-metrics'),totalCard=n('section','page-card usage-total');
    for(const [label,value]of [[tr('Total tokens','总 token'),total.input+total.output],[tr('Input (includes cache)','输入（含缓存）'),total.input],[tr('Output','输出'),total.output]]){const item=n('div','metric');item.append(n('span','hint',label),n('strong','',compact(value)));usageTooltip(item,{title:label,rows:usageRows(total,locale()),note:tr('Cached tokens are included in input. Token counts are not billed cost.','缓存已计入输入，token 数量不代表实际费用。')});metrics.append(item);}
    totalCard.append(metrics,n('p','usage-cache-breakdown',`${tr('Cached input','缓存输入')} ${exact(total.cache)} · ${tr('Cache hit rate','缓存命中率')} ${usageNumbers(locale()).hitRate(total)}`),n('p','hint',`${tr('Model requests','模型请求')} ${exact(total.requests||0)}`));projection.append(totalCard);
    const routing=data.routing_total||{};
    if(routing.requests){const routingCard=card(tr('Task routing turns','任务路由回合'));routingCard.append(usageTooltip(n('p','usage-number',`${compact(routing.input+routing.output)} tokens`),{title:tr('Task routing turns','任务路由回合'),rows:usageRows(routing,locale()),note:tr('Includes the entire main-chat turn that delegated a task. Already included in totals.','包含委派任务的整个主聊天回合，已计入总量。')}));projection.append(routingCard);}
    projection.append(usageTrend(data,{locale:locale(),timezone,preset:range.preset}),usageDistributions(data,{locale:locale()}));
    if(calendarPages){const calendarData=calendarPages.length===1?calendarPages[0].value:combineUsage(calendarPages);projection.append(usageCalendar(calendarData,{locale:locale(),timezone,year,onYear:value=>{filter.usageYear=value;show('usage',{keepUsageOwner:true});},onDate:day=>onRange({preset:'day',start:day,end:day})}));}
    else {const failure=n('section','page-card usage-calendar-error');failure.append(n('p','inline-error',t('calendarUnavailable')),action(translate(locale(),'retry'),()=>pageView?.queue.run()));projection.append(failure);}
    for(const page of pages) {
      const value=page.value,owner={space:page.space.id,owner:usageOwner?.owner||''};
      const policy=value.quota.policy;
      limitProjection.append(action(`${tr('Usage limits','用量限制')}${pages.length>1?' · '+page.space.name:''}`,()=>policyEditor(policy,owner),{disabled:page.stale}));
      if(value.quota?.active){const allowance=card(`${tr('Token allowance','Token 配额')} · ${page.space.name}`);allowance.append(n('p','hint',value.quota?.active?`${Number(value.quota.remaining).toLocaleString(locale())} ${tr('remaining','剩余')} · ${value.quota.reset_label||''}`:tr('No active token limit','未启用 token 限额')),action(tr('Edit allowance','编辑配额'),()=>policyEditor(policy,owner),{disabled:page.stale}));projection.append(allowance);}
      if(value.waiting){const waiting=section(tr('Waiting for allowance','等待配额'));waiting.append(n('p','hint',value.waiting.provider||''));for(const name of ['resume','cancel']){const requestID=uid(),task=usageOwner?.returnTask;const button=action(tr(name==='resume'?'Resume':'Cancel waiting',name==='resume'?'恢复':'取消等待'),event=>operation(event.currentTarget,async()=>{if(task)return name==='resume'?taskDomain.resume(task,owner,{turn:value.waiting.turn,since:value.waiting.since}):taskDomain.cancel(task,owner);if(name==='resume')return request('/api/v1/usage/resume',{...owner,method:'POST',body:{request_id:requestID,turn:value.waiting.turn,since:value.waiting.since}});const snapshot=await request('/api/v1/snapshot',owner),message=snapshot.messages?.find(message=>message.id===snapshot.turn?.user_message_id);if(!message||message.request_id!==value.waiting.turn)throw Error(tr('Allowance wait changed. Refresh Usage.','配额等待已变化，请刷新用量页面。'));return request('/api/v1/turn/cancel',{...owner,method:'POST',body:{request_id:requestID,user_message_id:message.id}});},()=>show('usage',{keepUsageOwner:true})),{disabled:page.stale||!usageOwner});waiting.append(button);}projection.append(waiting);}
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
  function managementError(error) {
    return ({assistant_busy:'assistantBusy',assistant_changed:'assistantChanged',assistant_model_unavailable:'modelUnavailable',assistant_workspace_unavailable:'workspaceUnavailable',agent_link_capability_unsupported:'managementUnsupported',agent_link_authentication_failed:'managementUnauthorized',agent_link_identity_conflict:'managementConflict',agent_link_capacity_exceeded:'managementCapacity',agent_link_unreachable:'managementOffline',agent_link_disconnected:'managementOffline',agent_link_reconnect_required:'managementOffline',agent_link_revoked:'managementUnauthorized',agent_link_invalid:'managementInvalid'})[error?.code]||'managementFailed';
  }
  function runtimeSummary(form,name,kind,change) {
    form.form.classList.add('assistant-management-form');const row=n('div','assistant-runtime-summary'),label=n('span','',name);
    const button=action(translate(locale(),'assistantStart.change'),async()=>{if(await leave())await change();});row.append(connectionBrand(kind),label,button);form.form.prepend(row);return ()=>{button.textContent=translate(locale(),'assistantStart.change');};
  }
  function beginAssistantCatalog(retry,context,runtimeID='local',valid=()=>true){
    if(assistantCatalogLoad)return null;
    const root=host.firstElementChild,initiatingEditor=editor,boot=client.runtime.boot_id,generation=client.connectionGeneration;
    const control=client.spaces.find(item=>item.id===context.space)?.control_id,initiatingScope=JSON.stringify(scope()),abort=new AbortController();
    for(const node of root?.querySelectorAll('.assistant-catalog-state')||[])node.remove();
    const loading=n('section','assistant-catalog-state'),label=n('p','hint');loading.setAttribute('aria-live','polite');loading.append(label);root?.append(loading);
    const leased=new Set();
    const current=()=>!disposed&&host.firstElementChild===root&&editor===initiatingEditor&&JSON.stringify(scope())===initiatingScope&&client.runtime.boot_id===boot&&client.connectionGeneration===generation&&client.operationScopeReady?.(context)!==false&&client.spaces.find(item=>item.id===context.space)?.connected!==false&&client.spaces.find(item=>item.id===context.space)?.control_id===control&&valid();
    const operation={runtimeID,signal:abort.signal,current:()=>assistantCatalogLoad===operation&&current(),
      lock(){if(assistantCatalogLoad!==operation)return;for(const button of root?.querySelectorAll('.assistant-runtime-choice,.assistant-connect-new')||[]){leased.add(button);setPending(button,true);if(button.closest('[data-runtime-id]')?.dataset.runtimeId===runtimeID)button.setAttribute('aria-busy','true');}},
      locale(){label.textContent=translate(locale(),'loading.pending');},
      // Release this operation's pending state against the latest rendered
      // authority. An old result cannot overwrite a recovered generation, and
      // an old projection cannot acquire authority by losing its pending state.
      close(){abort.abort();loading.remove();if(assistantCatalogLoad!==operation)return;assistantCatalogLoad=null;for(const button of leased){if(button.matches('.assistant-runtime-choice,.assistant-connect-new'))setDisabled(button,catalogAuthority.get(button)?.()!==true);setPending(button,false);button.removeAttribute('aria-busy');}},
      failed(reason){if(!operation.current()){operation.close();return;}operation.close();const message=n('p','inline-error'),retryButton=action('',()=>{if(current()){loading.remove();retry();}});message.dataset.assistantCatalogI18n='agentLinks.'+managementError(reason);retryButton.dataset.assistantCatalogI18n='retry';loading.replaceChildren(message,retryButton);if(reason?.message){const diagnostics=n('details','detail-disclosure'),summary=n('summary');summary.dataset.assistantCatalogI18n='agentLinks.diagnostics';diagnostics.append(summary,n('pre','',reason.message));loading.append(diagnostics);}for(const node of loading.querySelectorAll('[data-assistant-catalog-i18n]'))node.textContent=translate(locale(),node.dataset.assistantCatalogI18n);root.append(loading);},
    };
    assistantCatalogLoad=operation;operation.lock();operation.locale();return operation;
  }
  function createdChatLifetime(form,context){
    const boot=client.runtime.boot_id,generation=client.connectionGeneration,control=client.spaces.find(item=>item.id===context.space)?.control_id,initiatingScope=JSON.stringify(scope());
    form.lifecycle.opensCreatedChat=true;
    form.lifecycle.creationCurrent=()=>!disposed&&editor?.session===form.session&&JSON.stringify(scope())===initiatingScope&&client.runtime.boot_id===boot&&client.connectionGeneration===generation&&client.spaces.find(item=>item.id===context.space)?.control_id===control&&client.operationScopeReady?.(context)!==false;
  }
  async function localAssistantEditor() {
    const captured=captureConfigurationScope(client,{space:client.spaces.find(item=>item.kind==='local')?.id||scope().space,owner:''}),t=key=>translate(locale(),'agentLinks.'+key);
    const loading=beginAssistantCatalog(localAssistantEditor,captured);if(!loading)return;
    let providers;try{providers=await domains.providers.query(captured,{authoritative:true});}catch(reason){loading.failed(reason);return;}
    if(!loading.current()){loading.close();return;}loading.close();const profiles=providers.profiles||[];
    if(!profiles.length){await providerPicker(providers,captured);if(editor?.kind==='picker')editor.onboarding=true;return;}
    const profile=profiles.find(item=>item.id===providers.default_provider_id)||profiles[0];
    let committed,attemptID,attemptBody,uncertain=false;
    const form=renderEditor(t('assistantCreate'),{name:'',profile_id:profile.id,model:profile.model||'',text:''},[
      {name:'name',label:t('assistantName')},{name:'profile_id',label:translate(locale(),'assistantStart.provider'),required:true,choices:profiles.map(item=>[item.id,item.name])},
      {name:'model',label:t('assistantModel'),required:true},{name:'text',label:translate(locale(),'assistantStart.instructions'),kind:'textarea'},
    ],async(values,context)=>{
      if(attemptID!==context.request_id){const selected=profiles.find(item=>item.id===values.profile_id);attemptID=context.request_id;attemptBody={locale:locale(),name:values.name,text:values.text,profile:selected,model:values.model};}
      try{committed||=await domains.directory.create(captured,attemptBody,attemptID);uncertain=false;return {created_assistant:{...committed,space_id:captured.space}};}
      catch(reason){uncertain=!reason.code||['request_failed','unavailable'].includes(reason.code);throw Object.assign(Error(t(uncertain?'managementUncertain':'managementFailed')),{uiKey:'agentLinks.'+(uncertain?'managementUncertain':'managementFailed'),diagnostic:reason.message});}
    },{capturedScope:captured,submitLabel:t('assistantCreate')});
    const models=n('datalist');models.id='assistant-models-'+uid();form.form.append(models);form.controls.get('model').setAttribute('list',models.id);
    const refreshModels=()=>{const selected=profiles.find(item=>item.id===form.controls.get('profile_id').value),catalog=(providers.connections||[]).find(item=>item.profile_id===selected?.id)?.catalog?.models||(providers.directory||[]).find(item=>item.id===selected?.model_settings?.catalog_provider)?.models||[],items=new Map();if(selected?.model)items.set(selected.model,selected.model);for(const model of catalog)items.set(model.id,model.name||model.id);models.replaceChildren();for(const [value,label]of items){const option=n('option','',label);option.value=value;models.append(option);}};
    form.controls.get('profile_id').addEventListener('change',()=>{const value=profiles.find(item=>item.id===form.controls.get('profile_id').value)?.model||'';form.controls.get('model').value=value;form.session.set('model',value);refreshModels();});refreshModels();
    const refreshSummary=runtimeSummary(form,translate(locale(),'assistantStart.localTitle'),'pot',()=>show('add-assistant'));
    form.lifecycle.settled=()=>{if(uncertain){for(const control of form.controls.values())control.disabled=true;form.root.querySelector('.assistant-runtime-summary button').disabled=true;form.root.querySelector('.editor-actions .primary').textContent=translate(locale(),'retry');}};
    form.lifecycle.locale=()=>{refreshSummary();form.lifecycle.setTitle?.(t('assistantCreate'));for(const [name,key]of [['name','agentLinks.assistantName'],['profile_id','assistantStart.provider'],['model','agentLinks.assistantModel'],['text','assistantStart.instructions']]){const input=form.controls.get(name);input.setAttribute('aria-label',translate(locale(),key));input.closest('.form-row').querySelector('.form-label').textContent=translate(locale(),key);}form.root.querySelector('.assistant-runtime-summary>span:not(.connection-brand)').textContent=translate(locale(),'assistantStart.localTitle');form.root.querySelector('.editor-actions .primary').textContent=form.session.pending?translate(locale(),'assistant.creating'):uncertain?translate(locale(),'retry'):t('assistantCreate');form.root.querySelector('.editor-actions button:not(.primary)').textContent=translate(locale(),'integrations.cancel');};
    createdChatLifetime(form,captured);
    form.lifecycle.afterSave=async saved=>{await leave();await openChat({space:captured.space,owner:saved.created_assistant.id,assistant:saved.created_assistant,created:true});};
  }
  async function linkedAssistantEditor(connectionID,space,assistantID='',runtimeName='',kind='pot',valid=()=>true) {
    const captured=captureConfigurationScope(client,{space,owner:''}),t=key=>translate(locale(),'agentLinks.'+key);
    const initiatingRoot=host.firstElementChild,operation=beginAssistantCatalog(()=>linkedAssistantEditor(connectionID,space,assistantID,runtimeName,kind,valid),captured,'connection:'+connectionID,valid);if(!operation)return;
    const loading=n('section','assistant-catalog-state');
    let catalog;
    try {catalog=await request('/api/v1/agent-links/assistants',{...captured,signal:operation.signal,method:'POST',body:{connection_id:connectionID,action:'catalog'}});}
    catch(reason){operation.failed(reason);return;}
    if(!operation.current()){operation.close();return;}
    operation.close();
    if(catalog.schema_version!==1||!catalog.actions?.includes(assistantID?'update':'create')||catalog.fields?.some(item=>item.required&&!['name','model','workspace','effort'].includes(item.name))){loading.replaceChildren(n('p','inline-error',t('managementUnsupported')),action(translate(locale(),'assistantStart.manage'),()=>show('machines')));initiatingRoot.append(loading);return;}
    const existing=catalog.assistants?.find(item=>item.address?.agent_id===assistantID);if(assistantID&&!existing){loading.replaceChildren(n('p','inline-error',t('assistantChanged')));initiatingRoot.append(loading);return;}
    const id=existing?.address.agent_id||uid(),supported=new Map((catalog.fields||[]).map(item=>[item.name,item]));
    const initial={},definitions=[];
    const workspaceLabel=item=>item.name+' · '+t(item.mode==='workspace-write'?'workspaceWrite':'workspaceRead')+(item.description?' · '+item.description:'');
    const keys={name:'assistantName',model:'assistantModel',workspace:'assistantWorkspace',effort:'assistantEffort'};
    for(const name of ['name','model','workspace','effort']) {
      const metadata=supported.get(name);if(!metadata||(existing&&!metadata.editable))continue;
      const choices=name==='model'?(catalog.models||[]).map(item=>[item.id,item.name]):name==='workspace'?(catalog.workspaces||[]).map(item=>[item.id,workspaceLabel(item)]):name==='effort'?[['',t('effortDefault')],...(catalog.models?.find(item=>item.id===(existing?.model||catalog.models?.[0]?.id))?.efforts||[]).map(item=>[item,t('effort.'+item)])]:null;
      initial[name]=existing?.[name]??metadata.default??(name==='name'?'':choices?.[0]?.[0]||'');if(name==='model'&&existing&&!choices.some(([value])=>value===existing.model)){choices.unshift(['',t('modelSelect')]);initial[name]='';}
      definitions.push({name,key:keys[name],label:t(keys[name]),required:metadata.required&&(!existing||name==='name'),...(choices?{choices}:{})});
    }
    let committed,uncertain=false,committedRequest;
    const form=renderEditor(t(existing?'assistantUpdate':'assistantCreate'),initial,definitions,async(values,context)=>{
      try {
        if(committedRequest!==context.request_id){committed=undefined;committedRequest=context.request_id;}
        const fields=Object.fromEntries(Object.entries(values).filter(([name,value])=>(!existing&&supported.get(name)?.required)||value!==initial[name]));
        if(existing&&!Object.keys(fields).length)committed={assistant:existing};
        committed||=await request('/api/v1/agent-links/assistants',{...captured,method:'POST',body:{connection_id:connectionID,action:existing?'update':'create',request_id:context.request_id,id,...fields,revision:existing?.revision||0}});
        uncertain=false;
        const address=committed.assistant?.address;if(!address||address.kind!=='assistant'||!address.node_id||!address.agent_id)throw Error('Invalid assistant address');
        if(existing){Object.assign(existing,committed.assistant);Object.assign(initial,values);}
        return {managed_assistant:committed.assistant};
      }catch(reason){uncertain=!['assistant_busy','assistant_changed','assistant_model_unavailable','assistant_workspace_unavailable','agent_link_invalid','agent_link_capacity_exceeded','agent_link_capability_unsupported','agent_link_identity_conflict','agent_link_authentication_failed'].includes(reason.code);throw Object.assign(Error(t(uncertain?'managementUncertain':managementError(reason))),{uiKey:'agentLinks.'+(uncertain?'managementUncertain':managementError(reason)),diagnostic:reason.message});}
    },{capturedScope:captured,submitLabel:t(existing?'assistantUpdate':'assistantCreate'),editableWhilePending:!!existing});
    const executionHint=existing?n('p','hint',translate(locale(),'settings.nextExecution')):null;if(executionHint)form.form.prepend(executionHint);
    const effort=form.controls.get('effort');
    if(effort)form.controls.get('model')?.addEventListener('change',()=>{const model=catalog.models.find(item=>item.id===form.controls.get('model').value);effort.replaceChildren();for(const [value,label]of [['',t('effortDefault')],...(model?.efforts||[]).map(item=>[item,t('effort.'+item)])]){const option=n('option','',label);option.value=value;effort.append(option);}form.session.set('effort','');});
    const refreshSummary=runtimeSummary(form,runtimeName||catalog.runtime.name,kind,()=>show('add-assistant'));
    form.lifecycle.settled=()=>{if(uncertain){for(const control of form.controls.values())control.disabled=true;form.root.querySelector('.assistant-runtime-summary button').disabled=true;form.root.querySelector('.editor-actions .primary').textContent=translate(locale(),'retry');}};
    form.lifecycle.afterSave=async saved=>{
      const value=saved.managed_assistant,owner='agent-link:'+connectionID+':'+value.address.agent_id;
      const assistant={id:owner,name:value.name,avatar:value.avatar,space_id:space,address:value.address,revision:value.revision,capabilities:value.capabilities,origin:{link_id:connectionID,node_id:value.address.node_id,assistant_id:value.address.agent_id,kind:catalog.runtime.kind,name:runtimeName||catalog.runtime.name},agent_link:{kind:catalog.runtime.kind,name:runtimeName||catalog.runtime.name,node_id:value.address.node_id}};
      await leave();await openChat({space,owner,assistant,created:!existing});
    };
    if(!existing)createdChatLifetime(form,captured);
    form.lifecycle.locale=()=>{if(executionHint)executionHint.textContent=translate(locale(),'settings.nextExecution');refreshSummary();const effort=form.controls.get('effort');if(effort)for(const option of effort.options)option.textContent=t(option.value?'effort.'+option.value:'effortDefault');const workspace=form.controls.get('workspace');if(workspace)for(const option of workspace.options){const item=catalog.workspaces.find(item=>item.id===option.value);option.textContent=workspaceLabel(item);}form.lifecycle.setTitle?.(t(existing?'assistantUpdate':'assistantCreate'));for(const definition of definitions){const input=form.controls.get(definition.name),label=input?.closest('.form-row')?.querySelector('.form-label');if(label)label.textContent=t(definition.key);input?.setAttribute('aria-label',t(definition.key));}const submit=form.root.querySelector('.editor-actions .primary'),cancel=form.root.querySelector('.editor-actions button:not(.primary)');if(submit)submit.textContent=form.session.pending?translate(locale(),'assistant.creating'):uncertain?translate(locale(),'retry'):t(existing?'assistantUpdate':'assistantCreate');if(cancel)cancel.textContent=translate(locale(),'integrations.cancel');};
  }
  async function addAssistantPage(root,token) {
    assistantStartRefresh=null;
    const t=key=>translate(locale(),'assistantStart.'+key),captured={space:client.spaces.find(item=>item.kind==='local')?.id||scope().space,owner:''};
    heading(t('title'));root.classList.add('assistant-start');
    const intro=n('div','assistant-start-intro');intro.append(n('h1','',t('title')),n('p','hint',t('description')));root.append(intro);
    const options=n('div','assistant-runtime-list'),status=n('p','inline-error');status.setAttribute('role','alert');root.append(options,status);
    let projection=options.cloneNode(false),snapshot={items:[]},authorityByRuntime=new Map();
    function runtimeRow({id="local",name,kind,description,status:state,select,manage,valid=()=>true}) {
      const row=n('div','assistant-runtime-row'),content=n('span','assistant-runtime-info'),title=n('strong','',name),detail=n('span','hint',description),label=n('span','assistant-runtime-status',state);
      row.dataset.runtimeId=id;row.dataset.viewKey=id;content.append(title,detail);
      const boot=client.runtime.boot_id,generation=client.connectionGeneration,control=client.spaces.find(item=>item.id===captured.space)?.control_id,initiatingScope=JSON.stringify(scope());
      const current=()=>!disposed&&gate.current(token)&&host.firstElementChild===root&&JSON.stringify(scope())===initiatingScope&&client.runtime.boot_id===boot&&client.connectionGeneration===generation&&client.operationScopeReady?.(captured)!==false&&client.spaces.find(item=>item.id===captured.space)?.connected!==false&&client.spaces.find(item=>item.id===captured.space)?.control_id===control&&valid();
      if(select)authorityByRuntime.set(id,current);
      if(select){const button=action('',()=>{if(current()&&!assistantCatalogLoad)select();});button.disabled=!current();button.classList.add('assistant-runtime-choice');if(id!=='local')button.setAttribute('aria-label',t('runtimeLabel').replace('{brand}',(['pot','codex','antigravity'].includes(kind)?translate(locale(),'agentLinks.'+(kind==='antigravity'?'sourceAntigravity':kind==='codex'?'sourceCodex':'sourcePot')):t('otherRuntime'))).replace('{name}',name).replace('{description}',description).replace('{status}',state));button.append(connectionBrand(kind),content,label,icon('chevron-right'));row.append(button);}
      else {const button=action(t('manage'),manage);button.setAttribute('aria-label',t('manageConnection').replace('{name}',name));row.append(connectionBrand(kind),content,label,button);}
      projection.append(row);
    }
    const connect=action(t('connectNew'),()=>connection());connect.classList.add('assistant-connect-new');root.append(connect);
    function render(){
        projection=options.cloneNode(false);authorityByRuntime=new Map();heading(t('title'));intro.querySelector('h1').textContent=t('title');intro.querySelector('p').textContent=t('description');connect.textContent=t('connectNew');
        runtimeRow({name:t('localTitle'),kind:'pot',description:t('localDescription'),status:t('available'),select:localAssistantEditor});
        for(const link of snapshot.items||[]) {
          const targets=link.directory?.targets||[],node=targets.find(item=>item.address?.kind==='node'),supported=node?.capabilities?.operations?.includes('assistant.management'),online=link.status==='connected',count=targets.filter(item=>item.address?.kind==='assistant').length;
          const description=!online?t('offlineDescription'):!supported?t(node?.capabilities?.operations?.includes('task.submit')?'executorDescription':'unsupportedDescription'):link.directory?t('assistantCount').replace('{count}',String(count)):linkHostLabel(link);
          const valid=()=>{const current=snapshot.items?.find(item=>item.id===link.id);return current?.peer?.node_id===link.peer?.node_id&&current.status==='connected'&&current.directory?.targets?.some(item=>item.address?.kind==='node'&&item.capabilities?.operations?.includes('assistant.management'));};
          const name=[linkDisplayName(link),linkHostLabel(link)].filter((value,index,all)=>value&&all.indexOf(value)===index).join(' / ');runtimeRow({id:'connection:'+link.id,name,kind:link.peer?.kind||'pot',description,status:t(online?'online':'offline'),select:online&&supported?()=>linkedAssistantEditor(link.id,captured.space,'',name,link.peer?.kind,valid):null,valid,manage:()=>show('machines')});
        }
        reconcile(options,projection);for(const button of options.querySelectorAll('.assistant-runtime-choice'))catalogAuthority.set(button,authorityByRuntime.get(button.closest('[data-runtime-id]').dataset.runtimeId));
        const canConnect=authorityByRuntime.get('local');catalogAuthority.set(connect,canConnect);setDisabled(connect,!canConnect());connect.onclick=()=>{if(canConnect())connection();};
        assistantCatalogLoad?.lock();assistantCatalogLoad?.locale();for(const node of root.querySelectorAll('[data-assistant-catalog-i18n]'))node.textContent=translate(locale(),node.dataset.assistantCatalogI18n);
    }
    render();assistantStartRefresh=async()=>{if(gate.current(token))render();};
    async function refresh(){
      try {const value=await request('/api/v1/agent-links',captured);if(!gate.current(token))return;snapshot=value;status.replaceChildren();render();
      }catch{if(gate.current(token))status.replaceChildren(n('span','',t('connectionsFailed')),action(translate(locale(),'retry'),refresh));}
    }
    const view={root,token,topics:['agent-links','assistants','spaces'],space:captured.space};view.queue=createRefreshQueue(refresh,{active:()=>!disposed&&pageView===view&&gate.current(token)});pageView=view;await view.queue.run();
    async function connection() {
      if(!gate.current(token))return;
      const picker=renderEditor(t('connectTitle'),{},[],()=>{}, {capturedScope:captured});
      picker.root.classList.add('agent-kind-picker');
      picker.form.querySelector('.editor-actions .primary').hidden=true;
      const fields=picker.form.querySelector('.editor-fields'),description=n('p','hint'),choices=n('div','assistant-runtime-list');fields.append(description,choices);
      const entries=[];
      for(const kind of ['codex','antigravity','pot']){
        const row=n('div','assistant-runtime-row'),button=action('',()=>{suspendEditor();openConnection(kind);}),info=n('span','assistant-runtime-info'),title=n('strong'),detail=n('span','hint');
        button.classList.add('assistant-runtime-choice','agent-kind-choice');button.dataset.agentKind=kind;info.append(title,detail);button.append(connectionBrand(kind),info,icon('chevron-right'));row.append(button);choices.append(row);entries.push({kind,button,title,detail});
      }
      picker.lifecycle.locale=()=>{picker.lifecycle.setTitle(t('connectTitle'));description.textContent=t('connectDescription');picker.form.querySelector('.editor-actions button:not(.primary)').textContent=translate(locale(),'integrations.cancel');for(const entry of entries){entry.title.textContent=entry.kind==='pot'?t('potTitle'):translate(locale(),'agentLinks.source'+(entry.kind==='antigravity'?'Antigravity':'Codex'));entry.detail.textContent=t('connectDescription.'+entry.kind);entry.button.setAttribute('aria-label',t(entry.kind+'Action'));}};
      picker.lifecycle.locale();
    }
    function openConnection(kind) {
      let connectedID='';const lifetime=new AbortController();
      const connections=createAgentLinks({root:n('div'),request,locale,scope:()=>captured,signal:lifetime.signal,
        edit:({title,initial,fields,save,scope:context,submitLabel})=>{
          const form=renderEditor(title,initial,fields,save,{capturedScope:context,submitLabel});
          form.lifecycle.afterSave=openConnected;return form;
        },onConnected:async result=>{connectedID=result?.id||result?.link?.id;}});
      async function openConnected(){
        if(!await leave())return;route='add-assistant';routeChanged('add-assistant');const list=body();heading(t('title'));list.classList.add('assistant-start');list.append(n('h1','',t('title')));
        try{const snapshot=await request('/api/v1/agent-links',captured);if(disposed||host.firstElementChild!==list)return;const link=snapshot.items?.find(item=>item.id===connectedID);if(link?.directory?.targets?.some(item=>item.address?.kind==='node'&&item.capabilities?.operations?.includes('assistant.management'))){await linkedAssistantEditor(connectedID,captured.space,'',linkDisplayName(link),link.peer?.kind);}else{list.append(n('p','hint',t('unsupportedDescription')),action(t('manage'),()=>show('machines')),action(t('back'),()=>show('add-assistant',{force:true})));}}
        catch{if(host.firstElementChild===list)list.append(n('p','inline-error',t('connectionsFailed')),action(translate(locale(),'retry'),openConnected));}
      }
      const form=connections.addAgent({kind,onChangeKind:()=>back()}),dispose=form.lifecycle.dispose;
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
    if(client.spaces.length>1){const owner=field(t('agentLinks.selectControl'),agentOwnerSpace,{choices:client.spaces.map(space=>[space.id,space.name||t('machine.unavailable')])});connectionRoot.append(owner.row);owner.input.onchange=()=>{agentOwnerSpace=owner.input.value;connections.load();};}
    const connections=createAgentLinks({root:connectionRoot,request,locale,notify,scope:selectedScope,edit,signal:token.signal,onConnected:async result=>{const id=result?.id||result?.link?.id;if(id){const peer=result.peer||result.link?.peer;const identity=peer?.address?{id:peer.address.agent_id,node_id:peer.address.node_id,kind:peer.adapter}:peer;const owned=identity?.kind==='pot'&&client.spaces.find(space=>space.control_id===identity.node_id);topologySelection=owned?'control:'+owned.id:agentNodeID(identity);}await pageView?.queue.run();}});
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
        const assistants=node.link?.assistants||node.link?.directory?.targets?.filter(item=>item.address?.kind==='assistant')||(!node.link?(client.assistants||[]).filter(item=>!item.origin&&item.space_id===node.space):[]),directory=n('section','topology-assistants');
        directory.append(n('h3','section-heading',t('agentLinks.providedAssistants')));
        if(!assistants.length)directory.append(n('p','hint',t('agentLinks.noAssistants')));
        for(const assistant of assistants){
          const remoteID=assistant.address?.agent_id||assistant.id,id=node.link?'agent-link:'+node.link.id+':'+remoteID:assistant.id;
          const open=action('',()=>openChat({space:node.space,owner:id}));open.className='control topology-assistant';
          open.append(avatar(24,assistant.avatar),n('span','topology-assistant-name',assistant.name||remoteID));
          const activity=node.activities?.get(remoteID),state=node.observation?.fresh?assistantActivityState(activity):'unknown';
          open.append(n('span','topology-assistant-state state-'+state,t('topology.activity.'+state)));
          if(remoteID===(node.link?.default_assistant_id||node.link?.directory?.default_target?.agent_id))open.append(n('span','hint',t('agentLinks.defaultAssistant')));
          if(node.link&&!assistant.capabilities?.operations?.includes('chat.read'))open.disabled=true;
          open.setAttribute('aria-label',t('agentLinks.openChat')+' · '+(assistant.name||remoteID));directory.append(open);
        }
        if(node.link?.directory?.targets?.some(item=>item.capabilities?.operations?.includes('assistant.management'))){
          directory.append(action(t('agentLinks.manageAssistants'),()=>linkedAssistantEditor(node.link.id,node.space,'',linkDisplayName(node.link),node.link.peer?.kind)));
          for(const assistant of assistants)directory.append(action(t('agentLinks.editExecution')+' · '+assistant.name,()=>linkedAssistantEditor(node.link.id,node.space,assistant.address?.agent_id||assistant.id,linkDisplayName(node.link),node.link.peer?.kind)));
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
    let needsPermissions=true,freshnessTicks=0;
    const redrawTopology=()=>{if(!data.inventory.length)return;const restore=preserveInspectorFocus(),edgeSelection=currentEdgeID,items=data.inventory.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner));model=projectTopology(client.spaces,items,data.linkPages);view.update(model);if(edgeSelection){const edge=model.edges.find(item=>item.id===edgeSelection);if(edge)showEdge(edge);}restore();};
    const freshnessTimer=setInterval(()=>{if(!document.hidden&&gate.current(token)&&!editor){redrawTopology();if(++freshnessTicks%3===0)pageView?.queue.run();}},10000);
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
      const readPermissions=needsPermissions;needsPermissions=false;
      data.permissionsPending=readPermissions;
      const permissions=readPermissions?across('/api/v1/permissions',token).catch(()=>{needsPermissions=true;return [];}).then(pages=>{data.permissionPages=pages;data.permissionsPending=false;if(data.inventory.length)return update(data);}):Promise.resolve();
      const links=across('/api/v1/agent-links',token,{errorTarget:connectionRoot}).then(pages=>{data.linkPages=pages;const selected=pages.find(page=>page.space.id===agentOwnerSpace);if(selected)connections.update(selected.value);linkNotice.hidden=true;}).catch(reason=>{if(reason.name==='AbortError')return;linkNotice.hidden=false;linkNotice.replaceChildren(n('span','',t('topology.connectionUnavailable')),action(t('retry'),()=>pageView?.queue.run()));});
      data.inventory=(await across('/api/v1/runtime',token)).map(page=>{const machine=runtimeMachine(page.value);return {...page,value:machine?[machine]:[]};});await links;await update(data);await permissions;return data;
    },update,{topics:['machines','spaces','permissions','agent-links','tasks','assistant-mail','runtime'],space:client.spaces?.length===1?client.spaces[0].id:'',invalidate:topics=>{if(!topics.length||topics.some(topic=>['permissions','spaces'].includes(topic)))needsPermissions=true;}});
  }
  const approvalScope=()=>taskView?.scope||scope();
  const approvalContext=()=>JSON.stringify([approvalScope(),taskView?.task.id||'']);
  function renderApprovals() {
    taskView?.ui.refreshApprovals();
    const chatContext=JSON.stringify(scope());
    if(disposed||approvalOwner!==approvalContext()){for(const card of approvalCards.values())card.invalidate();approvalHost.replaceChildren();chatApprovals.update(chatContext,[]);taskView?.ui.setApprovals([]);return;}
    const entries=[],retained=new Set();
    for(const approval of pendingApprovals) {
      const toolRoot=taskView?.state?.messages.some(message=>message.tool?.id===approval.request.call_id&&message.tool.turn_id===approval.request.turn);
      if(toolRoot){continue;}
      const key=JSON.stringify([approvalOwner,approval.id]);retained.add(key);
      if(!approvalCards.has(key)) {
        const capturedKey=approvalOwner;
        approvalCards.set(key,createApprovalCard({locale,machineLabel:id=>machineInfo.label(id,approvalScope(),locale()),onDecision:async(original,decision)=>{
          if(approvalContext()!==capturedKey)throw Error(translate(locale(),'approvals.contextChanged'));
          await decideApproval(original,decision);
        }}));
      }
      const card=approvalCards.get(key),value={...structuredClone(approval),fresh:approvalFresh},options={taskTitle:taskNames.get(taskNameKey(approvalScope(),approval.request.task))};if(!approvalFresh)card.invalidate();
      {
        const capturedKey=approvalOwner,capturedScope={...approvalScope()};
        entries.push({id:approval.id,revision:JSON.stringify([value,options]),
          prepare:()=>{const candidate=createApprovalCard({locale,machineLabel:id=>machineInfo.label(id,capturedScope,locale()),onDecision:async(original,decision)=>{if(approvalContext()!==capturedKey)throw Error(translate(locale(),'approvals.contextChanged'));await decideApproval(original,decision);}});candidate.update(value,options);candidate.element.open=card.element.open;candidate.element.preparedApprovalCard=candidate;return candidate.element;},
          commit:prepared=>{if(prepared?.preparedApprovalCard){card.invalidate();approvalCards.set(key,prepared.preparedApprovalCard);return prepared;}card.update(value,options);return card.element;},
        });
      }
    }
    chatApprovals.update(chatContext,taskView?[]:entries);taskView?.ui.setApprovals(entries);
    for(const key of approvalCards.keys())if(!retained.has(key)){approvalCards.get(key).invalidate();approvalCards.delete(key);}
    approvalHost.replaceChildren();
  }
  let approvalQueue=null,approvalQueueKey='',approvalReadAt=0,approvalFailures=0;
  function permissions(){
    const captured={...approvalScope()},key=approvalContext();
    if(!captured.owner||disposed||!supportsCapability((client.assistants||[]).find(item=>item.id===captured.owner&&item.space_id===captured.space),'approvals.manage')){approvalQueue?.close();approvalQueue=null;approvalQueueKey='';approvalGate.close();approvalFresh=false;pendingApprovals=[];approvalOwner='';for(const card of approvalCards.values())card.invalidate();approvalCards.clear();approvalHost.replaceChildren();chatApprovals.update(JSON.stringify(scope()),[]);taskView?.ui.setApprovals([]);taskView?.ui.refreshApprovals();onApprovalsChanged();return Promise.resolve();}
    if(!approvalQueue||approvalQueueKey!==key){
      approvalQueue?.close();approvalQueueKey=key;const token=approvalGate.next(),current=()=>!disposed&&approvalGate.current(token)&&approvalQueueKey===key&&approvalContext()===key;
      approvalFresh=false;pendingApprovals=[];approvalOwner='';for(const card of approvalCards.values())card.invalidate();approvalCards.clear();approvalHost.replaceChildren();chatApprovals.update(JSON.stringify(scope()),[]);taskView?.ui.setApprovals([]);taskView?.ui.refreshApprovals();onApprovalsChanged();
      approvalQueue=createRefreshQueue(async()=>{
        approvalReadAt=Date.now();
        const [value]=await Promise.all([request('/api/v1/permissions',{...captured,signal:token.signal}),machineInfo.load({...captured,signal:token.signal})]);if(!current())return;
        approvalFailures=0;approvalFresh=true;pendingApprovals=(value.pending||[]).filter(item=>item.request?.owner===captured.owner&&(!taskView||item.request.task===taskView.task.id));approvalOwner=key;
        if(taskView)renderTaskTranscript(taskView);onApprovalsChanged();renderApprovals();
        await Promise.all([...new Set(pendingApprovals.map(item=>item.request.task).filter(Boolean))].map(async id=>{
          const nameKey=taskNameKey(captured,id);if(taskNames.has(nameKey))return;
          try{const record=await taskDomain.metadata({id},captured,{signal:token.signal});if(current())taskNames.set(nameKey,record.info?.title||record.request?.text||id);}catch{/* Retain the exact Task ID when its title is unavailable. */}
        }));if(current()){renderApprovals();if(taskView)renderTaskTranscript(taskView);}
      },{active:current,onError:reason=>{approvalFailures++;approvalFresh=false;renderApprovals();if(taskView)renderTaskTranscript(taskView);onApprovalsChanged();notify(reason.message);}});
    }return approvalQueue.run();
  }
  // Local changes have a monitored push source. Linked owners have no such
  // delivery guarantee, and unhealthy local streams need bounded recovery.
  const approvalTimer=setInterval(()=>{
    if(disposed||document.hidden||!chatVisible()&&!taskView)return;
    const captured=approvalScope(),assistant=client.assistants?.find(item=>item.id===captured.owner&&item.space_id===captured.space);
    if(!assistant?.origin&&client.changesHealthy?.(captured.space)&&approvalFresh)return;
    const delay=Math.min(5000*2**Math.min(approvalFailures,3),30000);
    if(Date.now()-approvalReadAt>=delay)permissions();
  },1000);
  async function show(next,{keepUsageOwner=false,force=false}={}) {
    if(editor)return;
 if(extensionUI&&next==='extensions')return extensionUI.refresh();extensionUI?.dispose();extensionUI=null;
    if(!keepUsageOwner)usageOwner=null;
    if(next==='settings'&&filter.settingsTab!=='general'&&!configurationSpace)configurationSpace=scope().space;
    const viewKey=JSON.stringify([next,next==='tasks'?[filter.taskTab,filter.taskScope,filter.taskQuery,filter.taskPhase,filter.listOwner,filter.scheduleQuery,filter.taskOffset,filter.scheduleOffset]:next==='usage'?[filter.usageSpace,filter.usageRange,filter.usageYear,usageOwner]:next==='settings'?[filter.settingsTab,configurationSpace]:[]]);
    if(!force&&route===next&&currentViewKey===viewKey){if(taskListView)return refreshTaskList();if(pageView)return pageView.queue.run();}
    captureView();viewReady=false;assistantCatalogLoad?.close();pageView?.queue.close();pageView=null;

    closeTaskView();taskOrigin=null;closeTaskList();scheduleView=null;detailParent=false;if(!keepUsageOwner)usageOwner=null;
    route=next;heading(next==='tasks'?translate(locale(),filter.taskTab==='schedules'?'tasks.schedulesTab':'tasks.tab'):next==='extensions'?translate(locale(),'extensions.title'):tr({tasks:filter.taskTab==='schedules'?'Schedules':'Tasks',machines:translate(locale(),'topology.title'),usage:'Usage',settings:'Settings'}[next]||next,{tasks:filter.taskTab==='schedules'?'定时计划':'任务',machines:translate(locale(),'topology.title'),usage:'用量',settings:'设置'}[next]||next));
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
  const preference=()=>{if(assistantLoad){renderAssistantLoad();return;}if(extensionUI){heading(translate(locale(),'extensions.title'));return;}if(editor){editor.lifecycle.locale?.();for(const status of editor.root.querySelectorAll('[data-error-key]')){const span=status.querySelector(':scope>span');if(span)span.textContent=translate(locale(),status.dataset.errorKey);const summary=status.querySelector('details>summary');if(summary)summary.textContent=translate(locale(),'agentLinks.diagnostics');}return;}if(route==='add-assistant'&&assistantStartRefresh){assistantStartRefresh().catch(()=>notify(translate(locale(),'assistantStart.refreshFailed')));return;}if(route==='settings'&&filter.settingsTab==='integrations'&&!editor){heading(translate(locale(),'settings'));for(const node of host.querySelectorAll('[data-settings-i18n]'))node.textContent=translate(locale(),node.dataset.settingsI18n);return;}if(taskView){taskDetail(taskView.task,{recordHistory:false,returnTo:taskView.returnTo});return;}if(scheduleView&&!editor){scheduleHistory(scheduleView.item,false,scheduleView.runsOffset);return;}if(!editor&&route)show(route,{keepUsageOwner:!!usageOwner,force:true});};
  window.addEventListener('beforeunload',beforeUnload);window.addEventListener('pot:locale',preference);
  return {get authorityReady(){return editorAuthorityReady();},get title(){return title;},get editing(){return !!editor;},get approvalSpace(){return approvalScope().space;},get notificationContext(){return taskView?{space:taskView.scope.space,owner:taskView.scope.owner,task:taskView.task.id}:null;},get taskOwner(){return taskView?.scope.owner||'';},get hasParent(){return !!assistantLoad||!!editor||detailParent||!!usageOwner||route==='add-assistant'&&!host.hidden;},show,assistant,setupAI,leave,back,permissions,syncApprovals:renderApprovals,taskMenu(){taskView?.ui.openMenu();},handleHistory:popstate,openSchedule:scheduleHistory,openUsage:openUsageView,clearApprovals(){approvalQueue?.close();approvalQueue=null;approvalQueueKey='';approvalGate.close();approvalOwner='';approvalFresh=false;pendingApprovals=[];for(const card of approvalCards.values())card.invalidate();approvalCards.clear();approvalHost.replaceChildren();chatApprovals.update(JSON.stringify(scope()),[]);taskView?.ui.setApprovals([]);taskView?.ui.refreshApprovals();onApprovalsChanged();},approvalFor,decideApproval,taskNeedsApproval,rememberTasks,renderApprovals,conversationUsage(name,record=true){return openUsageView({...scope(),name},record);},openTask(task,options={}){route='tasks';return taskDetail(task,options);},
    approvalRegion:()=>chatApprovals.region(),
    showProviders(){filter.settingsTab='providers';return show('settings');},
    refresh(topics=[],source={}){
      const changed=source.domainNotified?Promise.resolve():domains.changed(topics,source);if(pageView?.domain||taskView||taskListView)return changed;
 if(extensionUI){if(!topics.length||topics.includes('extensions')||topics.includes('spaces'))extensionUI.load().catch(()=>{});return changed;}
      if(editor){editor.lifecycle.refreshDelete?.();if(topics.includes('extensions')&&(!source.space||source.space===editor.session.context.space))editor.lifecycle.providersInvalidated?.();return;}if(disposed||!route||!host.offsetParent)return;
      if(pageView){if(relevantChange(topics,pageView.topics,pageView.space,source)){pageView.invalidate?.(topics);return pageView.queue.run();}return;}
      if(route==='settings')return;
      if(route==='tasks'&&!relevantChange(topics,['tasks','runner','spaces','assistants','machines',...(filter.taskTab==='schedules'?['schedules']:[])],taskView?.scope.space||'',source))return;
      if(taskListView)return refreshTaskList();if(detailParent&&!taskView&&!scheduleView)return;
      if(taskView)return refreshTaskEvents();

    },
    dispose(){assistantCatalogLoad?.close();releaseDomains();extensionUI?.dispose();stopResources?.();notificationSettings?.dispose();for(const value of [...editorParents,...(editor?[editor]:[])])value.lifecycle?.dispose?.();disposed=true;clearInterval(approvalTimer);closeTaskList();closeTaskView();approvalQueue?.close();pageView?.queue.close();gate.close();approvalGate.close();window.removeEventListener('beforeunload',beforeUnload);window.removeEventListener('pot:locale',preference);},
  };
}
