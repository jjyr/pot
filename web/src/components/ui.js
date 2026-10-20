import {createUpdates} from './updates.mjs';
import {mergeHistoryPage} from '../state/chat-history.mjs';
import {createMessageWindow,readingAnchor,restoreReadingAnchor} from './message-window.mjs';
import {notificationOnboarding} from './notification-onboarding.mjs';
import {createSendConfirmation} from '../state/send-confirmation.mjs';
import {bindMessageSelection,renderMessageReferences,referenceProblem} from './message-references.mjs';
import {createSystemNotifications} from '../adapters/notifications.mjs';
import {createToolCall} from './tool-call.mjs';
import {toolForest} from '../state/tool-calls.mjs';
import {projectScriptRuns} from '../state/script-runs.mjs';
import {avatar} from './avatar.mjs';
import {icon} from './icons.mjs';
import {messages,translate} from '../locales/messages.mjs';
import {acceptSnapshot, beginSend, beginSubscription, createConversationState, draft, draftReferences, setDraftReferences, ownerKey, setDraft, validSnapshot} from '../state/conversation.mjs';
import {bindComposer} from './composer.mjs';
import {createAttachmentComposer,renderMessageAttachments,failedMessageInput} from './attachments.mjs';
import {renderMarkdown} from './markdown.mjs';
import {createPages} from './pages.mjs';
import {createReplies,replyKey} from '../state/replies.mjs';
import {bindCompletions} from './completions.mjs';
import {projectChatReplies} from '../state/chat-replies.mjs';
import {createReplyBubble} from './reply.mjs';
import {createTurnContinuation} from './turn-continuation.mjs';
import {reconcile} from './reconcile.mjs';
import {createRefreshQueue,relevantChange} from '../state/refresh.mjs';
import {bindTaskClock} from './task-time.mjs';
import {createBackgroundTasks} from './background-tasks.mjs';
import {captureReadingPosition,restoreReadingPosition} from './reading-position.mjs';
import {bindResumeRecovery} from '../state/resume.mjs';
import {bindViewport} from './viewport.mjs';
import {settleMotion,copyFeedback} from './motion.mjs';

function el(tag, className = '', text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}
function button(name, label, className = 'icon-button') {
  const item = el('button', className);
  item.type = 'button';
  item.title = label;
  item.setAttribute('aria-label', label);
  item.append(icon(name));
  return item;
}
function stored(key, fallback) {
  try { return localStorage.getItem(key) || fallback; } catch { return fallback; }
}
function persist(key, value) { try { localStorage.setItem(key, value); } catch { /* restricted WebView */ } }

// Product components depend only on this client contract, not browser transport
// or Wails APIs. DOM nodes, especially the editor, survive stream/state changes.
export function mountApp(root, client, {surface = 'main'} = {}) {
  const params = new URLSearchParams(location.search);
  let locale = params.get('locale') || stored('pot-go-preview-locale', navigator.language.startsWith('zh') ? 'zh-CN' : 'en');
  if (!messages[locale]) locale = 'en';
  let theme = params.get('theme') || stored('pot-go-preview-theme', 'system');
  if (!['system', 'light', 'dark'].includes(theme)) theme = 'system';
  const state = createConversationState();
  let initialReads=[];try{initialReads=client.desktop?client.readBaselines||[]:JSON.parse(stored('pot-ui-read-v1','[]'));}catch{}
  const replies=createReplies(Array.isArray(initialReads)?initialReads:[]),reactions=new Map(),readAttempts=new Set();
  let epoch = beginSubscription(state);
  let disconnected = false;
  let disposed = false;
  let route = 'chat';
  let connection = 'connecting';
  let error = '';
  let connectionError = '';
  let directoryOpen = !params.has('conversation');
  let cancelPending = false;
  let editorOwner = null;
  let menuOpen = false;
  let preferencesOpen = false,restoringHistory=false;
  let assistants = [], spaces = [], selectedAssistant = null, pendingSelection = null;
  let unsubscribe = () => {}, stopChanges = () => {};
  let directoryEpoch = 0, directorySignature = '';
  const positions = new Map(), cachedSnapshots = new Map(), readingPositions = new Map();
  let chatShown=false,pendingReadingPosition=null,pendingTaskToggle=null,lastTaskReadingPosition=null,pendingFollow=false,followFrame=0;
  let restoredSnapshot=false,awaitingSnapshot=false;
  const snapshotKey=(space=client.scope?.().space||'',owner=client.scope?.().owner||'')=>JSON.stringify([(client.spaces||[]).find(item=>item.id===space)?.control_id||client.runtime?.runtime_id||'',space,owner]);
  const t = key => translate(locale,key);
  root.replaceChildren();
  root.className = `pot-app surface-${surface === 'tray' ? 'tray' : 'main'}`;
  const rail = el('nav', 'feature-rail');
  const titlebar = el('header', 'titlebar');
  const directory = el('aside', 'assistant-directory');
  const workspace = el('main', 'workspace');
  const notice = el('p', 'preview-notice');
  const notificationError=el('div','notification-route-error inline-error');notificationError.hidden=true;notificationError.setAttribute('role','alert');
  const approvals = el('div', 'approval-host');
  const chatApprovals=el('section','chat-approvals');chatApprovals.hidden=true;
  let locateApprovals='';
  const resourceNotices=el('div','resource-notices');resourceNotices.hidden=true;resourceNotices.dataset.empty='true';
  const history = el('div', 'history');
  history.setAttribute('role', 'log');
  history.setAttribute('aria-label', t('chat'));
  const messageColumn = el('div', 'message-column');
  const taskClock=bindTaskClock(messageColumn,{locale:()=>locale,active:()=>!disposed&&route==='chat'&&!root.classList.contains('show-directory')});
  const historyStatus=el('button','history-page-status text-button');historyStatus.type='button';
  const windowHost=el('div','message-window');
  messageColumn.append(historyStatus,windowHost,chatApprovals);
  history.append(messageColumn);
  const status = el('div', 'connection-status');
  status.setAttribute('role', 'status');
  const latest = button('down', t('latest'), 'latest-button');
  latest.hidden = true;
  const composerArea = el('div', 'composer-area');
  const form = el('form', 'composer');
  const input = el('textarea', 'composer-input');
  input.id = 'pot-composer';
  input.rows = 1;
  input.setAttribute('autocapitalize', 'sentences');
  const send = button('send', t('send'), 'send-button');
  form.append(input, send);
  composerArea.append(latest, form, status);
  const continuation=client.production?createTurnContinuation({client,locale:()=>locale}):null;
  if(continuation)composerArea.prepend(continuation.root);
  const unavailable = el('div', client.production ? 'page-host' : 'unavailable');
  workspace.append(notice, notificationError, approvals, resourceNotices, history, composerArea, unavailable);
  const menu = el('nav', 'mobile-menu');
  menu.hidden = true;
  const preferences = el('dialog', 'preferences');
  root.append(rail, titlebar, directory, workspace, menu, preferences);
  const notifications=client.production?createSystemNotifications({client,surface,locale:()=>locale,
    view:()=>({...((route==='tasks'&&pages?.notificationContext)||{space:selectedAssistant?.space_id||'',owner:selectedAssistant?.id||''}),route:menuOpen||preferencesOpen||root.classList.contains('show-directory')?'navigation':route}),
    open:openNotification}):null;
  if(notifications)client.notifications=notifications;
  const onboarding=notifications&&!client.desktop?notificationOnboarding({notifications,locale:()=>locale}):null;
  if(onboarding)root.append(onboarding.element);
  let notificationReady=false;
  function mainReady(){
    if(notificationReady)return;notificationReady=true;
    // The successful initial directory read has mounted the main interface.
    // Wait for a paint; rendering, reconnects and tray setup do not authorize.
    requestAnimationFrame(()=>requestAnimationFrame(()=>{if(!disposed)notifications?.ready();}));
  }
  async function openNotification(event){
    notificationError.hidden=true;
    try{
      const item=assistants.find(value=>value.space_id===event.space&&value.id===event.owner&&controlOf(value)===event.control);
      if(!item)throw Error(t('missingAssistant'));
      if(!await pages.leave())return;
      if(event.task){
        route='tasks';directoryOpen=false;
        await pages.openTask({id:event.task,title:event.name,assistant_id:event.owner,_assistant_id:event.owner,_space_id:event.space});
        renderShell();return;
      }
      await selectAssistant(item,false);
      locateApprovals=event.kind==='approval'?JSON.stringify({owner:event.owner,space:event.space}):'';
      renderMessages();await pages.permissions();
    }catch(reason){
      const dismiss=button('close',t('close'));dismiss.onclick=()=>{notificationError.hidden=true;};
      notificationError.replaceChildren(el('span','',reason.message),dismiss);notificationError.hidden=false;throw reason;
    }
  }
  const updates=client.production?createUpdates({client,locale:()=>locale,changed:()=>{if(!disposed)renderShell();},notify:message=>{error=message;updateControls();}}):null;
 if(updates)root.append(updates.dialog);
 const pages = client.production ? createPages({chatVisible:()=>route==='chat'&&!root.classList.contains('show-directory'),onApprovalsChanged:()=>renderMessages(),client, host: unavailable, approvalHost: approvals, chatApprovalHost:chatApprovals, locale: () => locale, notify: value => {error = value; updateControls();}, changed: value => {if(value?.assistant)pendingSelection=value.assistant;loadDirectory();}, titleChanged: () => renderShell(),routeChanged:value=>{route=value;renderShell();}, openChat:async target=>{if(target&&(!selectedAssistant||selectedAssistant.id!==target.owner||selectedAssistant.space_id!==target.space)){const assistant=assistants.find(item=>item.id===target.owner&&item.space_id===target.space);if(assistant)await selectAssistant(assistant,false,false);}route='chat';directoryOpen=false;renderShell();renderMessages();pages.permissions();if(!selectedAssistant)loadDirectory();}}) : null;
  const media = matchMedia('(max-width: 719px)');
  const reducedMotion=matchMedia('(prefers-reduced-motion: reduce)');
  const saveReads=()=>{if(!client.desktop)persist('pot-ui-read-v1',JSON.stringify(replies.records()));};
  const controlOf=item=>item?.control_id||item?.space_id||client.runtime?.runtime_id||'';
  function markVisibleRead(){
    const item=selectedAssistant;if(restoringHistory||!client.production||!item||item.offline||route!=='chat'||menuOpen||document.visibilityState==='hidden'||!document.hasFocus()||(media.matches&&directoryOpen))return;
    const count=item.reply_count||0,id=item.reply_id||'';
    if(id&&!state.snapshot?.messages.some(message=>message.id===id&&message.role==='assistant'&&message.status==='complete'))return;
    const control=controlOf(item),key=JSON.stringify([control,item.id,id,count]);if(readAttempts.has(key))return;
    readAttempts.add(key);
    if(client.markRead)client.markRead({space_id:item.space_id,owner:item.id,reply_id:id,reply_count:count}).then(receipt=>{if(disposed)return;replies.adopt(receipt);directorySignature='';renderShell();}).catch(()=>{readAttempts.delete(key);});
    else{const unread=replies.unread(control,item.id,count);replies.mark(control,item.id,id,count);saveReads();return unread>0;}
  }
  function assistantAvatar(item,size){
    const value=avatar(size,item?.avatar),started=reactions.get(replyKey(controlOf(item),item?.id||''));
    if(started&&!reducedMotion.matches&&performance.now()-started<650){value.classList.add('is-arriving');value.style.animationDelay=`-${performance.now()-started}ms`;}
    return value;
  }
  const focusRead=()=>{markVisibleRead();if(!disposed)renderShell();};
  const readStorage=event=>{if(event.key!=='pot-ui-read-v1'||client.desktop)return;try{for(const record of JSON.parse(event.newValue||'[]'))replies.adopt(record);directorySignature='';renderShell();}catch{}};
  const stopRead=client.onRead?.(receipt=>{replies.adopt(receipt);directorySignature='';if(!disposed)renderShell();})||(()=>{});
  const reducedChanged=()=>{client.setReducedMotion?.(reducedMotion.matches).catch(()=>{});if(reducedMotion.matches)reactions.clear();if(!disposed)renderShell();};
  client.setReducedMotion?.(reducedMotion.matches).catch(()=>{});
  reducedMotion.addEventListener('change',reducedChanged);window.addEventListener('focus',focusRead);document.addEventListener('visibilitychange',focusRead);window.addEventListener('storage',readStorage);
  const editor = bindComposer(input, {
    onInput: text => { setDraft(state, text, editorOwner); updateControls(); },
    onSend: submit,
    onCompositionEnd: () => { syncEditor(); updateControls(); },
  });
  const stopViewport = bindViewport(document.documentElement, {onWidthChange: () => editor.resize()});
  const restoredAttachmentFailures=new Set(),attachmentAttempts=new Map();
  const attachmentUI=client.production?createAttachmentComposer({client,form,input,getKey:()=>editorOwner,getCaps:()=>state.snapshot?.agent?.attachments,isSending:()=>state.pending.has(editorOwner),canAdd:()=>route==='chat'&&!!state.snapshot&&connection==='connected'&&!state.pending.has(editorOwner)&&!selectedAssistant?.offline,changed:()=>updateControls(),locale:()=>locale}):null;
  if(attachmentUI){form.classList.add('has-attachments');attachmentUI.toolbar.append(send);}
  const referenceDraftHost=el('div','composer-references');form.prepend(referenceDraftHost);
  const referenceStatus=el('div','reference-announcement');referenceStatus.setAttribute('role','status');composerArea.append(referenceStatus);
  let referenceDraftSignature='',referenceNotice='';
  let historyLoading=false,historyFailure='',historyGeneration=0,historyFlight=null;
  function readHistory(query,options={}){
    const receivedEpoch=epoch;
    if(historyLoading&&historyFlight)return historyFlight.then(()=>receivedEpoch===epoch?readHistory(query,options):false);
    historyFlight=readHistoryNow(query,options);return historyFlight;
  }
  async function readHistoryNow(query,{around=false}={}) {
    if(!client.history||!state.snapshot||historyLoading)return false;
    const receivedEpoch=epoch,generation=++historyGeneration,scope={...client.scope()},anchor=readingAnchor(history);
    historyLoading=true;historyFailure='';renderMessages();
    try {
      const page=await client.history(query,{...scope,signal:abort.signal});
      if(disposed||receivedEpoch!==epoch||generation!==historyGeneration)return false;
      const merged=mergeHistoryPage(state.snapshot,page,state.uiAnchor,{around});
      if(!merged)return false;
      state.snapshot=merged;cacheSnapshot(merged);lastAnchor=anchor;resizeAnchor=true;renderMessagesNow();
      if(around&&anchor){messageWindow.seek(anchor.id);renderMessagesNow();restoreReadingAnchor(history,anchor);}
      return true;
    }catch(reason){if(!disposed&&receivedEpoch===epoch){historyFailure=reason?.status===404?'history.missing':'history.failed';}return false;}
    finally{if(!disposed&&receivedEpoch===epoch&&generation===historyGeneration){historyLoading=false;renderMessages();}}
  }
  async function jumpToMessage(id){
    let message=state.snapshot?.messages.find(message=>message.id===id&&!message.context_only);
    if(!message){if(!await readHistory({around:id},{around:true}))return;message=state.snapshot?.messages.find(message=>message.id===id);}
    if(!message){historyFailure='history.missing';renderMessages();return;}
    state.uiAnchor=message.sequence;
    // Estimate once to mount the target; measured windows refine its position.
    following=false;renderMessagesNow();messageWindow.seek(message.id);
    const targetRow=()=>[...messageNodes.values()].find(record=>record.row.dataset.messageId===id)?.row;
    for(let attempt=0;attempt<4&&!targetRow();attempt++){renderMessages();await new Promise(requestAnimationFrame);}
    const row=targetRow();if(!row)return;
    for(let parent=row.parentElement?.closest('details');parent;parent=parent.parentElement?.closest('details'))parent.open=true;
    row.scrollIntoView({block:'center',behavior:'instant'});following=false;row.classList.remove('reference-target');requestAnimationFrame(()=>row.classList.add('reference-target'));
  }

  const stopMessageSelection=bindMessageSelection(history,{capture:refs=>{
    if(route!=='chat'||!state.snapshot||editorOwner!==ownerKey(state.snapshot)||state.pending.has(editorOwner))return;
    const next=[...draftReferences(state)];for(const ref of refs)if(!next.some(old=>old.message_id===ref.message_id&&old.quote===ref.quote))next.push(ref);
    if(referenceProblem(next)){referenceNotice='references.limit';updateControls();return;}
    setDraftReferences(state,next);referenceNotice='references.added';updateControls();
  }});
  function updateReferenceDraft(){
    referenceStatus.textContent=referenceNotice?t(referenceNotice):'';referenceStatus.className=referenceNotice==='references.limit'?'reference-limit':'reference-announcement';
    const refs=draftReferences(state,editorOwner),next=JSON.stringify([editorOwner,refs,locale,!!state.pending.has(editorOwner)]);
    if(next===referenceDraftSignature)return;referenceDraftSignature=next;referenceDraftHost.hidden=!refs.length;
    referenceDraftHost.replaceChildren(renderMessageReferences(refs,{messages:state.snapshot?.messages||[],locale:()=>locale,onJump:jumpToMessage,disabled:state.pending.has(editorOwner),onRemove:index=>{setDraftReferences(state,refs.filter((_,i)=>i!==index),editorOwner);referenceNotice='references.removed';updateControls();}}));
  }
  function retryAttachmentInput(){
    const failed=failedMessageInput(state.snapshot);
    return failed&&failed.input.text===draft(state)&&attachmentUI?.matches(failed.input.attachments)&&JSON.stringify(draftReferences(state))===JSON.stringify(failed.input.references||[])?failed.target:null;
  }
  function restoreFailedAttachments(){
    const snapshot=state.snapshot,failed=failedMessageInput(snapshot);
    if(!attachmentUI||!failed||state.pending.has(ownerKey(snapshot)))return;
    const attempt=attachmentAttempts.get(ownerKey(snapshot));
    if(attempt&&BigInt(snapshot.cursor)<=BigInt(attempt.cursor))return;
    const key=JSON.stringify([ownerKey(snapshot),failed.target,attempt?.requestID||'history']);
    if(restoredAttachmentFailures.has(key))return;
    restoredAttachmentFailures.add(key);
    if(!draft(state)&&!attachmentUI.items().length&&!draftReferences(state).length){setDraft(state,failed.input.text);setDraftReferences(state,failed.input.references||[]);attachmentUI.restore(editorOwner,failed.input.attachments||[],{...client.scope()});syncEditor();}
  }
  const completions=client.production?bindCompletions(input,{client,host:composerArea,locale:()=>locale,mobile:editor.mobile}):null;
  function syncEditor() {
    if (!state.snapshot) return;
    const nextOwner = ownerKey(state.snapshot);
    // Finish an IME commit into its original draft before adopting a newly
    // attached owner. Reconnecting must not disable or replace native preedit.
    if (editor.composing && editorOwner !== nextOwner) return;
    if(editorOwner!==nextOwner)referenceNotice='';
    editorOwner = nextOwner;
    editor.setValue(draft(state, editorOwner));
  }
  function requestID() { return crypto.randomUUID(); }
  const confirmations=createSendConfirmation({state,client,
    accepted:receipt=>attachmentUI?.clearSubmitted(receipt.key,receipt.request.attachments||[]),
    rejected:receipt=>{if(receipt.key===editorOwner)error=t('send.rejected');},
    changed:()=>{if(!disposed){syncEditor();restoreFailedAttachments();updateControls();}},
  });
  function submit() {
    if ((!client.production&&state.snapshot?.running) || selectedAssistant?.offline || connection !== 'connected' || editorOwner !== (state.snapshot && ownerKey(state.snapshot))) return;
    if(attachmentUI?.problem())return;
    const refs=attachmentUI?.refs()||[];
    const receipt = beginSend(state, requestID(),{allowRunning:client.production,attachments:refs,retryUserMessageID:retryAttachmentInput()});
    if (!receipt) return;
    if(refs.length||receipt.request.references?.length)attachmentAttempts.set(receipt.key,{requestID:receipt.request.request_id,cursor:state.snapshot.cursor});
    error = '';
    updateControls();
    confirmations.start(receipt);
  }
  async function cancel() {
    if (cancelPending || !state.snapshot?.running) return;
    cancelPending = true;
    updateControls();
    const target = {request_id: requestID(), boot_id: state.snapshot.boot_id, turn_id: state.snapshot.turn_id};
    const cancelEpoch = state.epoch;
    try { await client.cancel(target); }
    catch { if (cancelEpoch === state.epoch) error = t('cancelError'); }
    finally { cancelPending = false; if (!disposed) updateControls(); }
  }
  form.addEventListener('submit', event => { event.preventDefault(); if(!editor.mobile)submit(); });
  send.addEventListener('click', () => state.snapshot?.running&&!draft(state).trim()&&!attachmentUI?.items().length ? cancel() : submit());
  function updateControls() {
    const snapshot = state.snapshot;
    const pending = snapshot && state.pending.get(ownerKey(snapshot));
    const running = snapshot?.running;
    continuation?.update(snapshot,{connected:connection==='connected'&&!selectedAssistant?.offline&&!pending});
    const stopping=running&&(!client.production||(!draft(state).trim()&&!attachmentUI?.items().length));
    const label = stopping ? (cancelPending ? t('stopping') : t('stop')) : running?t('send.guidance'):t('send');
    send.replaceChildren(icon(stopping ? 'stop' : 'send'));
    send.title = label;
    send.setAttribute('aria-label', label);
    send.disabled = stopping ? selectedAssistant?.offline||cancelPending || connection !== 'connected' : selectedAssistant?.offline|| !snapshot || editorOwner !== ownerKey(snapshot) || pending || (!draft(state).trim()&&!attachmentUI?.items().length) || !!attachmentUI?.problem() || connection !== 'connected';
    attachmentUI?.update();
    updateReferenceDraft();
    input.disabled = !snapshot && !editorOwner;
    const visibleConnection=selectedAssistant?.offline?'offline':connection;
    const projection=status.cloneNode(false);
    projection.replaceChildren();
    projection.classList.toggle('has-error', Boolean(error || connectionError) || visibleConnection === 'offline');
    projection.classList.toggle('is-connected',!pending&&!error&&!connectionError&&visibleConnection==='connected');
    const replyOwnsConnection=state.snapshot?.running||state.sends.has(editorOwner);
    if(pending){
      projection.append(el('span','',t(pending.phase==='uncertain'?'send.unknown':'send.confirming')));
      if(pending.recoverable){const retry=el('button','text-button',t('send.checkRetry'));retry.type='button';retry.disabled=visibleConnection!=='connected';retry.onclick=()=>confirmations.recover(pending);projection.append(retry);}
    }
    if(error||!replyOwnsConnection)projection.append(el('span', '', error || connectionError || (visibleConnection === 'connected' ? t(client.production?'connected':'preview') : t(visibleConnection))));
    if (visibleConnection === 'offline'&&!replyOwnsConnection) {
      const retry = el('button', 'text-button', t('retry'));
      retry.type = 'button';
      retry.onclick = () => loadSnapshot(true);
      projection.append(retry);
    }
    reconcile(status,projection);
    renderMessages();
  }
  function saveNavigation(next,replace=false){const value={potRoute:next,potScope:client.scope?.()||{},potDirectory:directoryOpen};window.history[replace?'replaceState':'pushState'](value,'',location.href);}
  async function navigate(next,{replace=false,record=true}={}) {
    if (pages && !await pages.leave()) return;
    route = next;
    menuOpen = false;
    if (next === 'chat') directoryOpen = true;
    if(client.production&&record)saveNavigation(next,replace);
    renderShell();
    if(pages&&next==='chat')pages.permissions();if (pages && next !== 'chat') pages.show(next);
  }
  function navigation(inMenu) {
    const fragment = document.createDocumentFragment();
    for (const name of ['chat', 'tasks', 'machines', 'usage', 'settings']) {
      const update=name==='settings'?updates?.button(inMenu):null;if(update)fragment.append(update);
 const item = button(name, t(name), inMenu ? 'menu-destination' : `rail-button${name === 'settings'&&!update ? ' rail-bottom' : ''}`);
      if (name === 'chat') { item.replaceChildren(); const image = el('img', 'pot-icon'); image.src = new URL('../assets/PotCompanion.png', import.meta.url).href; image.alt = ''; item.append(image); }
      if (inMenu) item.append(el('span', '', t(name === 'chat' ? 'assistants' : name)));
      item.classList.toggle('is-selected', route === name);
      if (route === name) item.setAttribute('aria-current', 'page');
      item.onclick = () => navigate(name);
      fragment.append(item);
    }
    return fragment;
  }
  let preferenceControls;
  function renderPreferences() {
    if(client.production){preferences.classList.add('product-actions');const projection=preferences.cloneNode(false);const close=button('close',t('close'));close.onclick=()=>preferences.close();projection.append(close);const action=(label,run)=>{const choice=el('button','menu-destination',label);choice.type='button';choice.onclick=()=>{preferences.close();run();};projection.append(choice);};if(selectedAssistant){action(t('assistantSettings'),()=>{route='settings';pages.assistant(selectedAssistant);renderShell();});action(t('usage'),()=>{route='usage';pages.conversationUsage(selectedAssistant.name);renderShell();});}if(state.snapshot?.running){const captured={...client.scope()},target=state.snapshot.turn_id,paused=!!state.snapshot.turn?.paused;action(paused?(locale==='zh-CN'?'恢复回复':'Resume turn'):(locale==='zh-CN'?'暂停回复':'Pause turn'),async()=>{try{await client.controlTurn({user_message_id:target,action:paused?'resume':'pause'},captured);}catch(reason){error=reason.message;updateControls();}});}action(t('settings'),()=>navigate('settings'));reconcile(preferences,projection);return;}
    if (!preferenceControls) {
      const heading = el('div', 'preferences-heading');
      const title = el('h2');
      const close = button('close', t('close'));
      close.onclick = () => preferences.close();
      heading.append(title, close);
      const hint = el('p', 'hint');
      preferenceControls = {title, close, hint, fields: new Map()};
      preferences.append(heading, hint);
      for (const key of ['language', 'appearance']) {
        const label = el('label', 'preference-row');
        const text = el('span');
        const select = el('select');
        select.id = `preview-${key}`;
        label.htmlFor = select.id;
        select.onchange = () => {
          if (key === 'language') { locale = select.value; persist('pot-go-preview-locale', locale); }
          else { theme = select.value; persist('pot-go-preview-theme', theme); }
          renderShell(); renderMessages(); if(client.production) window.dispatchEvent(new Event('pot:locale'));
        };
        label.append(text, select);
        preferences.append(label);
        preferenceControls.fields.set(key, {text, select});
      }
    }
    const {title, close, hint, fields} = preferenceControls;
    title.textContent = t(client.production?'preferences':'more'); hint.textContent = client.production?'':t('notice');
    close.title = t('close'); close.setAttribute('aria-label', t('close'));
    for (const [key, choices, current] of [
      ['language', [['en', 'English'], ['zh-CN', '简体中文']], locale],
      ['appearance', ['system', 'light', 'dark'].map(value => [value, t(value)]), theme],
    ]) {
      const {text, select} = fields.get(key);
      text.textContent = t(key);
      select.setAttribute('aria-label', t(key));
      // Retain the focused native select through live theme/locale changes.
      for (const [index, [value, label]] of choices.entries()) {
        let option = select.options[index];
        if (!option) { option = el('option'); select.append(option); }
        option.value = value; option.textContent = label;
      }
      select.value = current;
    }
  }
  preferences.addEventListener('close', () => { preferencesOpen = false;notifications?.report(); });
  const shellRoots={rail,titlebar,menu};
  function renderShell() {
    const showingChat=route==='chat'&&!(media.matches&&directoryOpen);
    if(chatShown&&!showingChat)readingPositions.set(snapshotKey(),captureReadingPosition(history));
    if(!chatShown&&showingChat)pendingReadingPosition=readingPositions.get(snapshotKey())||pendingReadingPosition;
    chatShown=showingChat;
    queueMicrotask(()=>{if(!disposed)notifications?.report();});
    const rail=shellRoots.rail.cloneNode(false),titlebar=shellRoots.titlebar.cloneNode(false),menu=shellRoots.menu.cloneNode(false);
    markVisibleRead();
    if(route!=='chat')completions?.reset();
    window.potErrors?.setTranslator(t);
    document.documentElement.dataset.theme = theme;
    document.documentElement.lang = locale;
    const narrow = media.matches && surface !== 'tray';
    root.dataset.route = route;
    root.classList.toggle('show-directory', route === 'chat' && narrow && directoryOpen);
    pages?.renderApprovals();
    rail.replaceChildren(navigation(false));
    rail.setAttribute('aria-label', t('menu'));
    titlebar.replaceChildren();
    if (pages?.hasParent) { const back = button('back', t('parentBack')); back.onclick = () => pages.back(); titlebar.append(back); }
    if (surface !== 'tray'&&!pages?.hasParent) {
      const back = route === 'chat' && narrow && !directoryOpen;
      const leading = button(back ? 'back' : 'menu', t(back ? 'back' : 'menu'), 'icon-button narrow-navigation');
      leading.onclick = () => {
        if (back) { directoryOpen = true;if(client.production)saveNavigation('chat',true);renderShell(); }
        else { menuOpen = true; renderShell(); }
      };
      titlebar.append(leading);
      if(!narrow&&route==='chat'&&!client.production){const back=button('back',t('back'));back.onclick=()=>{directoryOpen=true;renderShell()};titlebar.append(back);}
    }
    const title = el('div', 'window-title');
    if (route === 'chat' && !(narrow && directoryOpen)) title.append(assistantAvatar(selectedAssistant,24));
    const titleText=route === 'chat' ? (narrow && directoryOpen ? t('assistants') : selectedAssistant?.name || 'Pot') : pages?.title || t(route);title.title=titleText;
    title.append(el('span', '', titleText));
    titlebar.append(title);
 if(narrow){const update=updates?.button();if(update){update.className="update-button icon-button";titlebar.append(update);}}
    if (surface === 'tray' && client.openMain) {
      const open = button('open', t('openMain'));
      open.onclick = async () => { try { await client.openMain(); } catch (reason) { error = reason.message; updateControls(); } };
      titlebar.append(open);
    }
    if (client.production && route === 'chat' && selectedAssistant) { const settings = button('settings', t('assistantSettings')); settings.onclick = () => {route = 'settings'; pages.assistant(selectedAssistant); renderShell();}; titlebar.append(settings); }
    if(!client.production){const cloud=el('span','icon-button');cloud.title=t('remote');cloud.append(icon('cloud'));titlebar.append(cloud);}
    if (route==='chat'&&state.snapshot?.usage) {const value=state.snapshot.usage,format=new Intl.NumberFormat(locale,{notation:'compact',maximumFractionDigits:1}),usage=el('span','title-meta token-meta'),cached=locale==='zh-CN'?'缓存输入':'Cached input';usage.append(icon('clock'),el('span','',`${format.format(value.input+value.output)} tokens${value.cache==null?'':` · ${cached} ${format.format(value.cache)}`}`));usage.title=`${locale==='zh-CN'?'输入（含缓存）':'Input (includes cache)'}: ${value.input} · ${locale==='zh-CN'?'输出':'Output'}: ${value.output}${value.cache==null?'':` · ${cached}: ${value.cache}`}`;titlebar.append(usage);}
    const more = button(client.production?'more':'settings', t(client.production ? 'preferences' : 'more'));
    more.setAttribute('aria-haspopup', 'dialog');
    more.onclick = () => { preferencesOpen = true; renderPreferences(); preferences.showModal(); };
    if(!client.production||route==='chat')titlebar.append(more);
    if(route==='tasks'&&pages?.taskOwner){const actions=button('more',locale==='zh-CN'?'任务操作':'Task actions');actions.setAttribute('aria-haspopup','dialog');actions.onclick=()=>pages.taskMenu();titlebar.append(actions);}
    if(!client.production&&titlebar.querySelector('.title-meta'))titlebar.insertBefore(more,titlebar.querySelector('.title-meta'));
    notice.textContent = client.production ? '' : t('notice');
    input.placeholder = t('composer');
    input.setAttribute('aria-label', t('composer'));
    history.setAttribute('aria-label', t('chat'));
    latest.title = t('latest');
    latest.setAttribute('aria-label', t('latest'));
    resourceNotices.hidden=resourceNotices.dataset.empty==='true'||!['chat','tasks','machines'].includes(route);history.hidden = composerArea.hidden = route !== 'chat';approvals.hidden=route!=='chat'&&!(route==='tasks'&&pages?.taskOwner);
    unavailable.hidden = route === 'chat';
    if (!client.production) unavailable.textContent = t('unavailable');
    // Search is window state and survives narrow conversation navigation/resizes.
    const nextDirectorySignature = JSON.stringify([locale,selectedAssistant?.space_id,selectedAssistant?.id,spaces,assistants,state.snapshot?.assistants,replies.records()]);
    if (nextDirectorySignature !== directorySignature || !directory.childElementCount) {
    directorySignature=nextDirectorySignature;
    const searchValue = directory.querySelector('input')?.value || '';
    const scrollTop = directory.querySelector('.assistant-list')?.scrollTop || 0;
    const nextDirectory=directory.cloneNode(false);
    const heading = el('div', 'directory-heading');
    heading.append(el('span', '', t('assistants')));
    const add = button('plus', t(client.production ? 'newAssistant' : 'preview'));
    add.disabled = !client.production;
    add.onclick = () => { route = 'settings'; pages.assistant(null); renderShell(); };
    heading.append(add);
    const search = el('input', 'assistant-search');
    search.type = 'search'; search.placeholder = t('search'); search.setAttribute('aria-label', t('search')); search.value = searchValue;
    const list = el('div', 'assistant-list');
    const items = client.production ? assistants : state.snapshot?.assistants || [{id:'main', name:'Pot', preview:t('preview')}];
    for (const item of items) {
      const selected = (!client.production&&item.id==='main') || selectedAssistant?.id === item.id && selectedAssistant?.space_id === item.space_id;
      const row = el('button', `assistant-row${selected ? ' is-selected' : ''}`); row.type = 'button';row.dataset.viewKey=JSON.stringify([item.space_id,item.id]);row.dataset.searchName=item.name.toLowerCase();
      row.append(assistantAvatar(item,32)); const labels = el('span', 'assistant-labels'); const name = el('span', 'assistant-name');const title=el('span','assistant-title',item.name);title.title=item.name;name.append(title);
      if(spaces.length>1){const location=el('span','assistant-location',item.space_name||t(item.space_kind==='remote'?'remote':'local'));location.title=location.textContent;name.append(location);}
      const unread=replies.unread(controlOf(item),item.id,item.reply_count||0);if(unread){const badge=el('span','unread-badge',unread>9?'9+':String(unread));badge.title=String(unread);badge.setAttribute('aria-label',`${unread} ${locale==='zh-CN'?'条未读回复':'unread replies'}`);name.append(badge);row.classList.add('has-unread');}
      const pending=Number(item.pending_approvals)||0;
      if(pending){row.classList.add('needs-approval');labels.append(name,el('span','assistant-approval-status',`${t('tool.awaitingApproval')} · ${pending}`));}
      else labels.append(name, el('span', 'assistant-preview', item.offline ? t('offline') : item.preview || ({failed:t('replyFailed'),needs_configuration:t('setupAI'),ready:t('connected')}[item.phase] || item.phase || '')));
      row.append(labels); row.onclick = () => client.production ? selectAssistant(item) : (directoryOpen = false, renderShell());
      list.append(row);
    }
    const empty = el('p', 'hint', t(items.length ? 'searchEmpty' : 'noAssistants'));
    const filter = search => {let count=0;const list=search.nextElementSibling;for(const row of list.querySelectorAll('.assistant-row')){row.hidden=!row.dataset.searchName.includes(search.value.toLowerCase());if(!row.hidden)count++;}list.querySelector('.hint').hidden=count>0;};
    search.oninput=event=>filter(event.currentTarget);list.append(empty);nextDirectory.append(heading,search,list);filter(search);reconcile(directory,nextDirectory);directory.querySelector('.assistant-list').scrollTop=scrollTop;
    }
    menu.replaceChildren();
    const menuHeading = el('div', 'mobile-menu-heading', 'Pot');
    const closeMenu = button('close', t('close'));
    closeMenu.onclick = () => { menuOpen = false; renderShell(); };
    menuHeading.append(closeMenu);
    const menuContent=el('div','mobile-menu-content');
    menuContent.append(menuHeading, navigation(true));menu.append(menuContent);
    menu.hidden = !menuOpen || !narrow;
    menu.setAttribute('aria-label', t('menu'));
    workspace.inert = directory.inert = titlebar.inert = !menu.hidden;
    reconcile(shellRoots.rail,rail);reconcile(shellRoots.titlebar,titlebar);reconcile(shellRoots.menu,menu);
    if(!menu.hidden&&!shellRoots.menu.contains(document.activeElement))shellRoots.menu.querySelector('.icon-button')?.focus({preventScroll:true});
    if (preferencesOpen) renderPreferences();
    updateControls();
    taskClock.refresh();
    requestAnimationFrame(() => { if (!disposed) editor.resize(); });
  }
  function selectedSequence(){const selection=getSelection();if(!selection?.rangeCount||selection.isCollapsed||!history.contains(selection.anchorNode))return null;const element=selection.anchorNode.nodeType===Node.ELEMENT_NODE?selection.anchorNode:selection.anchorNode.parentElement;return element?.closest('article.message')?.dataset.sequence||null;}
  function nearBottom() { return history.scrollHeight - history.clientHeight - history.scrollTop < 80; }
  function atBottom() { return history.scrollHeight - history.clientHeight - history.scrollTop <= 1; }
  let following=true,renderFrame=0,lastAnchor=null,resizeAnchor=false,lastScrollTop=0,upwardIntent=false;
  function scrollBottom() { following=true;upwardIntent=false;history.scrollTop = history.scrollHeight;lastScrollTop=history.scrollTop; latest.hidden = true; }
  function pauseFollowing(){following=false;pendingFollow=false;latest.hidden=false;}
  function loadEarlierNearTop(){
    if(!historyLoading&&!historyFailure&&history.scrollTop<Math.max(180,history.clientHeight)&&state.snapshot?.history?.has_more){
      pauseFollowing();readHistory({before:state.snapshot.history.before});
    }
  }
  function scrollUpIntent(event){
    if(event.defaultPrevented||!chatShown)return;
    // Let nested output panes consume their own scrolling first.
    for(let node=event.target instanceof Element?event.target:null;node&&node!==history;node=node.parentElement){
      if(node.scrollTop>0&&node.scrollHeight>node.clientHeight&&/^(auto|scroll)$/.test(getComputedStyle(node).overflowY))return;
    }
    if(history.scrollHeight<=history.clientHeight+1&&!state.snapshot?.history?.has_more&&!state.snapshot?.history?.has_newer)return;
    upwardIntent=true;pauseFollowing();loadEarlierNearTop();
  }
  history.addEventListener('wheel',event=>{
    if(!event.ctrlKey&&!event.metaKey&&event.deltaY<0&&Math.abs(event.deltaY)>Math.abs(event.deltaX))scrollUpIntent(event);
  },{passive:true});
  let touchY=null;
  history.addEventListener('touchstart',event=>{touchY=event.touches.length===1?event.touches[0].clientY:null;},{passive:true});
  history.addEventListener('touchmove',event=>{
    const nextY=event.touches.length===1?event.touches[0].clientY:null;
    if(touchY!==null&&nextY!==null&&nextY>touchY)scrollUpIntent(event);
    touchY=nextY;
  },{passive:true});
  history.addEventListener('keydown',event=>{
    if(event.ctrlKey||event.metaKey||event.altKey||event.target.closest('input,textarea,select,button,a,summary,[contenteditable]'))return;
    if(['ArrowUp','PageUp','Home'].includes(event.key)||event.key===' '&&event.shiftKey)scrollUpIntent(event);
  });
  history.addEventListener('scroll', () => {
    if(chatShown)lastTaskReadingPosition=captureReadingPosition(history);
    if(history.scrollTop<lastScrollTop-.5)pauseFollowing();
    else if(atBottom()){following=true;upwardIntent=false;}
    lastScrollTop=history.scrollTop;latest.hidden=following;
    lastAnchor=readingAnchor(history);state.uiAnchor=selectedSequence()||lastAnchor?.sequence||null;
    renderMessages();
    loadEarlierNearTop();
    if(!historyLoading&&!historyFailure&&nearBottom()&&state.snapshot?.history?.has_newer){pauseFollowing();readHistory({after:state.snapshot.history.after});}
  }, {passive: true});
  latest.onclick = async()=>{if(state.snapshot?.history?.has_newer)await readHistory({},{around:true});following=true;state.uiAnchor=null;renderMessages();requestAnimationFrame(scrollBottom);};
  historyStatus.onclick=()=>{pauseFollowing();if(historyFailure==='history.missing'){historyFailure='';renderMessages();}else if(!historyLoading&&state.snapshot?.history?.has_more)readHistory({before:state.snapshot.history.before});};
  const messageNodes = new Map(),toolMemory=new Map();
  const taskCache=new Map();
  const backgroundTaskView=createBackgroundTasks({locale:()=>locale,
    onOpen:task=>{const returnTo={route:'chat',scope:{...client.scope()}};route='tasks';pages.openTask(task,{returnTo});renderShell();},
    onToggle:change=>{pendingReadingPosition=captureReadingPosition(history,{ignoreToggle:!!change.expanded});pendingTaskToggle=change;pendingFollow=false;following=false;renderMessages();},
  });
  let backgroundTasks=null,backgroundTaskKey='';
  function refreshBackgroundTasks(){
    if(!client.production||!selectedAssistant)return Promise.resolve();
    const scope={...client.scope()},key=JSON.stringify([scope.space,scope.owner]);
    if(!backgroundTasks||key!==backgroundTaskKey){
      backgroundTasks?.close();backgroundTaskKey=key;
      backgroundTasks=createRefreshQueue(async()=>{
        const items=[],seen=new Set();let offset=0;
        do{
          if(seen.has(offset))throw Error('Invalid task pagination cursor');seen.add(offset);
          const value=await client.request(`/api/v1/tasks/query?scope=active&limit=50&offset=${offset}`,{...scope,signal:abort.signal});
          if(disposed||key!==JSON.stringify([client.scope().space,client.scope().owner]))return;
          items.push(...(value.items||[]));offset=value.next_offset;
        }while(offset!=null);
        taskCache.set(key,items.filter(task=>['queued','running','waiting','waiting_approval'].includes(task.phase)).map(task=>({...task,_space_id:scope.space,assistant_id:scope.owner})));
        pages.rememberTasks(taskCache.get(key));if(route==='chat')renderMessages();
      },{active:()=>!disposed&&key===JSON.stringify([client.scope().space,client.scope().owner])});
    }return backgroundTasks.run();
  }
  const windowHeights=new Map();
  const disposeMessage=record=>{record.childrenWindow?.close();record.attachmentNode?.dispose?.();record.row.remove();messageNodes.delete(record.row.dataset.windowKey);};
  const requestMessageRender=resize=>{resizeAnchor ||= !!resize;renderMessages();};
  let renderingSnapshot=null,renderingSending=null,renderingForest=null;
  const markdownFiles=scope=>({
    acquireFileImage:client.acquireFileImage?({task,path})=>client.acquireFileImage(task,path,scope):undefined,
    downloadFile:client.downloadFile?async({task,path})=>{try{await client.downloadFile(task,path,scope);}catch(reason){error=reason.message;updateControls();}}:undefined,
  });
  const renderMessage=(message,existing)=>{
    const snapshot=renderingSnapshot,sending=renderingSending,forest=renderingForest;
    let record = existing;
    if (!record) {
      const row = el('article', `message role-${message.role}`);
      const bubble = el('div', 'message-body');
      row.append(bubble);
      record = {row, bubble};
      messageNodes.set(message.viewKey, record);
    }
    const {row, bubble} = record;
    row.dataset.messageId=message.id;row.dataset.sequence=message.sequence||'';
    bubble.dataset.scrollAnchor=`message:${message.viewKey}`;
    if(message.role==='tool'&&message.tool){
      const scope=client.scope?.()||{},key=JSON.stringify([scope.space,scope.owner,message.id]);
      if(!record.tool){record.tool=createToolCall({memory:toolMemory,key,locale:()=>locale,onDecision:(approval,action)=>pages.decideApproval(approval,action),...markdownFiles(scope)});bubble.append(record.tool.element);}
      record.tool.update(message,{offline:connection==='offline'||disconnected,children:forest.byID.get(message.tool.turn_id+':'+message.tool.id)?.children.map(entry=>entry.message)||[]});
    }else if(message.role==='assistant'){
      record.reply ||= createReplyBubble(bubble);
      const scope=client.scope?.();
      record.reply.update(message,{t,...markdownFiles(scope),
        onSettings:async()=>{if(!await pages?.leave())return;route='settings';await pages.showProviders();renderShell();},
        onReconnect:()=>loadSnapshot(true),onRetrySend:submit,canRetrySend:connection==='connected'&&!pendingSend()&&draft(state)===sending?.request.text});
      record.text=message.text;record.role=message.role;
    }else if (record.text !== message.text || record.role !== message.role) {
      bubble.replaceChildren();
      if (message.role === 'tool') bubble.append(el('pre','',message.text));
      else if (message.role === 'system') bubble.textContent = message.text;
      else {const scope=client.scope?.(),prose=el('div','message-prose');prose.append(renderMarkdown(message.text,{...markdownFiles(scope)}));bubble.append(prose);}
      record.text = message.text; record.role = message.role;
      row.className = `message role-${message.role}`;
    }
    const source=message.role==='assistant'?bubble.querySelector('.reply-content'):message.role==='user'?bubble.querySelector('.message-prose'):null;
    if(source&&message.text&&!message.id.startsWith('pending:'))source.dataset.referenceSource=message.id;
    const referenceSignature=JSON.stringify([message.references||[],locale]);
    if(message.role==='user'&&(record.referenceSignature!==referenceSignature||record.referenceNode&&!record.referenceNode.isConnected)){
      record.referenceNode?.remove();record.referenceSignature=referenceSignature;
      if(message.references?.length){record.referenceNode=renderMessageReferences(message.references,{messages:snapshot?.messages||[],locale:()=>locale,onJump:jumpToMessage});bubble.prepend(record.referenceNode);}
    }
    const attachmentSignature=JSON.stringify([message.attachments||[],state.snapshot?.agent?.attachments]);
    if(message.role==='user'&&(record.attachmentSignature!==attachmentSignature||!record.attachmentNode?.isConnected)){
      record.attachmentNode?.dispose?.();record.attachmentNode?.remove();record.attachmentSignature=attachmentSignature;
      if(message.attachments?.length){record.attachmentNode=renderMessageAttachments(message.attachments,{client,caps:state.snapshot?.agent?.attachments,scope:{...client.scope()},locale:()=>locale,onError:reason=>{error=reason.message;updateControls();}});bubble.append(record.attachmentNode);}
    }
    if ((message.role === 'assistant'||message.role==='user') && message.text && !record.copy) {
      const copy = button('copy',t('copy'),'copy-message');
      copy.type = 'button';
      copyFeedback(copy,{text:()=>record.text,locale:()=>locale,translate,onError:reason=>{error=reason.message;updateControls();}});
      row.append(copy); record.copy = copy;
    }
    if (record.copy) {const label=t(record.copy.dataset.copied?'copied':'copy');record.copy.title=label;record.copy.setAttribute('aria-label',label);}
    if (message.role==='tool'&&!message.tool&&message.status === 'streaming' && !record.dot) {
      record.dot = el('span', 'activity-dot'); row.append(record.dot);
    } else if (message.status !== 'streaming' && record.dot) { record.dot.remove(); record.dot = null; }
    record.dot?.setAttribute('aria-label', t('working'));
    if(message.role==='user'&&message.steering&&message.status==='accepted'){
      if(!record.received){record.received=el('span','guidance-received');record.received.append(icon('clock'));row.append(record.received);}record.received.title=locale==='zh-CN'?'已接收':'Received';record.received.setAttribute('aria-label',record.received.title);
    }else if(record.received){record.received.remove();record.received=null;}

    if(record.tool){
      const entry=forest.byID.get(message.tool.turn_id+':'+message.tool.id),children=entry?.children.map(child=>child.message)||[];
      if(!record.childrenWindow){record.childrenWindow=createMessageWindow({scroller:history,host:record.tool.children,render:renderMessage,dispose:disposeMessage,requestRender:requestMessageRender,heights:windowHeights});record.tool.element.addEventListener('toggle',()=>renderMessages());}
      record.childrenWindow.set(record.tool.element.open?children:[]);
    }
    return record;
  };
  const messageWindow=createMessageWindow({scroller:history,host:windowHost,render:renderMessage,dispose:disposeMessage,requestRender:requestMessageRender,heights:windowHeights});
  const viewportObserver=new ResizeObserver(()=>requestMessageRender(true));viewportObserver.observe(history);
  function renderMessages(){if(disposed||renderFrame)return;renderFrame=requestAnimationFrame(()=>{renderFrame=0;renderMessagesNow();});}
  function renderMessagesNow() {
    if(!chatShown)return;
    const follow=following||pendingFollow,resized=resizeAnchor;
    const taskReading=pendingReadingPosition||(resized&&lastTaskReadingPosition)||captureReadingPosition(history);
    const anchor=resizeAnchor?lastAnchor:readingAnchor(history);resizeAnchor=false;
    const snapshot=state.snapshot||(client.production&&selectedAssistant?cachedSnapshots.get(snapshotKey(selectedAssistant.space_id||'',selectedAssistant.id)):null);
    const sending=state.sends.get(editorOwner||(snapshot&&ownerKey(snapshot)));
    const activeTurn=snapshot?.running?(snapshot.messages.find(message=>message.id===snapshot.turn_id)?.request_id||snapshot.turn_id):null;
    const visibleMessages=projectScriptRuns(projectChatReplies(snapshot,sending,{offline:connection==='offline'||disconnected||selectedAssistant?.offline}),{activeTurn});
    const forest=toolForest(visibleMessages);
    renderingSnapshot=snapshot;renderingSending=sending;renderingForest=forest;
    const roots=visibleMessages.filter(message=>!message.tool?.parent_id||!forest.byID.has(message.tool.turn_id+':'+message.tool.parent_id));
    messageWindow.set(roots,{anchor:anchor?.id});
    historyStatus.hidden=!client.history||!snapshot;
    historyStatus.disabled=historyLoading||!historyFailure&&!snapshot?.history?.has_more;
    historyStatus.textContent=t(historyLoading?'history.loading':historyFailure|| (snapshot?.history?.has_more?'history.earlier':'history.start'));
    const tasks=client.production?(taskCache.get(JSON.stringify([client.scope().space,client.scope().owner]))||[]):[];
    const tasksChanged=client.production?backgroundTaskView.update(snapshotKey(),tasks,(id,phase)=>pages.taskNeedsApproval(id,phase)):false;
    // The task region stays outside recycled message rows and shares the chat scroller.
    if(client.production&&tasks.length){
      if(backgroundTaskView.element.parentElement!==messageColumn)messageColumn.insertBefore(backgroundTaskView.element,chatApprovals);
    }else backgroundTaskView.element.remove();
    if(pendingTaskToggle&&!pendingTaskToggle.expanded&&taskReading.anchors[0]?.key.startsWith('task:')){
      const retained=new Set([...backgroundTaskView.element.querySelectorAll('[data-scroll-anchor]')].map(node=>node.dataset.scrollAnchor));
      if(!retained.has(taskReading.anchors[0].key))taskReading.anchors=[{key:'background-tasks-heading',offset:0}];
    }
    const restoreTasks=pendingReadingPosition||(tasksChanged||resized&&taskReading.anchors.some(item=>item.key.startsWith('task:'))?taskReading:null);
    pendingTaskToggle=null;
    taskClock.refresh();
    if (snapshot && !visibleMessages.length) {
      if(!messageColumn.querySelector('.empty-chat'))messageColumn.append(el('p', 'empty-chat', t('first')));
    }else if(visibleMessages.length){
      messageColumn.querySelector('.empty-chat')?.remove();
    }
    pages?.renderApprovals();
    if(!chatApprovals.hidden&&locateApprovals===JSON.stringify(client.scope?.())){locateApprovals='';requestAnimationFrame(()=>{if(!disposed&&route==='chat')chatApprovals.scrollIntoView({block:'nearest',behavior:reducedMotion.matches?'instant':'smooth'});});}
    if(restoreTasks&&chatShown&&!pendingFollow){following=false;restoreReadingPosition(history,restoreTasks);pendingReadingPosition=null;latest.hidden=nearBottom();}
    else if(follow)scrollBottom();else{restoreReadingAnchor(history,anchor);latest.hidden=false;}
    if(snapshot&&!snapshot.history?.has_more&&!snapshot.history?.has_newer&&history.scrollHeight<=history.clientHeight+1)scrollBottom();
    lastTaskReadingPosition=captureReadingPosition(history);
    lastScrollTop=history.scrollTop;
    lastAnchor=readingAnchor(history);if(!following)state.uiAnchor=selectedSequence()||lastAnchor?.sequence||null;
    if(following)state.uiAnchor=null;
    if(pendingFollow&&!followFrame)followFrame=requestAnimationFrame(()=>{followFrame=0;if(!disposed&&chatShown&&pendingFollow){scrollBottom();pendingFollow=false;}});
  }
  function cacheSnapshot(snapshot){const key=snapshotKey();cachedSnapshots.delete(key);cachedSnapshots.set(key,{...snapshot,kind:'reset',base_cursor:0});while(cachedSnapshots.size>8)cachedSnapshots.delete(cachedSnapshots.keys().next().value);while(positions.size>32)positions.delete(positions.keys().next().value);while(toolMemory.size>1280)toolMemory.delete(toolMemory.keys().next().value);}
  function restoreSavedPosition(position){
    const saved=readingPositions.get(snapshotKey());
    if(saved?.anchors.some(item=>item.key.startsWith('task:')||item.key.startsWith('background-tasks'))){following=false;renderMessagesNow();restoreReadingPosition(history,saved);lastAnchor=readingAnchor(history);latest.hidden=nearBottom();return;}
    following=position.following;state.uiAnchor=position.anchor?.sequence||null;
    renderMessagesNow();
    if(following)scrollBottom();else if(position.anchor){messageWindow.seek(position.anchor.id);renderMessagesNow();restoreReadingAnchor(history,position.anchor);lastAnchor=readingAnchor(history);}else history.scrollTop=position.top;
    latest.hidden=following;
  }
  function pendingSend(){return state.snapshot&&state.pending.has(ownerKey(state.snapshot));}
  function onSnapshot(snapshot, receivedEpoch = epoch) {
    if (disposed || receivedEpoch !== state.epoch) return;
    if(restoredSnapshot&&validSnapshot(snapshot)&&state.snapshot?.boot_id!==snapshot.boot_id){state.snapshot=null;pages?.clearApprovals();}
    if(state.snapshot&&validSnapshot(snapshot)&&ownerKey(snapshot)===ownerKey(state.snapshot)&&snapshot.boot_id!==state.snapshot.boot_id){
      cachedSnapshots.delete(snapshotKey());client.resources?.invalidate([],{space:client.scope().space,boot:snapshot.boot_id});pages?.refresh([],{space:client.scope().space,boot:snapshot.boot_id});
      error='';return loadSnapshot(true);
    }
    if (state.snapshot && validSnapshot(snapshot) && ownerKey(snapshot) !== ownerKey(state.snapshot)) {
      connection = 'offline'; error = t('identityChanged'); updateControls(); return;
    }
    // A return to the bottom can precede its scroll event and the next frame.
    const wasFollowing=chatShown&&(following&&nearBottom()||!upwardIntent&&atBottom());
    const readingBeforeReset=snapshot.kind==='reset'&&!following?readingAnchor(history):null;
    const initialSnapshot = !state.snapshot;
    if(validSnapshot(snapshot)){awaitingSnapshot=false;restoredSnapshot=false;}
    const pendingReceipt=state.pending.get(ownerKey(snapshot));
    let accepted;try{accepted=acceptSnapshot(state,snapshot,receivedEpoch);}catch(reason){if(reason.message==='conversation_gap'){loadSnapshot(true);return;}throw reason;}
    if (!accepted){if(state.snapshot&&validSnapshot(snapshot)&&state.snapshot.cursor===snapshot.cursor){connection='connected';updateControls();}return;}
    if(wasFollowing){following=true;pendingFollow=true;}
    if(pendingReceipt && !state.pending.has(pendingReceipt.key))confirmations.confirmed(pendingReceipt);
    connection = 'connected';
    connectionError = '';
    snapshot=state.snapshot;
    if(client.production)cacheSnapshot(snapshot);
    const restorePosition = editorOwner !== ownerKey(snapshot) ? positions.get(ownerKey(snapshot)) : undefined;
    syncEditor();
    restoreFailedAttachments();
    if(markVisibleRead())renderShell();
    renderMessages(); if (restorePosition !== undefined) {const key=ownerKey(snapshot);requestAnimationFrame(()=>{if(!disposed&&editorOwner===key)restoreSavedPosition(restorePosition);});} updateControls();
    if(client.production)pages.syncApprovals();
    if(readingBeforeReset&&!snapshot.messages.some(message=>message.id===readingBeforeReset.id&&!message.context_only)){
      readHistory({around:readingBeforeReset.id},{around:true});
    }
    if(initialSnapshot){if(client.production)pages.permissions();renderShell();}
  }
  const abort = new AbortController();
  async function loadSnapshot(reattach = false) {
    if (disposed) return;
    if (reattach) {historyGeneration++;historyLoading=false;epoch = beginSubscription(state,{retainPending:true});disconnected=false;return attach();}
    const receivedEpoch = epoch;
    connection = 'connecting'; error = ''; connectionError = ''; updateControls();
    try {
      const value = await client.snapshot({signal: abort.signal});
      if (disposed || receivedEpoch !== state.epoch) return;
      if (!validSnapshot(value)) throw Error('Invalid preview snapshot');
      onSnapshot(value, receivedEpoch);
    } catch (reason) {
      if (!disposed && receivedEpoch === state.epoch) { connection = 'offline'; error = reason.message; updateControls(); }
    }
  }
  function attach() {
    unsubscribe();
    const receivedEpoch = epoch;awaitingSnapshot=!!client.production;connection='connecting';updateControls();
    unsubscribe = client.subscribe(value => onSnapshot(value, receivedEpoch), (value, reason) => {
      if (disposed || receivedEpoch !== state.epoch) return;
      if (value === 'offline') {disconnected = true;connectionError = reason?.message || t('streamDisconnected');}
      if (value === 'connected') connectionError = '';
      if (value === 'connected' && disconnected) { disconnected = false; loadSnapshot(true); }
      else { connection = value==='connected'&&awaitingSnapshot?'connecting':value; updateControls(); }
    });
    if(!client.production)return loadSnapshot();
    // Both SSE and the desktop subscription send an authoritative initial snapshot.
    return Promise.resolve();
  }
  async function selectAssistant(item, initial = false,record=true) {
    completions?.reset();
    if (editor.composing) { input.blur(); if (editor.composing) return; }
    if (pages && !await pages.leave()) return;
    if (state.snapshot) {positions.set(ownerKey(state.snapshot),{top:history.scrollTop,anchor:readingAnchor(history),following});if(chatShown)readingPositions.set(snapshotKey(),captureReadingPosition(history));}
    pages?.clearApprovals();locateApprovals=item.pending_approvals?JSON.stringify({owner:item.id,space:item.space_id||''}):'';selectedAssistant = item; client.setScope({owner:item.id, space:item.space_id || ''});updates?.refresh(client.scope());
    epoch = beginSubscription(state); editorOwner = null; route = 'chat'; if(!initial)directoryOpen = false;
    pendingReadingPosition=readingPositions.get(snapshotKey())||null;pendingFollow=false;
    messageWindow.set([]);windowHost.replaceChildren();messageColumn.replaceChildren(historyStatus,windowHost,chatApprovals);messageNodes.clear();windowHeights.clear();following=true;upwardIntent=false;lastScrollTop=0;lastAnchor=null;historyGeneration++;historyLoading=false;historyFailure='';state.uiAnchor=null;error = ''; connectionError = ''; disconnected = false;
    const cached=cachedSnapshots.get(snapshotKey(item.space_id||'',item.id));
    restoredSnapshot=!!cached;connection='connecting';
    if(cached){acceptSnapshot(state,cached,epoch);syncEditor();renderMessages();const top=positions.get(ownerKey(cached));if(top!==undefined)requestAnimationFrame(()=>{if(!disposed&&editorOwner===ownerKey(cached)){restoreSavedPosition(top);}});}
    if(client.production&&record)saveNavigation('chat',initial);
    renderShell();attach();pages?.permissions();refreshBackgroundTasks();
  }
  const stopResources=client.resources?.subscribe((meta,reason)=>{if(disposed||!['/api/v1/spaces','/api/v1/assistants'].includes(meta.path))return;if(reason){error=translate(locale,'loading.stale');updateControls();return;}if(error===translate(locale,'loading.stale'))error='';loadDirectory();});
  const directoryQueue=createRefreshQueue(()=>readDirectory(),{active:()=>!disposed});
  function loadDirectory(){return directoryQueue.run();}
  async function readDirectory() {
    if (!client.production) return;
    const notificationEpoch=notifications?.epoch;
    const ticket = ++directoryEpoch,previousSpaces=JSON.stringify(spaces),previousOwners=JSON.stringify(assistants.map(item=>[item.space_id,item.id,item.name,item.offline]));
    try {
      const catalog = await (client.read||client.request)('/api/v1/spaces', {owner:'', space:'',signal:abort.signal});
      if (ticket !== directoryEpoch || disposed) return;
      spaces = catalog.items || [];client.spaces=spaces;
      if (!spaces.length) throw Error('No serving Space');
      if(!selectedAssistant){client.setScope({space:catalog.active_space_id||spaces[0].id,owner:''});updates?.refresh(client.scope());}
      const projectAssistants=(value,space,offline)=>{let items=value;if(typeof items==='string'){try{items=JSON.parse(items);}catch{items=[];}}return (items?.items||items||[]).map(item=>({...item,control_id:space.control_id||space.id,space_id:space.id,space_name:space.name,space_kind:space.kind,offline}));};
      const results = await Promise.allSettled(spaces.map(async space => {
        const items = await (client.read||client.request)('/api/v1/assistants', {owner:'', space:space.id,signal:abort.signal});
        return projectAssistants(items,space,space.connected===false);
      }));
      if (ticket !== directoryEpoch || disposed) return;
      assistants = results.flatMap((result, index) => result.status === 'fulfilled' ? result.value : (()=>{const space=spaces[index],previous=assistants.filter(item=>item.space_id===space.id);return previous.length?previous.map(item=>({...item,offline:true})):projectAssistants(space.assistants||[],space,true);})());
      client.assistants=assistants;
      const changed=[];if(previousSpaces!==JSON.stringify(spaces))changed.push('spaces');if(previousOwners!==JSON.stringify(assistants.map(item=>[item.space_id,item.id,item.name,item.offline])))changed.push('assistants');if(changed.length)pages.refresh(changed);
      for(const item of assistants){const arrival=replies.observe(controlOf(item),item.id,item.reply_id||'',item.reply_count||0,!item.offline);if(arrival.arrived&&route==='chat'&&document.visibilityState!=='hidden'&&!reducedMotion.matches){reactions.set(replyKey(controlOf(item),item.id),performance.now());setTimeout(()=>{if(!disposed){directorySignature='';renderShell();}},650);}}saveReads();
      if (results.some(result => result.status === 'rejected')) error = t('partialOffline');else if(error===t('partialOffline'))error='';
      if (selectedAssistant){selectedAssistant = assistants.find(item => item.id===selectedAssistant.id&&item.space_id===selectedAssistant.space_id) || selectedAssistant;if(selectedAssistant.offline)connection='offline';}
      renderShell();
      if(!assistants.length){
        const space=spaces.find(value=>value.id===client.scope().space)||spaces[0];
        const available=results.every(result=>result.status==='fulfilled')&&space.connected!==false;
        if(available&&!pages.editing){
          const result=await client.request('/api/v1/assistants/bootstrap',{owner:'',space:space.id,method:'POST',body:{locale}});
          if(ticket!==directoryEpoch||disposed)return;
          if(result.assistant){
            const items=await client.request('/api/v1/assistants',{owner:'',space:space.id});
            if(ticket!==directoryEpoch||disposed)return;
            assistants=projectAssistants(items,space,false);client.assistants=assistants;directoryOpen=false;renderShell();
          }
        }
        if(!assistants.length){directoryOpen=false;renderShell();const empty=el('div','empty-chat');empty.append(el('h2','',t('welcomeTitle')),el('p','hint',available?t('welcomeSetup'):t('partialOffline')));const setup=el('button','control primary',t(available?'setupAI':'retry'));setup.onclick=async()=>{if(!available){loadDirectory();return;}setup.disabled=true;try{await navigate('settings');await pages.setupAI();}catch(reason){error=reason.message;updateControls();}finally{setup.disabled=false;}};empty.append(setup);messageColumn.replaceChildren(empty);}
      }
      mainReady();
      if(pendingSelection){const chosen=assistants.find(item=>item.id===pendingSelection.id&&item.space_id===pendingSelection.space_id);pendingSelection=null;if(chosen){await selectAssistant(chosen);notifications?.directory({epoch:notificationEpoch});return;}}
      if (!selectedAssistant && assistants.length && !pages.editing) {
        const requested=params.get('assistant_id');const requestedSpace=params.get('space_id');
        const selected=requested?assistants.find(item=>item.id===requested&&(!requestedSpace||item.space_id===requestedSpace)):assistants[0];
        if(!selected){error=t('missingAssistant');connection='offline';updateControls();notifications?.directory({epoch:notificationEpoch});return;}
        await selectAssistant(selected,!requested);
      }
      notifications?.directory({epoch:notificationEpoch});
    } catch (reason) { if (ticket===directoryEpoch&&!disposed) {error = reason.message; connection='offline'; updateControls();if(!assistants.length){const empty=el('div','empty-chat',reason.message);const retry=el('button','control',t('retry'));retry.onclick=()=>loadDirectory();empty.append(document.createElement('br'),retry);messageColumn.replaceChildren(empty);}} }
  }
  const preferencesChanged = event => {
    if(event.detail.key==='pot-go-preview-locale') locale=event.detail.value;
    if(event.detail.key==='pot-go-preview-theme') theme=event.detail.value;
    renderShell();renderMessages();window.dispatchEvent(new Event('pot:locale'));
  };
  window.addEventListener('pot:preference',preferencesChanged);
  const popNavigation=async event=>{
    if(await pages?.handleHistory(event))return;
    if(!client.production)return;restoringHistory=true;
    try{
      const value=event.state||{},scope=value.potScope||{};
      if(((value.potRoute||'chat')==='chat'||value.potUsage&&!value.potUsage.returnTask)&&scope.owner&&(!selectedAssistant||selectedAssistant.id!==scope.owner||selectedAssistant.space_id!==scope.space)){const item=assistants.find(item=>item.id===scope.owner&&item.space_id===scope.space);if(!item){error=t('missingAssistant');updateControls();return;}await selectAssistant(item,false,false);}
      if(value.potResource){route='settings';await pages.openResources(false);return;}
      if(value.potSchedule){route='tasks';await pages.openSchedule(value.potSchedule,false);return;}
      if(value.potTask){route='tasks';await pages.openTask(value.potTask,{recordHistory:false,returnTo:value.potTaskReturn});return;}
      if(value.potUsage){route='usage';await pages.openUsage(value.potUsage,false);return;}
      await navigate(value.potRoute||'chat',{record:false});if((value.potRoute||'chat')==='chat')directoryOpen=!!value.potDirectory;
    }finally{restoringHistory=false;if(!disposed)renderShell();}
  };
  window.addEventListener('popstate',popNavigation);
  const keydown = event => {
    if((event.metaKey||event.ctrlKey)&&event.key===','){event.preventDefault();navigate('settings');return;}
    if((event.metaKey||event.ctrlKey)&&event.key.toLowerCase()==='d'){event.preventDefault();navigate('machines');return;}
    if (event.key === 'Escape' && menuOpen) { menuOpen = false; renderShell(); }
    if (event.metaKey && event.key === '[' && pages?.hasParent) {event.preventDefault(); pages.back(); return;}
    if (event.metaKey && event.key === '[' && media.matches && !directoryOpen) { event.preventDefault(); directoryOpen = true; renderShell(); }
  };
  let swipe = null,settling=false;
  const clearSwipe=()=>{menu.style.transform='';menu.inert=false;workspace.style.transform='';titlebar.style.transform='';root.classList.remove('is-edge-back');};
  const pointerDown = event => {
    if(!media.matches || surface==='tray' || event.pointerType==='mouse'||settling||preferences.open)return;
    if(!menuOpen&&event.clientX>root.getBoundingClientRect().left+parseFloat(getComputedStyle(root).paddingLeft)+24)return;
    swipe={id:event.pointerId,x:event.clientX,y:event.clientY,lastX:event.clientX,at:event.timeStamp,velocity:0,mode:menuOpen?'close':pages?.hasParent||route==='chat'&&!directoryOpen?'back':'open',active:false};
  };
  const pointerMove = event => {
    if(!swipe||swipe.id!==event.pointerId)return;
    const dx=event.clientX-swipe.x,dy=event.clientY-swipe.y;
    if(!swipe.active&&Math.abs(dy)>Math.abs(dx)&&Math.abs(dy)>10){swipe=null;return;}
    if(!swipe.active&&(swipe.mode==='close'?dx>=0:dx<=0))return;
    if(Math.abs(dx)>12&&Math.abs(dx)>Math.abs(dy))swipe.active=true;
    if(!swipe.active)return;
    const elapsed=event.timeStamp-swipe.at;if(elapsed>0)swipe.velocity=(event.clientX-swipe.lastX)/elapsed;
    swipe.at=event.timeStamp;swipe.lastX=event.clientX;
    const width=root.clientWidth,travel=Math.min(width,Math.max(0,swipe.mode==='close'?-dx:dx));
    swipe.travel=travel;
    if(swipe.mode==='back'){
      root.classList.toggle('is-edge-back',route==='chat'&&!pages?.hasParent);
      workspace.style.transform=titlebar.style.transform=`translateX(${travel}px)`;
    }else{menu.hidden=false;menu.inert=swipe.mode==='open';menu.style.transform=`translateX(${swipe.mode==='open'?travel-width:-travel}px)`;}
    event.preventDefault();
  };
  const finishSwipe=async(event,cancel=false)=>{
    if(!swipe||swipe.id!==event.pointerId)return;
    const gesture=swipe;swipe=null;
    if(!gesture.active){clearSwipe();return;}
    const width=root.clientWidth,travel=gesture.travel||0,direction=gesture.mode==='close'?-1:1,velocity=gesture.velocity*direction;
    const commit=!cancel&&velocity>=-.15&&(travel>Math.min(width/4,100)||travel>24&&velocity>.45);
    const targets=gesture.mode==='back'?[workspace,titlebar]:[menu];
    const end=gesture.mode==='back'?(commit?width:0):gesture.mode==='open'?(commit?0:-width):(commit?-width:0);
    settling=true;
    await Promise.all(targets.map(target=>settleMotion(target,{transform:target.style.transform},{transform:`translateX(${end}px)`})));
    if(disposed)return;
    if(commit){if(gesture.mode==='back'){if(pages?.hasParent)await pages.back();else directoryOpen=true;}else menuOpen=gesture.mode==='open';}
    clearSwipe();settling=false;renderShell();
  };
  const pointerUp=event=>{finishSwipe(event).catch(reason=>{clearSwipe();settling=false;error=reason.message;updateControls();});};
  const pointerCancel=event=>{finishSwipe(event,true).catch(()=>{clearSwipe();settling=false;});};
  root.addEventListener('pointerdown',pointerDown,{passive:true});root.addEventListener('pointermove',pointerMove,{passive:false});root.addEventListener('pointerup',pointerUp);root.addEventListener('pointercancel',pointerCancel);
  const mediaChanged = () => { menuOpen = false; renderShell(); };
  document.addEventListener('keydown', keydown);
  media.addEventListener('change', mediaChanged);
  renderShell();editor.resize();
  function attachChanges(){
    stopChanges();stopChanges=client.changes((topics,source={})=>{
      const keys=Array.isArray(topics)?topics:Object.keys(topics||{});
      client.resources?.invalidate(keys,source);
 if(relevantChange(keys,['updates','spaces','runtime'],client.scope().space,source))updates?.refresh(client.scope());
      if(source.boot){for(const [key,value]of cachedSnapshots)if((!source.space||JSON.parse(key)[1]===source.space)&&value.boot_id!==source.boot)cachedSnapshots.delete(key);if(state.snapshot&&(!source.space||source.space===client.scope().space)&&state.snapshot.boot_id!==source.boot)loadSnapshot(true);}
      if(source.reset){notifications?.refresh({reset:true,space:source.space});loadDirectory();}
      else if(relevantChange(keys,['task-outcomes','permissions']))notifications?.refresh({topics:keys,space:source.space});
      if(relevantChange(keys,['spaces','assistants','assistant-mail','conversation','runtime','permissions']))loadDirectory();
      if(relevantChange(keys,['tasks','runner'],client.scope().space,source))refreshBackgroundTasks();
      pages.refresh(keys,source);if(relevantChange(keys,['permissions','spaces','runtime'],pages.approvalSpace,source))pages.permissions();
    },{space:''});
  }
  if(client.production){attachChanges();loadDirectory();updates?.refresh(client.scope());}else attach();
  const stopResume=bindResumeRecovery(async()=>{
    if(disposed)return;
    const snapshot=selectedAssistant||!client.production?loadSnapshot(true):Promise.resolve();
    if(client.production){attachChanges();pages.refresh();pages.permissions();refreshBackgroundTasks();}
    await Promise.all([snapshot,loadDirectory()]);
  });
  return () => {
    disposed = true;updates?.close();confirmations.close();cancelAnimationFrame(renderFrame);cancelAnimationFrame(followFrame);viewportObserver.disconnect();messageWindow.close();stopResources?.();client.resources?.clear();stopResume();taskClock.dispose();onboarding?.dispose();notifications?.dispose();directoryQueue.close();backgroundTasks?.close();directoryEpoch++; abort.abort(); unsubscribe(); stopChanges(); pages?.dispose(); editor.destroy();
    stopMessageSelection();completions?.dispose();attachmentUI?.destroy();client.releaseAttachments?.();
    stopRead();reducedMotion.removeEventListener('change',reducedChanged);window.removeEventListener('focus',focusRead);document.removeEventListener('visibilitychange',focusRead);window.removeEventListener('storage',readStorage);
    root.removeEventListener('pointerdown',pointerDown);root.removeEventListener('pointermove',pointerMove);root.removeEventListener('pointerup',pointerUp);root.removeEventListener('pointercancel',pointerCancel);
    window.removeEventListener('pot:preference',preferencesChanged);
    document.removeEventListener('keydown', keydown);window.removeEventListener('popstate',popNavigation);
    media.removeEventListener('change', mediaChanged);
    stopViewport();
    root.replaceChildren();
  };
}
