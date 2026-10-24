import {machinePresentation} from '../state/machine-identity.mjs';
import {createCompaction} from './compaction.mjs';
import {projectLinkedAssistants,supportsCapability} from '../state/agent-links.mjs';
import {agentSource} from './connection-visuals.mjs';
import {createUpdates} from './updates.mjs';
import {mergeHistoryPage} from '../state/chat-history.mjs';
import {createChatCache} from '../state/chat-cache.mjs';
import {createChatSync} from '../state/chat-sync.mjs';
import {createMessageWindow} from './message-window.mjs';
import {createReadingCursor} from './reading-cursor.mjs';
import {notificationOnboarding} from './notification-onboarding.mjs';
import {createSendConfirmation} from '../state/send-confirmation.mjs';
import {bindMessageSelection,renderMessageReferences,referenceProblem} from './message-references.mjs';
import {createSystemNotifications} from '../adapters/notifications.mjs';
import {createToolCall} from './tool-call.mjs';
import {messageIdentities} from '../state/message-identities.mjs';
import {toolForest} from '../state/tool-calls.mjs';
import {projectScriptRuns} from '../state/script-runs.mjs';
import {avatar} from './avatar.mjs';
import {assistantActivity,assistantActivityIndicator} from './assistant-activity.mjs';
import {icon} from './icons.mjs';
import {navigationIcon} from './navigation-icons.mjs';
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
import {bindResumeRecovery} from '../state/resume.mjs';
import {bindViewport} from './viewport.mjs';
import {createAssistantCreation} from '../state/assistant-creation.mjs';
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
  let viewSurface=surface;
  if(client.production&&!client.desktop){
    let tabID;try{tabID=sessionStorage.getItem('pot-chat-view-id');if(!tabID){tabID=crypto.randomUUID();sessionStorage.setItem('pot-chat-view-id',tabID);}}catch{tabID=crypto.randomUUID();}
    viewSurface='browser:'+tabID;
  }
  const params = new URLSearchParams(location.search);
  let locale = params.get('locale') || stored('pot-go-preview-locale', navigator.language.startsWith('zh') ? 'zh-CN' : 'en');
  if (!messages[locale]) locale = 'en';
  client.uiLocale=()=>locale;
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
  let error = '',chatCacheFailed=false;
  let connectionError = '';
  let directoryOpen = !params.has('conversation');
  let directoryCollapsed = false;
  let cancelPending = false;
  let editorOwner = null;
  let menuOpen = false;
  let preferencesOpen = false,restoringHistory=false;
  let assistants = [], spaces = [], selectedAssistant = null, pendingSelection = null;
  let unsubscribe = () => {}, stopChanges = () => {};
  let directoryEpoch = 0, directorySignature = '';
  let creationStarting=false,creationFailed=false;
  const assistantCreation=createAssistantCreation({request:(...args)=>client.request(...args),locale:()=>locale,changed:()=>{if(!disposed)renderShell();}});
  const chatCache=client.production?createChatCache({name:client.chatCacheName||'pot-chat-cache'}):null;
  let activeView=null,viewWrite=Promise.resolve(),viewTimer,chatSync=null,selectionGeneration=0;
  let chatPreparing=false,revealFrame=0;
  let chatShown=false,pendingTaskToggle=null;
  let chatPosition=null,chatNavigation=0;
  let awaitingSnapshot=false;
  const snapshotKey=(space=client.scope?.().space||'',owner=client.scope?.().owner||'')=>JSON.stringify([(client.spaces||[]).find(item=>item.id===space)?.control_id||client.runtime?.runtime_id||'',space,owner]);
  const t = key => translate(locale,key);
  root.replaceChildren();
  root.className = `pot-app surface-${surface === 'tray' ? 'tray' : 'main'}`;
  const rail = el('nav', 'feature-rail');
  const titlebar = el('header', 'titlebar');
  const directory = el('aside', 'assistant-directory');
  directory.id = 'assistant-directory';
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
  composerArea.append(latest, form);
  const continuation=client.production?createTurnContinuation({client,locale:()=>locale}):null;
  if(continuation)composerArea.prepend(continuation.root);
  const unavailable = el('div', client.production ? 'page-host' : 'unavailable');
  workspace.append(notice, notificationError, approvals, resourceNotices, history, composerArea, unavailable);
  const menu = el('nav', 'mobile-menu');
  menu.hidden = true;
  const preferences = el('dialog', 'preferences');
  const assistantProfile = el('div', 'assistant-profile');
  assistantProfile.id = 'assistant-profile';
  assistantProfile.setAttribute('popover', 'auto');
  assistantProfile.setAttribute('role', 'dialog');
  root.append(rail, titlebar, directory, workspace, menu, preferences, assistantProfile);
  chatApprovals.dataset.scrollAnchor='chat-approvals';
  const reading=createReadingCursor(history,{
    active:()=>chatShown&&!disposed,
    requestRender:()=>renderMessages(true),
    changed:()=>{latest.hidden=reading.following;state.uiAnchor=reading.following?null:selectedSequence()||reading.anchor?.sequence||null;scheduleViewSave();},
    interact:()=>cancelChatPosition(),
    upward:()=>loadEarlierNearTop(),
    downward:()=>loadNewerNearBottom(),
    canReadEarlier:()=>!!(state.snapshot?.history?.has_more||state.snapshot?.history?.has_newer),
    scrolled:delta=>{
      const now=performance.now();
      historySpeed=now-historyScrollTime<200?Math.abs(delta)/Math.max(1,now-historyScrollTime):0;
      historyScrollTime=now;
      if(delta<0)loadEarlierNearTop();
      if(delta>0)loadNewerNearBottom();
    },
  });
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
 const pages = client.production ? createPages({createLocalAssistant:createAssistant,chatVisible:()=>route==='chat'&&!root.classList.contains('show-directory'),onApprovalsChanged:()=>renderMessages(),client, host: unavailable, approvalHost: approvals, chatApprovalHost:chatApprovals, locale: () => locale, notify: value => {error = value; updateControls();}, changed: async value => {if(value?.assistant)pendingSelection=value.assistant;const removed=value?.deletedAssistant;if(removed){const key=snapshotKey(removed.space,removed.owner),selected=selectedAssistant?.id===removed.owner&&selectedAssistant?.space_id===removed.space,writing=selected?chatSync?.close():null;if(selected)clearSelectedAssistant();await Promise.all([writing,viewWrite]);await chatCache?.remove(key).catch(cacheFailure);}await loadDirectory();}, titleChanged: () => renderShell(),routeChanged:value=>{route=value;renderShell();}, openChat:async target=>{if(target?.owner&&(!selectedAssistant||selectedAssistant.id!==target.owner||selectedAssistant.space_id!==target.space)){const assistant=assistants.find(item=>item.id===target.owner&&item.space_id===target.space);if(assistant)await selectAssistant(assistant,false,false);}route='chat';directoryOpen=!!target?.directory&&!selectedAssistant;renderShell();renderMessages();pages.permissions();if(!selectedAssistant&&!target?.directory)loadDirectory();}}) : null;
  const media = matchMedia('(max-width: 719px)');
  const reducedMotion=matchMedia('(prefers-reduced-motion: reduce)');
  const saveReads=()=>{if(!client.desktop)persist('pot-ui-read-v1',JSON.stringify(replies.records()));};
  const controlOf=item=>item?.control_id||item?.space_id||client.runtime?.runtime_id||'';
  function markVisibleRead(){
    const item=selectedAssistant;if(chatPreparing||restoringHistory||!client.production||!item||item.offline||route!=='chat'||menuOpen||document.visibilityState==='hidden'||!document.hasFocus()||(media.matches&&directoryOpen))return;
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
    layout:work=>reading.layout(work),
    onInput: text => { setDraft(state, completions?.value()??text, editorOwner); updateControls(); },
    onSend: submit,
    onCompositionEnd: () => { syncEditor(); updateControls(); },
  });
  const stopViewport = bindViewport(document.documentElement, {onWidthChange: () => editor.resize()});
  const restoredAttachmentFailures=new Set(),attachmentAttempts=new Map();
  const can=(operation,item=selectedAssistant)=>!client.production||supportsCapability(item,operation);
  const attachmentUI=client.production?createAttachmentComposer({client,form,input,getKey:()=>editorOwner,getCaps:()=>state.snapshot?.agent?.attachments,isSending:()=>state.pending.has(editorOwner),canAdd:()=>can('chat.attachments')&&route==='chat'&&!!state.snapshot&&connection==='connected'&&!state.pending.has(editorOwner)&&!selectedAssistant?.offline,changed:()=>updateControls(),locale:()=>locale}):null;
  if(attachmentUI){form.classList.add('has-attachments');attachmentUI.toolbar.append(send);}
  const referenceDraftHost=el('div','composer-references');form.prepend(referenceDraftHost);
  const referenceStatus=el('div','reference-announcement');referenceStatus.setAttribute('role','status');composerArea.append(referenceStatus);
  let referenceDraftSignature='',referenceNotice='';
  let historyLoading=false,historyFailure='',historyGeneration=0,historyFlight=null,historyRecovery=null,historyAbort=new AbortController();
  let historyScrollTime=0,historySpeed=0,historyLatency=200;
  const historyPrefetch=()=>Math.max(360,Math.min(history.clientHeight*4,history.clientHeight*2+historySpeed*historyLatency));
  function readHistory(query,options={}){
    if(options.positionTicket&&!currentChatPosition(options.positionTicket))return Promise.resolve(false);
    const receivedEpoch=epoch;
    if(historyLoading&&(historyRecovery||historyFlight))return (historyRecovery||historyFlight).then(()=>receivedEpoch===epoch?readHistory(query,options):false);
    historyFlight=readHistoryNow(query,options);return historyFlight;
  }
  async function readHistoryNow(query,{around=false,positionTicket}={}) {
    if(!client.history||!state.snapshot||historyLoading)return false;
    const receivedEpoch=epoch,generation=++historyGeneration,scope={...client.scope()};
    historyLoading=true;historyFailure='';renderMessages();
    try {
      const started=performance.now();
      const key=snapshotKey();
      let page=chatCache?await chatCache.readPage(key,query):null;
      if(!page)page=await client.history(query,{...scope,signal:historyAbort.signal});
      if(disposed||receivedEpoch!==epoch||generation!==historyGeneration||positionTicket&&!currentChatPosition(positionTicket))return false;
      historyLatency=historyLatency*.5+(performance.now()-started)*.5;
      const merged=chatCache?await chatCache.writePage(key,page,{...chatContext(),around}):mergeHistoryPage(state.snapshot,page,state.uiAnchor,{around});
      if(disposed||receivedEpoch!==epoch||generation!==historyGeneration||positionTicket&&!currentChatPosition(positionTicket))return false;
      if(!merged)return false;
      state.snapshot=merged;renderMessagesNow();scheduleViewSave();
      return true;
    }catch(reason){if(!disposed&&receivedEpoch===epoch&&generation===historyGeneration&&(!positionTicket||currentChatPosition(positionTicket))){historyFailure=reason?.status===404?'history.missing':'history.failed';}return false;}
    finally{if(!disposed&&receivedEpoch===epoch&&generation===historyGeneration){historyLoading=false;renderMessages();}}
  }
  async function jumpToMessage(id,{expand=false}={}){
    const ticket=positionChat({kind:'message',id,expand});
    let message=state.snapshot?.messages.find(message=>message.id===id&&!message.context_only);
    if(!message){prepareChat();if(!await readHistory({around:id},{around:true,positionTicket:ticket})){if(currentChatPosition(ticket))chatPosition=null;return;}message=state.snapshot?.messages.find(message=>message.id===id);}
    if(!currentChatPosition(ticket))return;
    if(!message){chatPosition=null;historyFailure='history.missing';renderMessages();return;}
    state.uiAnchor=message.sequence;
    renderMessagesNow();
    const row=[...messageNodes.values()].find(record=>record.row.dataset.messageId===id)?.row;
    row?.classList.add('reference-target');
  }

  const stopMessageSelection=bindMessageSelection(history,{capture:refs=>{
    if(!can('chat.references')||route!=='chat'||!state.snapshot||editorOwner!==ownerKey(state.snapshot)||state.pending.has(editorOwner))return;
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
  const completions=client.production?bindCompletions(input,{client,host:composerArea,locale:()=>locale,mobile:editor.mobile,setText:text=>editor.setValue(text)}):null;
  function syncEditor() {
    if (!state.snapshot) return;
    const nextOwner = ownerKey(state.snapshot);
    // Finish an IME commit into its original draft before adopting a newly
    // attached owner. Reconnecting must not disable or replace native preedit.
    if (editor.composing && editorOwner !== nextOwner) return;
    if(editorOwner!==nextOwner)referenceNotice='';
    editorOwner = nextOwner;
    if(completions)completions.setValue(draft(state,editorOwner));else editor.setValue(draft(state, editorOwner));
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
  function updateControls() { return reading.layout(updateControlsLayout); }
  function updateControlsLayout() {
    if(chatCacheFailed)error=t('chatCache.failed');
    const snapshot = state.snapshot;
    const pending = snapshot && state.pending.get(ownerKey(snapshot));
    const running = snapshot?.running;
    continuation?.update(snapshot,{connected:connection==='connected'&&can('chat.continue')&&!selectedAssistant?.offline&&!pending});
    const stopping=running&&(!client.production||(!draft(state).trim()&&!attachmentUI?.items().length));
    const label = stopping ? (cancelPending ? t('stopping') : t('stop')) : running?t('send.guidance'):t('send');
    send.replaceChildren(icon(stopping ? 'stop' : 'send'));
    send.title = label;
    send.setAttribute('aria-label', label);
    send.disabled = stopping ? !can('chat.cancel')||selectedAssistant?.offline||cancelPending || connection !== 'connected' : !can('chat.send')||selectedAssistant?.offline|| !snapshot || editorOwner !== ownerKey(snapshot) || pending || (!draft(state).trim()&&!attachmentUI?.items().length) || !!attachmentUI?.problem() || connection !== 'connected';
    attachmentUI?.update();
    updateReferenceDraft();
    input.disabled = !snapshot && !editorOwner;
    completions?.refresh();
    const status=titlebar.querySelector('.connection-status');
    if(status)reconcile(status,connectionStatus());
    renderMessages();
  }
  function connectionStatus() {
    const pending=state.snapshot&&state.pending.get(ownerKey(state.snapshot));
    const visibleConnection=selectedAssistant?.offline?'offline':connection;
    const projection=el('div','connection-status');
    projection.setAttribute('role','status');
    const label=text=>{const value=el('span','',text);value.title=text;return value;};
    projection.classList.toggle('has-error', Boolean(error || connectionError) || visibleConnection === 'offline');
    projection.classList.toggle('is-connected',!pending&&!error&&!connectionError&&visibleConnection==='connected');
    if(pending){
      projection.append(label(t(pending.phase==='uncertain'?'send.unknown':'send.confirming')));
      if(pending.recoverable){const retry=el('button','text-button',t('send.checkRetry'));retry.type='button';retry.disabled=visibleConnection!=='connected';retry.onclick=()=>confirmations.recover(pending);projection.append(retry);}
    }
    projection.append(label(error || connectionError || (visibleConnection === 'connected' ? t(client.production?'connected':'preview') : t(visibleConnection))));
    if (visibleConnection === 'offline') {
      const retry = el('button', 'text-button', t('retry'));
      retry.type = 'button';
      retry.onclick = () => loadSnapshot(true);
      projection.append(retry);
    }
    return projection;
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
    for (const name of ['chat', 'tasks', 'machines', 'extensions', 'usage', 'settings']) {
      const update=name==='settings'?updates?.button(inMenu):null;if(update)fragment.append(update);
      const item = button(name, t(name), inMenu ? 'menu-destination' : `rail-button${name === 'settings'&&!update ? ' rail-bottom' : ''}`);
      item.replaceChildren(navigationIcon(name));
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
    if(client.production){preferences.classList.add('product-actions');const projection=preferences.cloneNode(false);const close=button('close',t('close'));close.onclick=()=>preferences.close();projection.append(close);const action=(label,run)=>{const choice=el('button','menu-destination',label);choice.type='button';choice.onclick=()=>{preferences.close();run();};projection.append(choice);};if(selectedAssistant&&(can('assistant.configure')||can('assistant.profile')))action(t('assistantSettings'),()=>{route='settings';pages.assistant(selectedAssistant);renderShell();});if(selectedAssistant&&can('assistant.usage'))action(t('usage'),()=>{route='usage';pages.conversationUsage(selectedAssistant.name);renderShell();});if(state.snapshot?.running&&can('chat.control')){const captured={...client.scope()},target=state.snapshot.turn_id,paused=!!state.snapshot.turn?.paused;action(t(paused?'chat.resume':'chat.pause'),async()=>{try{await client.controlTurn({user_message_id:target,action:paused?'resume':'pause'},captured);}catch(reason){error=reason.message;updateControls();}});}action(t('settings'),()=>navigate('settings'));reconcile(preferences,projection);return;}
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
  function chatContext(){return {surface:viewSurface,anchor:state.uiAnchor,position:reading.snapshot(),window:state.snapshot?.messages.map(message=>({id:message.id,context_only:!!message.context_only}))};}
  function layoutKey(){return JSON.stringify([history.clientWidth,locale,theme,'plex-1']);}
  function scheduleViewSave(){
    if(!chatCache||disposed||chatPreparing)return;
    clearTimeout(viewTimer);viewTimer=setTimeout(()=>rememberChatPosition(),100);
  }
  function rememberChatPosition(){
    clearTimeout(viewTimer);
    if(!state.snapshot||chatPreparing)return viewWrite;
    const snapshot=state.snapshot,key=snapshotKey();
    while(toolMemory.size>1280)toolMemory.delete(toolMemory.keys().next().value);
    activeView={conversation_id:snapshot.conversation_id,boot_id:snapshot.boot_id,cursor:snapshot.cursor,
      position:reading.snapshot(),layout:layoutKey(),heights:[...windowHeights],tools:[...toolMemory],
      window:snapshot.messages.map(message=>({id:message.id,context_only:!!message.context_only}))};
    if(chatCache){const view=structuredClone(activeView);viewWrite=viewWrite.catch(()=>{}).then(()=>chatCache.saveView(key,viewSurface,view)).catch(reason=>cacheFailure(reason));}
    return viewWrite;
  }
  function cacheFailure(reason){
    if(disposed)return;
    chatCacheFailed=true;error=t('chatCache.failed');connection='offline';console.error('Chat cache:',reason);updateControls();
    if(!state.snapshot){chatPreparing=false;history.style.visibility='';history.removeAttribute('aria-busy');}
  }
  function prepareChat(){
    cancelAnimationFrame(revealFrame);revealFrame=0;chatPreparing=true;
    history.style.visibility='hidden';history.setAttribute('aria-busy','true');
  }
  function revealChat(){
    if(!chatPreparing||revealFrame||!state.snapshot||historyLoading||!chatShown)return;
    const receivedEpoch=epoch,key=snapshotKey();
    revealFrame=requestAnimationFrame(()=>{
      if(disposed||receivedEpoch!==epoch||key!==snapshotKey()){revealFrame=0;return;}
      editor.resize();renderMessagesNow(true);
      revealFrame=requestAnimationFrame(()=>{
        if(disposed||receivedEpoch!==epoch||key!==snapshotKey()){revealFrame=0;return;}
        renderMessagesNow(true);revealFrame=0;
        if(historyLoading)return;
        chatPreparing=false;history.style.visibility='';history.removeAttribute('aria-busy');scheduleViewSave();
      });
    });
  }
  const closeAssistantProfile = () => assistantProfile.hidePopover();
  window.addEventListener('resize', closeAssistantProfile);
  function toggleDirectory() {
    positionChat({kind:'restore',position:reading.snapshot()});
    directoryCollapsed = !directoryCollapsed;
    renderShell();
    root.querySelector('.directory-toggle')?.focus({preventScroll: true});
  }
  function openAssistantSettings() {
    assistantProfile.hidePopover();
    route = 'settings'; pages.assistant(selectedAssistant); renderShell();
  }
  assistantProfile.addEventListener('toggle', event => {
    root.querySelector('.assistant-profile-trigger')?.setAttribute('aria-expanded', String(event.newState === 'open'));
  });
  function renderShell() {
    root.classList.toggle('is-background',document.visibilityState==='hidden');
    const showingChat=route==='chat'&&!(media.matches&&directoryOpen);
    if(chatShown&&!showingChat){rememberChatPosition();cancelChatPosition();}
    if(!chatShown&&showingChat&&!chatPosition)positionChat({kind:'restore',position:activeView?.position});
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
    root.classList.toggle('directory-collapsed', directoryCollapsed);
    if (route !== 'chat' || narrow && directoryOpen) assistantProfile.hidePopover();
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
    }
    if (!narrow && surface !== 'tray' && ['chat','add-assistant'].includes(route) && directoryCollapsed) {
      const expand = button('panel-open', t('chat.expandAssistants'), 'icon-button directory-toggle');
      expand.setAttribute('aria-controls', directory.id); expand.setAttribute('aria-expanded', 'false');
      expand.onclick = toggleDirectory; titlebar.append(expand);
    }
    const title = el('div', 'window-title');
    const titleText=route === 'chat' ? (narrow && directoryOpen ? t('assistants') : selectedAssistant?.name || 'Pot') : pages?.title || t(route);title.title=titleText;
    if (route === 'chat' && !(narrow && directoryOpen)) {
      const identity = el(client.production && selectedAssistant ? 'button' : 'div', 'assistant-profile-trigger');
      const labels = el('span', 'assistant-identity-labels');
      const identityName=el('span','assistant-identity-name',titleText),source=agentSource(selectedAssistant?.origin,locale);if(source)identityName.append(source);labels.append(identityName);
      const spaceName = selectedAssistant?.origin?.name || '';
      if (spaceName) labels.append(el('span', 'assistant-identity-space', spaceName));
      identity.append(assistantAvatar(selectedAssistant, narrow || surface === 'tray' ? 24 : 34), labels);
      if (client.production && selectedAssistant) {
        identity.type = 'button'; identity.append(icon('down'));
        identity.setAttribute('aria-label', t('chat.assistantInfo'));
        identity.setAttribute('aria-haspopup', 'dialog'); identity.setAttribute('aria-controls', assistantProfile.id);
        identity.setAttribute('aria-expanded', String(assistantProfile.matches(':popover-open')));
        identity.onclick = event => {
          const trigger = event.currentTarget;
          const bounds = trigger.getBoundingClientRect();
          assistantProfile.style.left = `${Math.max(8, Math.min(bounds.left, innerWidth - 288))}px`;
          assistantProfile.style.top = `${bounds.bottom + 6}px`;
          assistantProfile.togglePopover({source: trigger});
        };
        const projection = assistantProfile.cloneNode(false);
        projection.setAttribute('aria-label', t('chat.assistantInfo'));
        const heading = el('div', 'assistant-profile-heading');
        const info = el('div', 'assistant-identity-labels');
        info.append(el('strong', '', titleText));
        if (spaceName) info.append(el('span', 'assistant-identity-space', spaceName));
        heading.append(assistantAvatar(selectedAssistant, 40), info);
        const settings = button('sliders', t('assistantSettings'), 'assistant-profile-settings');
        settings.append(el('span', '', t('assistantSettings'))); settings.onclick = openAssistantSettings;
        projection.append(heading);if(can('assistant.configure')||can('assistant.profile'))projection.append(settings);reconcile(assistantProfile, projection);
      }
      title.append(identity,connectionStatus());
    } else title.append(el('span', '', titleText));
    titlebar.append(title);
 if(narrow){const update=updates?.button();if(update){update.className="update-button icon-button";titlebar.append(update);}}
    if (surface === 'tray' && client.openMain) {
      const open = button('open', t('openMain'));
      open.onclick = async () => { try { await client.openMain(); } catch (reason) { error = reason.message; updateControls(); } };
      titlebar.append(open);
    }
    if (client.production && route === 'chat' && selectedAssistant && (can('assistant.configure')||can('assistant.profile'))) { const settings = button('sliders', t('assistantSettings')); settings.onclick = openAssistantSettings; titlebar.append(settings); }
    if(!client.production){const cloud=el('span','icon-button');cloud.title=t('remote');cloud.append(icon('cloud'));titlebar.append(cloud);}
    const more = button('more', t(client.production ? 'chat.actions' : 'more'));
    more.setAttribute('aria-haspopup', 'dialog');
    more.onclick = () => { preferencesOpen = true; renderPreferences(); preferences.showModal(); };
    if(!client.production||route==='chat')titlebar.append(more);
    if(route==='tasks'&&pages?.taskOwner){const actions=button('more',locale==='zh-CN'?'任务操作':'Task actions');actions.setAttribute('aria-haspopup','dialog');actions.onclick=()=>pages.taskMenu();titlebar.append(actions);}
    notice.textContent = client.production ? '' : t('notice');
    input.placeholder = t(client.production?'completions.placeholder':'composer');
    input.setAttribute('aria-label', t('composer'));
    history.setAttribute('aria-label', t('chat'));
    latest.title = t('latest');
    latest.setAttribute('aria-label', t('latest'));
    resourceNotices.hidden=resourceNotices.dataset.empty==='true'||!['chat','tasks','machines'].includes(route);history.hidden = composerArea.hidden = route !== 'chat';approvals.hidden=route!=='chat'&&!(route==='tasks'&&pages?.taskOwner);
    unavailable.hidden = route === 'chat';
    if (!client.production) unavailable.textContent = t('unavailable');
    // Search is window state and survives narrow conversation navigation/resizes.
    const nextDirectorySignature = JSON.stringify([locale,narrow,surface,selectedAssistant?.space_id,selectedAssistant?.id,spaces,assistants,state.snapshot?.assistants,replies.records(),creationStarting,assistantCreation.pending,creationFailed]);
    if (nextDirectorySignature !== directorySignature || !directory.childElementCount) {
    directorySignature=nextDirectorySignature;
    const searchValue = directory.querySelector('input')?.value || '';
    const scrollTop = directory.querySelector('.assistant-list')?.scrollTop || 0;
    const nextDirectory=directory.cloneNode(false);
    const heading = el('div', 'directory-heading');
    heading.append(el('span', '', t('assistants')));
    if (!narrow && surface !== 'tray') {
      const collapse = button('panel-close', t('chat.collapseAssistants'), 'icon-button directory-toggle');
      collapse.setAttribute('aria-controls', directory.id); collapse.setAttribute('aria-expanded', 'true');
      collapse.onclick = toggleDirectory; heading.append(collapse);
    }
    const creating=creationStarting||assistantCreation.pending;
    const add = button('plus', t(client.production ? (creating?'assistant.creating':'assistantStart.title') : 'preview'));
    add.disabled = !client.production||creating||!creationSpace()||creationSpace()?.connected===false;
    add.setAttribute('aria-busy',String(creating));
    add.onclick = () => {directoryOpen=false;navigate('add-assistant');};
    heading.append(add);
    const creationFailure=el('p','inline-error assistant-creation-error',t('assistant.createFailed'));
    creationFailure.hidden=!creationFailed;creationFailure.setAttribute('role','alert');
    const search = el('input', 'assistant-search');
    search.type = 'search'; search.placeholder = t('search'); search.setAttribute('aria-label', t('search')); search.value = searchValue;
    const list = el('div', 'assistant-list');
    const items = client.production ? assistants : state.snapshot?.assistants || [{id:'main', name:'Pot', preview:t('preview')}];
    for (const item of items) {
      const selected = (!client.production&&item.id==='main') || selectedAssistant?.id === item.id && selectedAssistant?.space_id === item.space_id;
      const row = el('button', `assistant-row${selected ? ' is-selected' : ''}`); row.type = 'button';row.dataset.viewKey=JSON.stringify([item.space_id,item.id]);row.dataset.searchName=item.name.toLowerCase();
      row.append(assistantAvatar(item,32)); const labels = el('span', 'assistant-labels'); const name = el('span', 'assistant-name');const title=el('span','assistant-title',item.name);title.title=item.name;name.append(title);const source=agentSource(item.origin,locale);if(source)name.append(source);
      const unread=replies.unread(controlOf(item),item.id,item.reply_count||0),activity=assistantActivity(item,unread);
      if(activity)name.append(assistantActivityIndicator(activity,locale));
      if(spaces.length>1){const location=el('span','assistant-location',item.space_name||t(item.space_kind==='remote'?'remote':'local'));location.title=location.textContent;name.append(location);}
      if(unread){const badge=el('span','unread-badge',unread>9?'9+':String(unread));badge.title=String(unread);badge.setAttribute('aria-label',t('assistant.unreadReplies').replace('{count}',unread.toLocaleString(locale)));name.append(badge);row.classList.add('has-unread');}
      const pending=Number(item.pending_approvals)||0;
      if(pending){row.classList.add('needs-approval');labels.append(name,el('span','assistant-approval-status',`${t('tool.awaitingApproval')} · ${pending}`));}
      else labels.append(name, el('span', 'assistant-preview', item.offline ? t('offline') : item.preview || ({failed:t('replyFailed'),needs_configuration:t('setupAI'),ready:t('connected')}[item.phase] || item.phase || '')));
      row.append(labels); row.onclick = () => client.production ? selectAssistant(item) : (directoryOpen = false, renderShell());
      list.append(row);
    }
    const empty = el('p', 'hint', t(items.length ? 'searchEmpty' : 'noAssistants'));
    const filter = search => {let count=0;const list=search.nextElementSibling;for(const row of list.querySelectorAll('.assistant-row')){row.hidden=!row.dataset.searchName.includes(search.value.toLowerCase());if(!row.hidden)count++;}list.querySelector('.hint').hidden=count>0;};
    search.oninput=event=>filter(event.currentTarget);list.append(empty);nextDirectory.append(heading,creationFailure,search,list);filter(search);reconcile(directory,nextDirectory);directory.querySelector('.assistant-list').scrollTop=scrollTop;
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
    // Layout and position restoration must finish before the newly visible Chat paints.
    if(chatShown){editor.resize();renderMessagesNow();}
  }
  function selectedSequence(){const selection=getSelection();if(!selection?.rangeCount||selection.isCollapsed||!history.contains(selection.anchorNode))return null;const element=selection.anchorNode.nodeType===Node.ELEMENT_NODE?selection.anchorNode:selection.anchorNode.parentElement;return element?.closest('article.message')?.dataset.sequence||null;}
  function nearBottom() { return history.scrollHeight - history.clientHeight - history.scrollTop < 80; }
  let renderFrame=0,fullRender=false,chatProjection;
  function currentChatPosition(ticket){return !disposed&&ticket.generation===chatNavigation&&ticket.key===snapshotKey();}
  function cancelChatPosition(){chatNavigation++;chatPosition=null;}
  function positionChat(intent){
    const ticket={kind:intent.kind,key:snapshotKey(),generation:++chatNavigation};
    chatPosition=ticket;
    if(intent.kind==='restore')reading.restore(intent.position);
    else if(intent.kind==='latest')reading.latest();
    else reading.locate({id:intent.id,expand:intent.expand});
    renderMessages();return ticket;
  }
  function pauseFollowing(){cancelChatPosition();reading.pause();}
  function loadEarlierNearTop(){
    if(!historyLoading&&!historyFailure&&history.scrollTop<historyPrefetch()&&state.snapshot?.history?.has_more){
      pauseFollowing();readHistory({before:state.snapshot.history.before});
    }
  }
  function loadNewerNearBottom(){
    if(!historyLoading&&!historyFailure&&history.scrollHeight-history.clientHeight-history.scrollTop<historyPrefetch()&&state.snapshot?.history?.has_newer){
      pauseFollowing();readHistory({after:state.snapshot.history.after});
    }
  }
  latest.onclick = async()=>{const ticket=positionChat({kind:'latest'});if(state.snapshot?.history?.has_newer){prepareChat();await readHistory({},{around:true,positionTicket:ticket});}if(!currentChatPosition(ticket))return;state.uiAnchor=null;renderMessagesNow();};
  historyStatus.onclick=()=>{pauseFollowing();if(historyFailure==='history.missing'){historyFailure='';renderMessages();}else if(!historyLoading&&state.snapshot?.history?.has_more)readHistory({before:state.snapshot.history.before});};
  const messageNodes = new Map(),toolMemory=new Map();
  const taskCache=new Map();
  const backgroundTaskView=createBackgroundTasks({locale:()=>locale,
    onOpen:task=>{const returnTo={route:'chat',scope:{...client.scope()}};route='tasks';pages.openTask(task,{returnTo});renderShell();},
    onToggle:change=>{cancelChatPosition();reading.pause({ignoreToggle:!!change.expanded});pendingTaskToggle=change;renderMessages();},
  });
  let backgroundTasks=null,backgroundTaskKey='';
  function refreshBackgroundTasks(){
    if(!client.production||!selectedAssistant)return Promise.resolve();
    if(!can('task.list')){backgroundTasks?.close();backgroundTasks=null;backgroundTaskKey='';return Promise.resolve();}
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
  const requestMessageRender=resize=>{
    if(resize&&chatShown)reading.reflow();else renderMessages(true);
  };
  let renderingSnapshot=null,renderingSending=null,renderingForest=null,navigationPath=new Map();
  const markdownFiles=scope=>({
    messageFiles:client.messageFiles?(message,options)=>client.messageFiles(message,{...scope,...options}):undefined,
    retryMessageFile:client.retryMessageFile?(message,ref,options)=>client.retryMessageFile(message,ref,{...scope,...options}):undefined,
    acquireResource:client.acquireResource?resource=>client.acquireResource(resource,scope):undefined,
    downloadResource:client.downloadResource?resource=>client.downloadResource(resource,scope):undefined,
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
    row.dataset.scrollAnchor=`message:${message.viewKey}`;
    if(message.compaction){
 if(!record.compact){record.compact=createCompaction({locale:()=>locale});bubble.append(record.compact.element);}
 record.compact.update(message);
 }else if(message.role==='tool'&&message.tool){
      const scope=client.scope?.()||{},key=JSON.stringify([scope.space,scope.owner,message.id]);
      if(!record.tool){record.tool=createToolCall({messageActor:()=>messageIdentities(client).actor(scope),resolveTask:id=>(taskCache.get(JSON.stringify([scope.space,scope.owner]))||[]).find(task=>task.id===id),onOpenTask:task=>{route='tasks';pages.openTask({...task,_space_id:scope.space,assistant_id:scope.owner},{returnTo:{route:'chat',scope:{...scope}}});renderShell();},resolveMessageIdentity:address=>messageIdentities(client).resolve(address),memory:toolMemory,key,locale:()=>locale,machineLabel:id=>machinePresentation(client).label(id,scope,locale),onDecision:(approval,action)=>pages.decideApproval(approval,action),onInteract:event=>reading.engage(event),onLocate:(call,turn)=>{const target=renderingForest.byID.get(turn+':'+call)?.message;if(target)jumpToMessage(target.id,{expand:true});},...markdownFiles(scope)});bubble.append(record.tool.element);}
      record.tool.update(message,{offline:connection==='offline'||disconnected,children:forest.byID.get(message.tool.turn_id+':'+message.tool.id)?.children.map(entry=>entry.message)||[]});
    }else if(message.role==='assistant'){
      record.reply ||= createReplyBubble(bubble);
      const scope=client.scope?.();
      record.reply.update(message,{t,...markdownFiles(scope),
        onSettings:async()=>{if(!await pages?.leave())return;route='settings';await pages.showProviders();renderShell();},
        onRetrySend:submit,canRetrySend:connection==='connected'&&!pendingSend()&&draft(state)===sending?.request.text});
      record.text=message.text;record.role=message.role;
    }else if (record.text !== message.text || record.role !== message.role) {
      bubble.replaceChildren();
      if (message.role === 'tool') bubble.append(el('pre','',message.text));
      else if (message.role === 'system') bubble.textContent = message.text;
      else {const scope=client.scope?.(),prose=el('div','message-prose');prose.append(renderMarkdown(message.text,{...markdownFiles(scope)}));bubble.append(prose);}
      record.text = message.text; record.role = message.role;
      row.className = `message role-${message.role}`;
    }
    if(message.role==='assistant'){
      const name=selectedAssistant?.name||'Pot',image=selectedAssistant?.avatar;
      if(!record.speaker||record.speakerName!==name||record.speakerImage!==image){
        record.speaker?.remove();record.speakerName=name;record.speakerImage=image;
        record.speaker=el('div','message-speaker');
        record.speaker.append(assistantAvatar(selectedAssistant,25),el('span','',name));
        row.prepend(record.speaker);
      }
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
      if(chatPosition&&navigationPath.has(message.id)||reading.destination?.expand&&reading.destination.id===message.id)record.tool.element.open=true;
      if(children.length||record.childrenWindow){
        if(!record.childrenWindow){record.childrenWindow=createMessageWindow({scroller:history,host:record.tool.children,nested:true,render:renderMessage,dispose:disposeMessage,requestRender:requestMessageRender,reading,heights:windowHeights});record.tool.element.addEventListener('toggle',()=>renderMessages());}
        record.childrenWindow.set(record.tool.element.open?children:[],{target:navigationPath.has(message.id)?{id:navigationPath.get(message.id)}:undefined});
      }
    }
    return record;
  };
  const messageWindow=createMessageWindow({scroller:history,host:windowHost,render:renderMessage,dispose:disposeMessage,requestRender:requestMessageRender,reading,heights:windowHeights});
  const viewportObserver=new ResizeObserver(()=>requestMessageRender(true));viewportObserver.observe(history);
  function renderMessages(viewportOnly=false){fullRender ||= !viewportOnly;if(disposed||renderFrame)return;renderFrame=requestAnimationFrame(()=>{renderFrame=0;const viewportOnly=!fullRender;fullRender=false;renderMessagesNow(viewportOnly);});}
  function renderMessagesNow(viewportOnly=false) {
    if(!chatShown)return;
    const navigation=chatPosition?.key===snapshotKey()?chatPosition:null;
    reading.flush();
    const anchor=reading.anchor;
    const snapshot=state.snapshot;
    const sending=state.sends.get(editorOwner||(snapshot&&ownerKey(snapshot)));
    const activeTurn=snapshot?.running?(snapshot.messages.find(message=>message.id===snapshot.turn_id)?.request_id||snapshot.turn_id):null;
    const offline=connection!=='connected'||disconnected||selectedAssistant?.offline;
    if(!chatProjection||chatProjection.snapshot!==snapshot||chatProjection.sending!==sending||chatProjection.phase!==sending?.phase||chatProjection.detail!==sending?.detail||chatProjection.offline!==offline||chatProjection.activeTurn!==activeTurn){
      const messages=projectScriptRuns(projectChatReplies(snapshot,sending,{offline}),{activeTurn});
      const forest=toolForest(messages),roots=messages.filter(message=>!message.tool?.parent_id||!forest.byID.has(message.tool.turn_id+':'+message.tool.parent_id));
      chatProjection={snapshot,sending,phase:sending?.phase,detail:sending?.detail,offline,activeTurn,messages,forest,roots};
    }
    const {messages:visibleMessages,forest,roots}=chatProjection;
    renderingSnapshot=snapshot;renderingSending=sending;renderingForest=forest;
    navigationPath=new Map();
    let destination=visibleMessages.find(message=>message.id===(reading.destination?.id||anchor?.id));
    const ancestry=new Set();
    while(destination?.tool?.parent_id&&!ancestry.has(destination.id)){
      ancestry.add(destination.id);
      const parent=forest.byID.get(destination.tool.turn_id+':'+destination.tool.parent_id)?.message;
      if(!parent)break;
      navigationPath.set(parent.id,destination.id);destination=parent;
    }
    let target=reading.following?{end:true}:destination?{id:destination.id,offset:reading.destination?undefined:anchor?.offset}:undefined;
    historyStatus.hidden=!client.history||!snapshot;
    historyStatus.disabled=historyLoading||!historyFailure&&!snapshot?.history?.has_more;
    historyStatus.textContent=t(historyLoading?'history.loading':historyFailure|| (snapshot?.history?.has_more?'history.earlier':'history.start'));
    const tasks=client.production?(taskCache.get(JSON.stringify([client.scope().space,client.scope().owner]))||[]):[];
    if(client.production&&!viewportOnly)backgroundTaskView.update(snapshotKey(),tasks,(id,phase)=>pages.taskNeedsApproval(id,phase));
    // The task region stays outside recycled message rows and shares the chat scroller.
    if(client.production&&tasks.length){
      if(backgroundTaskView.element.parentElement!==messageColumn)messageColumn.insertBefore(backgroundTaskView.element,chatApprovals);
    }else backgroundTaskView.element.remove();
    if(pendingTaskToggle&&!pendingTaskToggle.expanded&&reading.position?.anchors[0]?.key.startsWith('task:')){
      const retained=new Set([...backgroundTaskView.element.querySelectorAll('[data-scroll-anchor]')].map(node=>node.dataset.scrollAnchor));
      if(!retained.has(reading.position.anchors[0].key))reading.locate({key:'background-tasks-heading',align:'nearest'});
    }
    pendingTaskToggle=null;
    if(!viewportOnly)taskClock.refresh();
    if (snapshot && !visibleMessages.length) {
      if(!messageColumn.querySelector('.empty-chat'))messageColumn.append(el('p', 'empty-chat', t('first')));
    }else if(visibleMessages.length){
      messageColumn.querySelector('.empty-chat')?.remove();
    }
    if(!viewportOnly)pages?.renderApprovals();
    if(!chatApprovals.hidden&&locateApprovals===JSON.stringify(client.scope?.())){
      locateApprovals='';cancelChatPosition();reading.locate({key:'chat-approvals',align:'nearest'});
    }
    if(reading.destination?.key==='chat-approvals')target={end:true};
    messageWindow.layout(roots,{anchor:anchor?.id,target,restore:()=>reading.apply()});
    if(navigation&&snapshot&&!historyLoading&&navigation.kind!=='message')chatPosition=null;
    latest.hidden=reading.following;
    state.uiAnchor=reading.following?null:selectedSequence()||reading.anchor?.sequence||null;
    revealChat();
  }
  function restoreSavedPosition(position){
    positionChat({kind:'restore',position});
    state.uiAnchor=reading.anchor?.sequence||null;
  }
  function pendingSend(){return state.snapshot&&state.pending.has(ownerKey(state.snapshot));}
  async function applySnapshot(snapshot, receivedEpoch = epoch, receivedMessages=snapshot.messages) {
    if (disposed || receivedEpoch !== state.epoch) return;
    if(state.snapshot&&validSnapshot(snapshot)&&state.snapshot.boot_id!==snapshot.boot_id){
      state.snapshot=null;pages?.clearApprovals();client.resources?.invalidate([],{space:client.scope().space,boot:snapshot.boot_id});
      pages?.refresh([],{space:client.scope().space,boot:snapshot.boot_id});
    }
    if (state.snapshot && validSnapshot(snapshot) && ownerKey(snapshot) !== ownerKey(state.snapshot)) {
      connection = 'offline'; error = t('identityChanged'); updateControls(); return;
    }
    reading.flush();
    const readingBeforeReset=snapshot.kind==='reset'&&!reading.following?reading.anchor:null;
    if(chatCache&&readingBeforeReset&&!snapshot.messages.some(message=>message.id===readingBeforeReset.id&&!message.context_only)){
      const ticket={generation:chatNavigation,key:snapshotKey()},generation=++historyGeneration,scope={...client.scope()},context=chatContext();
      let finish;const recovery=new Promise(resolve=>{finish=resolve;});historyRecovery=recovery;
      historyLoading=true;connection='connecting';updateControls();
      try{
        let page=await chatCache.readPage(ticket.key,{around:readingBeforeReset.id});
        if(!page)page=await client.history({around:readingBeforeReset.id},{...scope,signal:historyAbort.signal});
        if(receivedEpoch!==epoch||generation!==historyGeneration||!currentChatPosition(ticket))return;
        const restored=await chatCache.writePage(ticket.key,page,{...context,around:true});
        if(!restored||receivedEpoch!==epoch||generation!==historyGeneration||!currentChatPosition(ticket))return;
        snapshot=restored;
      }catch(reason){if(receivedEpoch===epoch&&generation===historyGeneration&&currentChatPosition(ticket))historyFailure=reason?.status===404?'history.missing':'history.failed';return;}
      finally{finish();if(historyRecovery===recovery)historyRecovery=null;if(receivedEpoch===epoch&&generation===historyGeneration){historyLoading=false;renderMessages();}}
    }
    const initialSnapshot = !state.snapshot;
    if(validSnapshot(snapshot)){awaitingSnapshot=false;}
    const pendingReceipt=state.pending.get(ownerKey(snapshot));
    let accepted;try{accepted=acceptSnapshot(state,snapshot,receivedEpoch,{committed:!!chatCache,receivedMessages});}catch(reason){if(reason.message==='conversation_gap'){loadSnapshot(true);return;}throw reason;}
    if (!accepted){if(state.snapshot&&validSnapshot(snapshot)&&state.snapshot.cursor===snapshot.cursor){connection='connected';updateControls();}return;}
    if(pendingReceipt && !state.pending.has(pendingReceipt.key))confirmations.confirmed(pendingReceipt);
    connection = 'connected';
    connectionError = '';
    if(chatCacheFailed){chatCacheFailed=false;error='';}
    snapshot=state.snapshot;
    if(editorOwner !== ownerKey(snapshot)&&!chatPosition)restoreSavedPosition(activeView?.position);
    syncEditor();
    restoreFailedAttachments();
    if(markVisibleRead())renderShell();
    renderMessages();updateControls();
    if(client.production)pages.syncApprovals();
    if(readingBeforeReset&&!snapshot.messages.some(message=>message.id===readingBeforeReset.id&&!message.context_only)){
      readHistory({around:readingBeforeReset.id},{around:true});
    }
    if(initialSnapshot){if(client.production)pages.permissions();renderShell();}
    scheduleViewSave();
  }
  function onSnapshot(snapshot,receivedEpoch=epoch){
    if(disposed||receivedEpoch!==epoch)return;
    if(!validSnapshot(snapshot)){cacheFailure(Error('Invalid conversation snapshot'));return;}
    const scope=client.scope?.(),expected=scope?.space?JSON.stringify([scope.space,scope.owner]):scope?.owner;
    if(client.production&&expected&&snapshot.owner_id!==expected){connection='offline';error=t('identityChanged');updateControls();return;}
    if(chatCache)chatSync?.push(snapshot);else applySnapshot(snapshot,receivedEpoch);
  }
  const abort = new AbortController();
  async function loadSnapshot(reattach = false) {
    if (disposed) return;
    if(reattach){const retained=state.snapshot;historyGeneration++;historyLoading=false;epoch=beginSubscription(state);if(retained)acceptSnapshot(state,retained,epoch,{committed:true});disconnected=false;return attach();}
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
    chatSync?.close();
    const receivedEpoch = epoch;
    if(chatCache)chatSync=createChatSync({cache:chatCache,key:snapshotKey(),context:()=>receivedEpoch===epoch?chatContext():{surface:viewSurface},committed:(snapshot,received)=>applySnapshot(snapshot,receivedEpoch,received),failed:reason=>{if(disposed||receivedEpoch!==epoch)return;if(['conversation_gap','history_gap'].includes(reason.message))loadSnapshot(true);else cacheFailure(reason);}});
    awaitingSnapshot=!!client.production;connection='connecting';updateControls();
    unsubscribe = client.subscribe(value => onSnapshot(value, receivedEpoch), (value, reason) => {
      if (disposed || receivedEpoch !== state.epoch) return;
      if (value === 'offline') {disconnected = true;connectionError = reason?.message || t('streamDisconnected');}
      if (value === 'connected') connectionError = '';
      if (value === 'connected' && disconnected) { disconnected = false; loadSnapshot(true); }
      else { connection = chatCacheFailed?'offline':value==='connected'&&awaitingSnapshot?'connecting':value; updateControls(); }
    });
    if(!client.production)return loadSnapshot();
    // Both SSE and the desktop subscription send an authoritative initial snapshot.
    return Promise.resolve();
  }
  async function selectAssistant(item, initial = false,record=true) {
    const selection=++selectionGeneration;
    completions?.reset();
    if(editor.composing){input.blur();if(editor.composing)return;}
    if(pages&&!await pages.leave()||selection!==selectionGeneration||disposed)return;
    unsubscribe();cancelChatPosition();historyAbort.abort();
    if(chatSync)await chatSync.retire();
    if(chatShown)await rememberChatPosition();
    if(selection!==selectionGeneration||disposed)return;
    historyAbort=new AbortController();
    chatSync?.close();chatSync=null;cancelChatPosition();prepareChat();
    pages?.clearApprovals();locateApprovals=item.pending_approvals?JSON.stringify({owner:item.id,space:item.space_id||''}):'';selectedAssistant=item;
    client.setScope({owner:item.id,space:item.space_id||''});updates?.refresh(client.scope());
    epoch=beginSubscription(state);const receivedEpoch=epoch;editorOwner=null;activeView=null;route='chat';if(!initial)directoryOpen=false;
    pendingTaskToggle=null;chatProjection=null;renderingSnapshot=null;renderingSending=null;renderingForest=null;navigationPath.clear();historyScrollTime=0;historySpeed=0;historyLatency=200;reading.restore();
    messageWindow.set([]);windowHost.replaceChildren();messageColumn.replaceChildren(historyStatus,windowHost,chatApprovals);messageNodes.clear();windowHeights.clear();toolMemory.clear();
    historyGeneration++;historyLoading=false;historyFailure='';state.uiAnchor=null;error='';chatCacheFailed=false;connectionError='';disconnected=false;connection='connecting';
    renderShell();
    try{
      const cached=chatCache?await chatCache.read(snapshotKey(),viewSurface):null;
      if(disposed||selection!==selectionGeneration||receivedEpoch!==epoch)return;
      activeView=cached?.view||null;
      if(activeView?.layout===layoutKey())for(const [key,height]of activeView.heights||[])windowHeights.set(key,height);
      for(const [key,value]of activeView?.tools||[])toolMemory.set(key,value);
      restoreSavedPosition(activeView?.position);
      if(cached?.snapshot){acceptSnapshot(state,cached.snapshot,epoch,{committed:true});syncEditor();}
      if(client.production&&record)saveNavigation('chat',initial);
      renderShell();attach();pages?.permissions();refreshBackgroundTasks();
      const anchor=reading.anchor;
      if(anchor&&!state.snapshot?.messages.some(message=>message.id===anchor.id))await readHistory({around:anchor.id},{around:true});
      else if(reading.following&&state.snapshot?.history?.has_newer)await readHistory({},{around:true});
    }catch(reason){if(!disposed&&receivedEpoch===epoch){cacheFailure(reason);}}
  }
  function creationSpace(){return selectedAssistant?spaces.find(space=>space.id===selectedAssistant.space_id):spaces.find(space=>space.kind==='local')||spaces[0];}
  async function createAssistant() {
    if(creationStarting||assistantCreation.pending)return;
    const target=creationSpace();if(!target||target.connected===false)return;
    const origin={route,owner:selectedAssistant?.id,space:selectedAssistant?.space_id};
    creationStarting=true;creationFailed=false;error='';renderShell();
    try{
      if(!await pages.leave()||disposed)return;
      const providers=await client.request('/api/v1/providers',{space:target.id,owner:'',authoritative:true});
      if(disposed)return;
      if(!providers.default_provider_id){await pages.setupAI({space:target.id,owner:''});renderShell();return;}
      const assistant=await assistantCreation.create(target);
      if(disposed||!assistant)return;
      // Confirm the post-create catalogue before selecting: a stale cached list
      // would otherwise treat the newly created assistant as externally deleted.
      await (client.read||client.request)('/api/v1/assistants',{space:target.id,owner:'',authoritative:true});
      if(disposed)return;
      const current=spaces.find(space=>space.id===target.id);
      if(!current||(current.control_id||current.id)!==(target.control_id||target.id))throw Error('Space identity changed');
      const item={...assistant,control_id:current.control_id||current.id,space_name:current.name,space_kind:current.kind};
      directoryEpoch++;
      if(!assistants.some(value=>value.id===item.id&&value.space_id===item.space_id))assistants=[...assistants,item];
      client.assistants=assistants;
      if(route===origin.route&&selectedAssistant?.id===origin.owner&&selectedAssistant?.space_id===origin.space)await selectAssistant(item);
      assistantCreation.complete(target);
      await loadDirectory();
    }catch{
      if(!disposed){creationFailed=true;error=t('assistant.createFailed');updateControls();}
    }finally{creationStarting=false;if(!disposed)renderShell();}
  }
  function clearSelectedAssistant() {
    const space=selectedAssistant?.space_id||client.scope().space;
    selectionGeneration++;unsubscribe();historyAbort.abort();historyAbort=new AbortController();chatSync?.close();chatSync=null;epoch=beginSubscription(state);activeView=null;selectedAssistant=null;editorOwner=null;pendingSelection=null;
    client.setScope({space,owner:''});pages.clearApprovals();completions?.reset();
    clearTimeout(viewTimer);cancelChatPosition();reading.restore();windowHeights.clear();toolMemory.clear();chatProjection=null;renderingSnapshot=null;renderingSending=null;renderingForest=null;navigationPath.clear();
    messageWindow.set([]);messageNodes.clear();windowHost.replaceChildren();messageColumn.replaceChildren(historyStatus,windowHost,chatApprovals);
    input.value='';directoryOpen=true;error='';
  }
  const stopResources=client.resources?.subscribe((meta,reason)=>{if(disposed||!['/api/v1/spaces','/api/v1/assistants','/api/v1/agent-links','/api/v1/machines'].includes(meta.path))return;if(reason){error=translate(locale,'loading.stale');updateControls();return;}if(error===translate(locale,'loading.stale'))error='';loadDirectory();});
  const directoryQueue=createRefreshQueue(()=>readDirectory(),{active:()=>!disposed});
  function loadDirectory(){return directoryQueue.run();}
  async function readDirectory() {
    if (!client.production) return;
    const notificationEpoch=notifications?.epoch;
    const previousMachines=machinePresentation(client).revision;
    const ticket = ++directoryEpoch,previousSpaces=JSON.stringify(spaces),previousOwners=JSON.stringify(assistants.map(item=>[item.space_id,item.id,item.name,item.offline,item.capabilities]));
    try {
      const catalog = await (client.read||client.request)('/api/v1/spaces', {owner:'', space:'',signal:abort.signal});
      if (ticket !== directoryEpoch || disposed) return;
      spaces = catalog.items || [];client.spaces=spaces;
      if (!spaces.length) throw Error('No serving Space');
      if(!selectedAssistant){client.setScope({space:catalog.active_space_id||spaces[0].id,owner:''});updates?.refresh(client.scope());}
      const projectAssistants=(value,space,offline)=>{let items=value;if(typeof items==='string'){try{items=JSON.parse(items);}catch{items=[];}}return (items?.items||items||[]).map(item=>({...item,control_id:space.control_id||space.id,space_id:space.id,space_name:space.name,space_kind:space.kind,offline}));};
      const linkFailures=[];
      const results = await Promise.allSettled(spaces.map(async space => {
        const [items]=await Promise.all([(client.read||client.request)('/api/v1/assistants',{owner:'',space:space.id,signal:abort.signal}),machinePresentation(client).load({space:space.id,signal:abort.signal})]);
        const local=projectAssistants(items,space,space.connected===false);
        try{const links=await (client.read||client.request)('/api/v1/agent-links',{owner:'',space:space.id,signal:abort.signal});return [...local,...projectLinkedAssistants(links,space)];}
        catch{linkFailures.push(space.id);return [...local,...assistants.filter(item=>item.origin&&item.space_id===space.id).map(item=>({...item,offline:true}))];}
      }));
      if (ticket !== directoryEpoch || disposed) return;
      assistants = results.flatMap((result, index) => result.status === 'fulfilled' ? result.value : (()=>{const space=spaces[index],previous=assistants.filter(item=>item.space_id===space.id);return previous.length?previous.map(item=>({...item,offline:true})):projectAssistants(space.assistants||[],space,true);})());
      client.assistants=assistants;
      const changed=[];if(previousMachines!==machinePresentation(client).revision)changed.push('machines');if(previousSpaces!==JSON.stringify(spaces))changed.push('spaces');if(previousOwners!==JSON.stringify(assistants.map(item=>[item.space_id,item.id,item.name,item.offline,item.capabilities])))changed.push('assistants');if(changed.length)pages.refresh(changed);
      for(const item of assistants){const arrival=replies.observe(controlOf(item),item.id,item.reply_id||'',item.reply_count||0,!item.offline);if(arrival.arrived&&route==='chat'&&document.visibilityState!=='hidden'&&!reducedMotion.matches){reactions.set(replyKey(controlOf(item),item.id),performance.now());setTimeout(()=>{if(!disposed){directorySignature='';renderShell();}},650);}}saveReads();
      if (linkFailures.length||results.some(result => result.status === 'rejected')) error = t('partialOffline');else if(error===t('partialOffline'))error='';
      if(selectedAssistant&&!pages.editing&&!assistants.some(item=>item.id===selectedAssistant.id&&item.space_id===selectedAssistant.space_id)){
        const source=spaces.findIndex(space=>space.id===selectedAssistant.space_id);
        if(source>=0&&results[source].status==='fulfilled'&&spaces[source].connected!==false)clearSelectedAssistant();
      }
      if (selectedAssistant){selectedAssistant = assistants.find(item => item.id===selectedAssistant.id&&item.space_id===selectedAssistant.space_id) || selectedAssistant;if(selectedAssistant.offline)connection='offline';}
      renderShell();renderMessages();
      if(!assistants.length&&route==='chat'&&!pages.editing){
        const available=!linkFailures.length&&results.every(result=>result.status==='fulfilled')&&spaces.some(space=>space.connected!==false);
        if(available){directoryOpen=false;route='add-assistant';renderShell();await pages.show('add-assistant');}else{directoryOpen=false;renderShell();const empty=el('div','empty-chat');empty.append(el('p','hint',t('partialOffline')));const retry=el('button','control',t('retry'));retry.onclick=()=>loadDirectory();empty.append(retry);messageColumn.replaceChildren(empty);}
      }
      mainReady();
      if(pendingSelection){const chosen=assistants.find(item=>item.id===pendingSelection.id&&item.space_id===pendingSelection.space_id);pendingSelection=null;if(chosen){await selectAssistant(chosen);notifications?.directory({epoch:notificationEpoch});return;}}
      if (route==='chat' && !selectedAssistant && assistants.length && !pages.editing) {
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
  const mediaChanged = () => { menuOpen = false; closeAssistantProfile(); renderShell(); };
  document.addEventListener('keydown', keydown);
  media.addEventListener('change', mediaChanged);
  renderShell();editor.resize();
  function attachChanges(){
    stopChanges();stopChanges=client.changes((topics,source={})=>{
      const keys=Array.isArray(topics)?topics:Object.keys(topics||{});
      client.resources?.invalidate(keys,source);
 if(relevantChange(keys,['updates','spaces','runtime'],client.scope().space,source))updates?.refresh(client.scope());
      if(source.boot&&state.snapshot&&(!source.space||source.space===client.scope().space)&&state.snapshot.boot_id!==source.boot)loadSnapshot(true);
      if(source.reset){notifications?.refresh({reset:true,space:source.space});loadDirectory();}
      else if(relevantChange(keys,['task-outcomes','permissions']))notifications?.refresh({topics:keys,space:source.space});
      if(relevantChange(keys,['spaces','assistants','assistant-mail','agent-links','conversation','runtime','permissions','tasks','runner']))loadDirectory();
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
  const persistChat=()=>{if(document.visibilityState==='hidden')rememberChatPosition();};
  window.addEventListener('pagehide',rememberChatPosition);document.addEventListener('visibilitychange',persistChat);
  return () => {
    if(disposed)return;
    rememberChatPosition();clearTimeout(viewTimer);cancelAnimationFrame(revealFrame);historyAbort.abort();
    window.removeEventListener('pagehide',rememberChatPosition);document.removeEventListener('visibilitychange',persistChat);
    Promise.all([viewWrite,chatSync?.close()]).finally(()=>chatCache?.close());
    disposed = true;updates?.close();confirmations.close();cancelAnimationFrame(renderFrame);viewportObserver.disconnect();reading.dispose();messageWindow.close();stopResources?.();client.resources?.clear();stopResume();taskClock.dispose();onboarding?.dispose();notifications?.dispose();directoryQueue.close();backgroundTasks?.close();directoryEpoch++; abort.abort(); unsubscribe(); stopChanges(); pages?.dispose(); editor.destroy();
    stopMessageSelection();completions?.dispose();attachmentUI?.destroy();client.releaseAttachments?.();
    stopRead();reducedMotion.removeEventListener('change',reducedChanged);window.removeEventListener('focus',focusRead);document.removeEventListener('visibilitychange',focusRead);window.removeEventListener('storage',readStorage);
    root.removeEventListener('pointerdown',pointerDown);root.removeEventListener('pointermove',pointerMove);root.removeEventListener('pointerup',pointerUp);root.removeEventListener('pointercancel',pointerCancel);
    window.removeEventListener('pot:preference',preferencesChanged);
    document.removeEventListener('keydown', keydown);window.removeEventListener('popstate',popNavigation);
    media.removeEventListener('change', mediaChanged);
    window.removeEventListener('resize', closeAssistantProfile);
    stopViewport();
    root.replaceChildren();
  };
}
