import {domainsFor} from '../state/domains/index.mjs';
import {machinePresentation} from '../state/machine-identity.mjs';
import {storedPreference} from '../state/preferences.mjs';
import {createCompaction} from './compaction.mjs';
import {projectLinkedAssistants,supportsCapability} from '../state/agent-links.mjs';
import {agentSource} from './connection-visuals.mjs';
import {createUpdates} from './updates.mjs';
import {createCommunicationWindow} from './communication-window.mjs';
import {createInitialChatLayout} from './initial-chat-layout.mjs';
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
  return storedPreference(key, fallback);
}
function persist(key, value) { try { localStorage.setItem(key, value); } catch { /* restricted WebView */ } }

// Product components depend only on this client contract, not browser transport
// or Wails APIs. DOM nodes, especially the editor, survive stream/state changes.
export function mountApp(root, client, {surface = 'main'} = {}) {
  const domains=domainsFor(client),releaseDomains=domains.retain();
  let viewSurface=surface;
  if(client.production&&!client.desktop){
    let tabID;try{tabID=sessionStorage.getItem('pot-chat-view-id');if(!tabID){tabID=crypto.randomUUID();sessionStorage.setItem('pot-chat-view-id',tabID);}}catch{tabID=crypto.randomUUID();}
    viewSurface='browser:'+tabID;
  }
  const params = new URLSearchParams(location.search);
  let locale = params.get('locale') || stored('pot-ui-locale', navigator.language.startsWith('zh') ? 'zh-CN' : 'en');
  if (!messages[locale]) locale = 'en';
  client.uiLocale=()=>locale;
  let theme = params.get('theme') || stored('pot-ui-theme', 'system');
  if (!['system', 'light', 'dark'].includes(theme)) theme = 'system';
  const state = createConversationState();
  let initialReads=[];try{initialReads=client.desktop?client.readBaselines||[]:JSON.parse(stored('pot-ui-read-v1','[]'));}catch{}
  const replies=createReplies(Array.isArray(initialReads)?initialReads:[]),reactions=new Map(),readAttempts=new Set();
  let epoch = beginSubscription(state);
  let disconnected = false;
  let disposed = false;
  let route = 'chat';
  let connection = 'connecting';
  let error = '',chatCacheFailed=false,directoryStorageFailed=false;
  const sendCacheFailures=new Map();
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
  let activeView=null,viewWrite=Promise.resolve(),viewTimer,chatSession=null,selectionGeneration=0;
  let chatPreparing=false,initialIntent=null;
  let chatShown=false,pendingTaskToggle=null;
  let chatPosition=null,chatNavigation=0;
  let awaitingSnapshot=false;
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
  const messageNodes=new Map(),toolMemory=new Map(),windowHeights=new Map();
  const messageWindow=createCommunicationWindow({
    scroller:history,host:windowHost,render:(...args)=>renderMessage(...args),dispose:entry=>disposeMessage(entry),heights:windowHeights,
    onOpenTask:task=>openChatTask(task),locale:()=>locale,actor:()=>messageIdentities(client).actor(client.scope?.()),resolveIdentity:address=>messageIdentities(client).resolve(address),memory:toolMemory,scope:()=>JSON.stringify(client.scope?.()),
    active:()=>chatShown&&!disposed,
    requestRender:()=>renderMessages(true),
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
  const stopReading=messageWindow.subscribeReading(()=>{latest.hidden=messageWindow.following;state.uiAnchor=messageWindow.following?null:selectedSequence()||messageWindow.anchor?.sequence||null;scheduleViewSave();});
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
 const pages = client.production ? createPages({updates,chatVisible:()=>route==='chat'&&!root.classList.contains('show-directory'),onApprovalsChanged:()=>renderMessages(),client, host: unavailable, approvalHost: approvals, chatApprovalHost:chatApprovals, locale: () => locale, notify: value => {error = value; updateControls();}, changed: async value => {if(value?.assistant)pendingSelection=value.assistant;const removed=value?.deletedAssistant;if(removed){const selected=selectedAssistant?.id===removed.owner&&selectedAssistant?.space_id===removed.space;if(selected)clearSelectedAssistant();await domains.chat.remove(removed).catch(cacheFailure);}await loadDirectory();}, titleChanged: () => renderShell(),routeChanged:value=>{route=value;renderShell();}, openChat:async target=>{if(target?.assistant)directoryEpoch++;if(target?.owner&&(!selectedAssistant||selectedAssistant.id!==target.owner||selectedAssistant.space_id!==target.space)){let assistant=assistants.find(item=>item.id===target.owner&&item.space_id===target.space);if(!assistant&&target.assistant){assistant={...target.assistant,space_id:target.space};assistants=[...assistants,assistant];client.assistants=assistants;}if(assistant)await selectAssistant(assistant,false,false);}route='chat';directoryOpen=!!target?.directory&&!selectedAssistant;renderShell();renderMessages();pages.permissions();if(!selectedAssistant&&!target?.directory)loadDirectory();}}) : null;
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
    onInput: text => { setDraft(state, completions?.value()??text, editorOwner); updateControls(); },
    onSend: submit,
    onCompositionEnd: () => { syncEditor(); updateControls(); },
  });
  const stopViewport = bindViewport(document.documentElement, {onWidthChange: () => editor.resize()});
  const restoredAttachmentFailures=new Set(),attachmentAttempts=new Map();
  const can=(operation,item=selectedAssistant)=>!client.production||!directoryStorageFailed&&supportsCapability(item,operation);
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
    if(!chatSession?.hasHistory||!state.snapshot||historyLoading)return false;
    const receivedEpoch=epoch,generation=++historyGeneration;
    historyLoading=true;historyFailure='';renderMessages();
    try {
      const started=performance.now();
      const merged=await chatSession.history(query,{around,signal:historyAbort.signal,valid:()=>!disposed&&receivedEpoch===epoch&&generation===historyGeneration&&(!positionTicket||currentChatPosition(positionTicket))});
      if(disposed||receivedEpoch!==epoch||generation!==historyGeneration||positionTicket&&!currentChatPosition(positionTicket))return false;
      historyLatency=historyLatency*.5+(performance.now()-started)*.5;
      if(!merged)return false;
      state.snapshot=merged;renderMessagesNow();scheduleViewSave();
      return true;
    }catch(reason){if(!disposed&&receivedEpoch===epoch&&generation===historyGeneration&&(!positionTicket||currentChatPosition(positionTicket))){historyFailure=reason?.status===404?'history.missing':'history.failed';if(reason.storage)cacheFailure(reason);}return false;}
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
    const refs=draftReferences(state,editorOwner),next=JSON.stringify([editorOwner,refs,locale]);
    if(next===referenceDraftSignature)return;referenceDraftSignature=next;referenceDraftHost.hidden=!refs.length;
    referenceDraftHost.replaceChildren(renderMessageReferences(refs,{messages:state.snapshot?.messages||[],locale:()=>locale,onJump:jumpToMessage,onRemove:index=>{
      setDraftReferences(state,refs.filter((_,i)=>i!==index),editorOwner);
      // The submitted request owns a copy of its references. Cancelling the
      // editable draft must also release the original prose selection, which
      // otherwise stays highlighted and can be captured again on keyup.
      const selection=window.getSelection();
      if(selection?.rangeCount&&history.contains(selection.getRangeAt(0).commonAncestorContainer))selection.removeAllRanges();
      referenceNotice='references.removed';updateControls();
    }}));
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
    cacheFailed:(receipt,reason,retry)=>{sendCacheFailures.set(receipt,retry);updateControls();},
    changed:()=>{if(!disposed){syncEditor();restoreFailedAttachments();updateControls();}},
  });
  function submit() {
    if (!can('chat.send')||chatCacheFailed||(!client.production&&state.snapshot?.running) || selectedAssistant?.offline || connection !== 'connected' || editorOwner !== (state.snapshot && ownerKey(state.snapshot))) return;
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
    if (!can('chat.cancel')||cancelPending || !state.snapshot?.running) return;
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
  function updateControls() { return updateControlsLayout(); }
  function updateControlsLayout() {
    if(chatCacheFailed)error=t('chatCache.failed');
    if(directoryStorageFailed)error=t('storage.failed');
    const snapshot = state.snapshot;
    const pending = snapshot && state.pending.get(ownerKey(snapshot));
    const running = snapshot?.running;
    continuation?.update(snapshot,{connected:connection==='connected'&&can('chat.continue')&&!selectedAssistant?.offline&&!pending});
    const stopping=running&&(!client.production||(!draft(state).trim()&&!attachmentUI?.items().length));
    const label = stopping ? (cancelPending ? t('stopping') : t('stop')) : running?t('send.guidance'):t('send');
    send.replaceChildren(icon(stopping ? 'stop' : 'send'));
    send.title = label;
    send.setAttribute('aria-label', label);
    send.disabled = stopping ? !can('chat.cancel')||selectedAssistant?.offline||cancelPending || connection !== 'connected' : !can('chat.send')||chatCacheFailed||selectedAssistant?.offline|| !snapshot || editorOwner !== ownerKey(snapshot) || pending || (!draft(state).trim()&&!attachmentUI?.items().length) || !!attachmentUI?.problem() || connection !== 'connected';
    attachmentUI?.update();
    updateReferenceDraft();
    input.disabled = !snapshot && !editorOwner;
    completions?.refresh();
    const status=titlebar.querySelector('.connection-status');
    if(status)reconcile(status,connectionStatus());
    if(preferencesOpen)renderPreferences();
    renderMessages();
  }
  function connectionStatus() {
    const pending=state.snapshot&&state.pending.get(ownerKey(state.snapshot));
    const cacheFailures=[...sendCacheFailures].filter(([receipt])=>receipt.key===(state.snapshot&&ownerKey(state.snapshot)));
    const visibleConnection=selectedAssistant?.offline?'offline':connection;
    const projection=el('div','connection-status');
    projection.setAttribute('role','status');
    const label=text=>{const value=el('span','',text);value.title=text;return value;};
    projection.classList.toggle('has-error', Boolean(error || connectionError || cacheFailures.length) || visibleConnection === 'offline');
    projection.classList.toggle('is-connected',!pending&&!error&&!connectionError&&!cacheFailures.length&&visibleConnection==='connected');
    if(pending){
      projection.append(label(t(pending.phase==='uncertain'?'send.unknown':'send.confirming')));
      if(pending.recoverable){const retry=el('button','text-button',t('send.checkRetry'));retry.type='button';retry.disabled=visibleConnection!=='connected';retry.onclick=()=>confirmations.recover(pending);projection.append(retry);}
    }
    projection.append(label(error || (connectionError?.uiKey?t(connectionError.uiKey):connectionError) || (visibleConnection === 'connected' ? t(client.production?'connected':'preview') : t(visibleConnection))));
    if(cacheFailures.length){
      projection.append(label(t('storage.commandAccepted')));
      const retry=el('button','text-button',t('retry'));retry.type='button';
      retry.onclick=async()=>{retry.disabled=true;await Promise.allSettled(cacheFailures.map(async([receipt,save])=>{await save();if(sendCacheFailures.get(receipt)===save)sendCacheFailures.delete(receipt);}));if(!disposed)updateControls();};
      projection.append(retry);
    }
    if (directoryStorageFailed||visibleConnection === 'offline'||chatCacheFailed) {
      const retry = el('button', 'text-button', t('retry'));
      retry.type = 'button';
      retry.onclick = () => directoryStorageFailed?domains.directory.refresh().catch(reason=>{error=reason.storage?t('storage.failed'):reason.message;updateControls();}):loadSnapshot(true);
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
  let preferenceControls,preferenceContext;
  function renderPreferences() {
    if(client.production){
      const captured={...client.scope()},receivedEpoch=epoch,selection=selectionGeneration,boot=client.runtime.boot_id;
      const assistant=selectedAssistant,identity=assistant&&JSON.stringify([assistant.space_id,assistant.id,controlOf(assistant)]);
      const context=JSON.stringify([captured,receivedEpoch,selection,boot,identity,state.snapshot&&[ownerKey(state.snapshot),state.snapshot.boot_id,state.snapshot.running,state.snapshot.turn_id,!!state.snapshot.turn?.paused]]);
      // Retained directory data revokes execution authority, not the identity of
      // an open menu. Keep its existing actions focusable during revalidation;
      // fresh capability removal and storage/offline failures still remove them.
      // Reconciliation may remove a button or replace its handler. A retained
      // node/callback must still validate authority and its original destination.
      const current=()=>!disposed&&receivedEpoch===epoch&&selection===selectionGeneration&&boot===client.runtime.boot_id&&JSON.stringify(captured)===JSON.stringify(client.scope())&&captured.owner===selectedAssistant?.id&&captured.space===selectedAssistant?.space_id&&identity===JSON.stringify([selectedAssistant?.space_id,selectedAssistant?.id,controlOf(selectedAssistant)])&&!selectedAssistant?.offline;
      const retaining=preferences.open&&assistant?.retained&&!directoryStorageFailed&&context===preferenceContext&&current();
      const visible=(key,allowed)=>allowed||retaining&&!!preferences.querySelector('[data-view-key="'+key+'"]');
      preferences.classList.add('product-actions');const projection=preferences.cloneNode(false),close=button('close',t('close'));close.dataset.viewKey='close';close.onclick=()=>preferences.close();projection.append(close);
      const action=(key,label,run,valid=()=>true)=>{const choice=el('button','menu-destination',label);choice.type='button';choice.dataset.viewKey=key;if(!valid()){if(retaining)choice.setAttribute('aria-disabled','true');else choice.disabled=true;}choice.onclick=event=>{if(disposed||event.currentTarget.getAttribute('aria-disabled')==='true'||!valid())return;preferences.close();run();};projection.append(choice);};
      if(selectedAssistant&&visible('assistant-settings',can('assistant.configure')||can('assistant.profile')))action('assistant-settings',t('assistantSettings'),()=>{route='settings';pages.assistant(selectedAssistant);renderShell();},()=>current()&&(can('assistant.configure')||can('assistant.profile')));
      if(selectedAssistant&&visible('usage',can('assistant.usage')))action('usage',t('usage'),()=>{route='usage';pages.conversationUsage(selectedAssistant.name);renderShell();},()=>current()&&can('assistant.usage'));
      if(state.snapshot?.running&&visible('turn-control',can('chat.control'))){
        const snapshot=state.snapshot,target=snapshot.turn_id,paused=!!snapshot.turn?.paused;
        const valid=()=>current()&&can('chat.control')&&!chatCacheFailed&&connection==='connected'&&!!target&&state.snapshot?.running&&ownerKey(state.snapshot)===ownerKey(snapshot)&&state.snapshot.boot_id===snapshot.boot_id&&state.snapshot.turn_id===target&&!!state.snapshot.turn?.paused===paused;
        action('turn-control',t(paused?'chat.resume':'chat.pause'),async()=>{try{await client.controlTurn({user_message_id:target,action:paused?'resume':'pause'},captured);}catch(reason){if(valid()){error=reason.message;updateControls();}}},valid);
      }
      action('settings',t('settings'),()=>navigate('settings'));reconcile(preferences,projection);preferenceContext=context;return;
    }
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
          if (key === 'language') { locale = select.value; persist('pot-ui-locale', locale); }
          else { theme = select.value; persist('pot-ui-theme', theme); }
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
  function chatContext(){return {anchor:state.uiAnchor,position:messageWindow.snapshot()};}
  function layoutKey(){return JSON.stringify([history.clientWidth,locale,theme,'plex-1']);}
  function scheduleViewSave(){
    if(!chatSession||disposed||chatPreparing)return;
    clearTimeout(viewTimer);viewTimer=setTimeout(()=>rememberChatPosition(),100);
  }
  function rememberChatPosition(){
    clearTimeout(viewTimer);
    if(!state.snapshot||chatPreparing)return viewWrite;
    while(toolMemory.size>1280)toolMemory.delete(toolMemory.keys().next().value);
    activeView={position:messageWindow.snapshot(),layout:layoutKey(),heights:[...windowHeights],tools:[...toolMemory]};
    if(chatSession)viewWrite=chatSession.saveView(activeView).catch(reason=>{if(!disposed){error=t('chatCache.positionFailed');console.error('Chat view storage:',reason);updateControls();}});
    return viewWrite;
  }
  function cacheFailure(reason){
    if(disposed)return;
    chatCacheFailed=true;error=t('chatCache.failed');console.error('Chat cache:',reason);updateControls();
    if(!state.snapshot){initialLayout.cancel();chatPreparing=false;initialIntent=null;history.style.visibility='';history.removeAttribute('aria-busy');}
  }
  const initialLayout=createInitialChatLayout({history,
    ready:()=>!disposed&&chatPreparing&&!!state.snapshot&&!historyLoading&&chatShown,
    layout:()=>{editor.resize();renderMessagesNow(true);},
    position:()=>{if(initialIntent)applyChatPosition(initialIntent);},
    fontFallback:()=>root.classList.add('chat-font-fallback'),
    reveal:()=>{chatPreparing=false;initialIntent=null;history.style.visibility='';history.removeAttribute('aria-busy');scheduleViewSave();},
  });
  function prepareChat(){
    initialLayout.cancel();chatPreparing=true;
    initialIntent=chatPosition?.intent||{kind:'restore',position:messageWindow.snapshot()};
    history.style.visibility='hidden';history.setAttribute('aria-busy','true');
    root.classList.remove('chat-font-fallback');
  }
  function revealChat(){
    if(chatPreparing&&!initialLayout.pending&&state.snapshot&&!historyLoading&&chatShown)initialLayout.start();
  }
  const closeAssistantProfile = () => assistantProfile.hidePopover();
  window.addEventListener('resize', closeAssistantProfile);
  function toggleDirectory() {
    positionChat({kind:'restore',position:messageWindow.snapshot()});
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
    if(chatShown&&!showingChat){rememberChatPosition();cancelChatPosition();initialLayout.cancel();messageWindow.suspend();}
    if(!chatShown&&showingChat&&selectedAssistant&&!chatPreparing){prepareChat();if(!chatPosition)positionChat({kind:'restore',position:activeView?.position});}
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
    if (client.production && route === 'chat' && selectedAssistant && (can('assistant.configure')||can('assistant.profile'))) { const settings = button('sliders', t('assistantSettings')); settings.dataset.viewKey='assistant-settings';settings.onclick = openAssistantSettings; titlebar.append(settings); }
    if(!client.production){const cloud=el('span','icon-button');cloud.title=t('remote');cloud.append(icon('cloud'));titlebar.append(cloud);}
    const more = button('more', t(client.production ? 'chat.actions' : 'more'));more.dataset.viewKey='conversation-actions';
    more.setAttribute('aria-haspopup', 'dialog');
    more.onclick = () => { preferencesOpen = true; renderPreferences(); preferences.showModal(); };
    if(!client.production||route==='chat')titlebar.append(more);
    if(route==='tasks'&&pages?.taskOwner){const actions=button('more',locale==='zh-CN'?'任务操作':'Task actions');actions.dataset.viewKey='task-actions';actions.setAttribute('aria-haspopup','dialog');actions.onclick=()=>pages.taskMenu();titlebar.append(actions);}
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
    const nextDirectorySignature = JSON.stringify([locale,narrow,surface,selectedAssistant?.space_id,selectedAssistant?.id,spaces,assistants,state.snapshot?.assistants,replies.records()]);
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
    const add = button('plus', t(client.production ? 'assistantStart.title' : 'preview'));
    add.disabled = !client.production||!creationSpace()||creationSpace()?.connected===false;
    add.onclick = () => {directoryOpen=false;navigate('add-assistant');};
    heading.append(add);
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
    // Layout and position restoration must finish before the newly visible Chat paints.
    if(chatShown){editor.resize();renderMessagesNow();}
  }
  function selectedSequence(){const selection=getSelection();if(!selection?.rangeCount||selection.isCollapsed||!history.contains(selection.anchorNode))return null;const element=selection.anchorNode.nodeType===Node.ELEMENT_NODE?selection.anchorNode:selection.anchorNode.parentElement;return element?.closest('article.message')?.dataset.sequence||null;}
  function nearBottom() { return history.scrollHeight - history.clientHeight - history.scrollTop < 80; }
  let renderFrame=0,fullRender=false,chatProjection;
  function currentChatPosition(ticket){return !disposed&&ticket.generation===chatNavigation&&ticket.epoch===epoch;}
  function cancelChatPosition(){chatNavigation++;chatPosition=null;initialIntent=null;}
  function applyChatPosition(intent){
    if(intent.kind==='restore')messageWindow.restore(intent.position);
    else if(intent.kind==='latest')messageWindow.scrollToLatest();
    else messageWindow.locate({id:intent.id,expand:intent.expand});
  }
  function positionChat(intent){
    const ticket={kind:intent.kind,intent,epoch,generation:++chatNavigation};
    chatPosition=ticket;
    if(chatPreparing)initialIntent=intent;
    applyChatPosition(intent);
    renderMessages();return ticket;
  }
  function pauseFollowing(){cancelChatPosition();messageWindow.pause();}
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
  let backgroundProjection={key:'',items:[]};
  const projectedTasks=scope=>backgroundProjection.key===JSON.stringify([scope.space,scope.owner])?backgroundProjection.items:[];
  const openChatTask=(task,scope=client.scope())=>{route='tasks';pages.openTask({...task,_space_id:task._space_id||scope.space,assistant_id:task.assistant_id||scope.owner},{returnTo:{route:'chat',scope:{...scope}}});renderShell();};
  const backgroundTaskView=createBackgroundTasks({locale:()=>locale,
    onOpen:openChatTask,
    onToggle:change=>{cancelChatPosition();messageWindow.pause({ignoreToggle:!!change.expanded});pendingTaskToggle=change;renderMessages();},
  });
  let backgroundTasks=null,backgroundTaskKey='';
  function refreshBackgroundTasks(){
    if(!client.production||!selectedAssistant)return Promise.resolve();
    if(!can('task.list')){backgroundTasks?.close();backgroundTasks=null;backgroundTaskKey='';backgroundProjection={...backgroundProjection,items:backgroundProjection.items.map(task=>({...task,_stale:true}))};if(route==='chat')renderMessages();return Promise.resolve();}
    const scope={...client.scope()},key=JSON.stringify([scope.space,scope.owner]);
    if(!backgroundTasks||key!==backgroundTaskKey){backgroundTasks?.close();backgroundTaskKey=key;
      const stop=domains.tasks.watchActive(scope,snapshot=>{if(disposed||key!==JSON.stringify([client.scope().space,client.scope().owner])||!snapshot.data)return;const items=snapshot.data;backgroundProjection={key,items};messageIdentities(client).rememberTasks(items);pages.rememberTasks(items);if(route==='chat')renderMessages();});
      backgroundTasks={close:stop,run:()=>domains.tasks.active(scope).refresh()};
    }return backgroundTasks.run();
  }
  const disposeMessage=record=>{record.childrenWindow?.close();record.attachmentNode?.dispose?.();record.row.remove();messageNodes.delete(record.row.dataset.windowKey);};
  const requestMessageRender=resize=>{
    renderMessages(true);
  };
  let renderingSnapshot=null,renderingSending=null,renderingForest=null;
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
      if(!record.tool){record.tool=createToolCall({messageActor:()=>messageIdentities(client).actor(scope),resolveTask:id=>projectedTasks(scope).find(task=>task.id===id),onOpenTask:task=>openChatTask(task,scope),resolveMessageIdentity:address=>messageIdentities(client).resolve(address),memory:toolMemory,key,locale:()=>locale,machineLabel:id=>machinePresentation(client).label(id,scope,locale),onDecision:(approval,action)=>pages.decideApproval(approval,action),onInteract:event=>messageWindow.engage(event),onLocate:(call,turn)=>{const target=renderingForest.byID.get(turn+':'+call)?.message;if(target)jumpToMessage(target.id,{expand:true});},...markdownFiles(scope)});bubble.append(record.tool.element);}
      record.tool.update(message,{stacked:message.communicationStacked,offline:connection==='offline'||disconnected,children:forest.byID.get(message.tool.turn_id+':'+message.tool.id)?.children.map(entry=>entry.message)||[]});
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
      if(messageWindow.revealing&&messageWindow.visibilityPath.has(message.id)||messageWindow.destination?.expand&&messageWindow.destination.id===message.id)record.tool.element.open=true;
      if(children.length||record.childrenWindow){
        if(!record.childrenWindow){record.childrenWindow=createCommunicationWindow({onOpenTask:openChatTask,locale:()=>locale,actor:()=>messageIdentities(client).actor(client.scope?.()),resolveIdentity:address=>messageIdentities(client).resolve(address),memory:toolMemory,scope:()=>JSON.stringify(client.scope?.()),scroller:history,host:record.tool.children,nested:true,render:renderMessage,dispose:disposeMessage,requestRender:requestMessageRender,controller:messageWindow,heights:windowHeights});record.tool.element.addEventListener('toggle',()=>renderMessages());}
        record.childrenWindow.setMessages(record.tool.element.open?children:[]);
      }
    }
    return record;
  };
  const viewportObserver=new ResizeObserver(()=>requestMessageRender(true));viewportObserver.observe(history);
  function renderMessages(viewportOnly=false){fullRender ||= !viewportOnly;if(disposed||renderFrame)return;renderFrame=requestAnimationFrame(()=>{renderFrame=0;const viewportOnly=!fullRender;fullRender=false;renderMessagesNow(viewportOnly);});}
  function renderMessagesNow(viewportOnly=false) {
    if(!chatShown)return;
    const navigation=chatPosition?.epoch===epoch?chatPosition:null;
    const anchor=messageWindow.anchor;
    const snapshot=state.snapshot;
    const sending=state.sends.get(editorOwner||(snapshot&&ownerKey(snapshot)));
    const activeTurn=snapshot?.running?(snapshot.messages.find(message=>message.id===snapshot.turn_id)?.request_id||snapshot.turn_id):null;
    const offline=connection!=='connected'||disconnected||selectedAssistant?.offline;
    if(!chatProjection||chatProjection.snapshot!==snapshot||chatProjection.sending!==sending||chatProjection.phase!==sending?.phase||chatProjection.detail!==sending?.detail||chatProjection.offline!==offline||chatProjection.activeTurn!==activeTurn){
      const messages=projectScriptRuns(projectChatReplies(snapshot,sending,{offline}),{activeTurn});
      const forest=toolForest(messages),roots=messages.filter(message=>!message.tool?.parent_id||!forest.byID.has(message.tool.turn_id+':'+message.tool.parent_id));
      const parents=new Map(messages.flatMap(message=>{const parent=message.tool?.parent_id&&forest.byID.get(message.tool.turn_id+':'+message.tool.parent_id)?.message;return parent?[[message.id,parent.id]]:[];}));
      chatProjection={snapshot,sending,phase:sending?.phase,detail:sending?.detail,offline,activeTurn,messages,forest,roots,parents};
    }
    const {messages:visibleMessages,forest,roots,parents}=chatProjection;
    renderingSnapshot=snapshot;renderingSending=sending;renderingForest=forest;
    historyStatus.hidden=!chatSession?.hasHistory||!snapshot;
    historyStatus.disabled=historyLoading||!historyFailure&&!snapshot?.history?.has_more;
    historyStatus.textContent=t(historyLoading?'history.loading':historyFailure|| (snapshot?.history?.has_more?'history.earlier':'history.start'));
    const tasks=client.production?projectedTasks(client.scope()):[];
    if(client.production&&!viewportOnly)backgroundTaskView.update(JSON.stringify(client.scope()),tasks,(id,phase)=>pages.taskNeedsApproval(id,phase));
    // The task region stays outside recycled message rows and shares the chat scroller.
    if(client.production&&tasks.length){
      if(backgroundTaskView.element.parentElement!==messageColumn)messageColumn.insertBefore(backgroundTaskView.element,chatApprovals);
    }else backgroundTaskView.element.remove();
    if(pendingTaskToggle&&!pendingTaskToggle.expanded&&messageWindow.position?.anchors[0]?.key.startsWith('task:')){
      const retained=new Set([...backgroundTaskView.element.querySelectorAll('[data-scroll-anchor]')].map(node=>node.dataset.scrollAnchor));
      if(!retained.has(messageWindow.position.anchors[0].key))messageWindow.locate({key:'background-tasks-heading',align:'nearest'});
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
      locateApprovals='';cancelChatPosition();messageWindow.locate({key:'chat-approvals',align:'nearest'});
    }
    messageWindow.setMessages(roots,{parents});
    if(navigation&&snapshot&&!historyLoading&&navigation.kind!=='message')chatPosition=null;
    latest.hidden=messageWindow.following;
    state.uiAnchor=messageWindow.following?null:selectedSequence()||messageWindow.anchor?.sequence||null;
    revealChat();
  }
  function restoreSavedPosition(position){
    positionChat({kind:'restore',position});
    state.uiAnchor=messageWindow.anchor?.sequence||null;
  }
  function pendingSend(){return state.snapshot&&state.pending.has(ownerKey(state.snapshot));}
  function readyFromSnapshot(){connection='connected';connectionError='';disconnected=false;if(chatCacheFailed){chatCacheFailed=false;error='';}}
  async function applySnapshot(snapshot, receivedEpoch = epoch, receivedMessages=snapshot.messages,{live=true}={}) {
    if (disposed || receivedEpoch !== state.epoch) return;
    if(state.snapshot&&validSnapshot(snapshot)&&state.snapshot.boot_id!==snapshot.boot_id){
      state.snapshot=null;pages?.clearApprovals();domains.changed([],{space:client.scope().space,boot:snapshot.boot_id});
      pages?.refresh([],{space:client.scope().space,boot:snapshot.boot_id});
    }
    if (state.snapshot && validSnapshot(snapshot) && ownerKey(snapshot) !== ownerKey(state.snapshot)) {
      connection = 'offline'; error = t('identityChanged'); updateControls(); return;
    }
    const readingBeforeReset=snapshot.kind==='reset'&&!messageWindow.following?messageWindow.anchor:null;
    if(chatSession&&readingBeforeReset&&!snapshot.messages.some(message=>message.id===readingBeforeReset.id&&!message.context_only)){
      const ticket={generation:chatNavigation,epoch},generation=++historyGeneration;
      let finish;const recovery=new Promise(resolve=>{finish=resolve;});historyRecovery=recovery;
      historyLoading=true;connection='connecting';updateControls();
      try{
        const restored=await chatSession.history({around:readingBeforeReset.id},{around:true,signal:historyAbort.signal,valid:()=>receivedEpoch===epoch&&generation===historyGeneration&&currentChatPosition(ticket)});
        if(!restored||receivedEpoch!==epoch||generation!==historyGeneration||!currentChatPosition(ticket))return;
        snapshot=restored;
      }catch(reason){if(receivedEpoch===epoch&&generation===historyGeneration&&currentChatPosition(ticket)){historyFailure=reason?.status===404?'history.missing':'history.failed';if(reason.storage)cacheFailure(reason);}return;}
      finally{finish();if(historyRecovery===recovery)historyRecovery=null;if(receivedEpoch===epoch&&generation===historyGeneration){historyLoading=false;renderMessages();}}
    }
    const initialSnapshot = !state.snapshot;
    if(live&&validSnapshot(snapshot)){awaitingSnapshot=false;}
    const pendingReceipt=state.pending.get(ownerKey(snapshot));
    let accepted;try{accepted=acceptSnapshot(state,snapshot,receivedEpoch,{committed:true,receivedMessages});}catch(reason){if(reason.message==='conversation_gap'){loadSnapshot(true);return;}throw reason;}
    if (!accepted){if(state.snapshot&&validSnapshot(snapshot)&&state.snapshot.cursor===snapshot.cursor){if(live)readyFromSnapshot();updateControls();}return;}
    if(initialSnapshot&&!chatPreparing)prepareChat();
    if(pendingReceipt && !state.pending.has(pendingReceipt.key))confirmations.confirmed(pendingReceipt);
    if(live)readyFromSnapshot();
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
  async function loadSnapshot(reattach = false) {
    if (disposed) return;
    if(reattach){const retained=state.snapshot;historyGeneration++;historyLoading=false;epoch=beginSubscription(state);if(retained)acceptSnapshot(state,retained,epoch,{committed:true});disconnected=false;return attach();}
    const receivedEpoch = epoch;
    connection = 'connecting'; error = ''; connectionError = ''; updateControls();
    try {
      await chatSession?.refresh();
    } catch (reason) {
      if (!disposed && receivedEpoch === state.epoch) { connection = 'offline'; error = reason.message; updateControls(); }
    }
  }
  function attach() {
    unsubscribe();
    const receivedEpoch = epoch;
    chatSession ||= domains.chat.open(client.scope?.()||{},{surface:viewSurface,context:chatContext});
    awaitingSnapshot=!!client.production;connection='connecting';updateControls();
    unsubscribe = chatSession.watch({snapshot:(snapshot,received,options)=>applySnapshot(snapshot,receivedEpoch,received,options),failed:reason=>{if(disposed||receivedEpoch!==epoch)return;if(['conversation_gap','history_gap'].includes(reason.message))loadSnapshot(true);else if(reason.uiKey==='identityChanged'){connection='offline';error=t('identityChanged');updateControls();}else cacheFailure(reason);},removed:()=>loadDirectory(),connection:(value, reason) => {
      if (disposed || receivedEpoch !== state.epoch) return;
      if (value === 'offline') {disconnected = true;connectionError = reason?.uiKey?reason:reason?.message || t('streamDisconnected');}
      if (value === 'connected') connectionError = '';
      if(value==='connecting'&&client.production)awaitingSnapshot=true;
      if (value === 'connected' && disconnected && client.desktop) { disconnected = false; loadSnapshot(true); }
      else { connection = value==='connected'&&awaitingSnapshot?'connecting':value; updateControls(); }
    }});
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
    if(chatSession)await chatSession.retire();
    if(chatShown)await rememberChatPosition();
    if(selection!==selectionGeneration||disposed)return;
    historyAbort=new AbortController();
    chatSession?.close().catch(()=>{});chatSession=null;cancelChatPosition();prepareChat();
    pages?.clearApprovals();locateApprovals=item.pending_approvals?JSON.stringify({owner:item.id,space:item.space_id||''}):'';selectedAssistant=item;
    client.setScope({owner:item.id,space:item.space_id||''});updates?.refresh(client.scope());
    chatSession=domains.chat.open(client.scope(),{surface:viewSurface,context:chatContext});
    epoch=beginSubscription(state);const receivedEpoch=epoch;editorOwner=null;activeView=null;route='chat';if(!initial)directoryOpen=false;
    pendingTaskToggle=null;chatProjection=null;renderingSnapshot=null;renderingSending=null;renderingForest=null;historyScrollTime=0;historySpeed=0;historyLatency=200;messageWindow.reset();
    messageWindow.setMessages([]);windowHost.replaceChildren();messageColumn.replaceChildren(historyStatus,windowHost,chatApprovals);messageNodes.clear();windowHeights.clear();toolMemory.clear();
    historyGeneration++;historyLoading=false;historyFailure='';state.uiAnchor=null;error='';chatCacheFailed=false;connectionError='';disconnected=false;connection='connecting';
    renderShell();
    try{
      const cached=await chatSession.query();
      if(disposed||selection!==selectionGeneration||receivedEpoch!==epoch)return;
      activeView=cached?.view||null;
      if(activeView?.layout===layoutKey())for(const [key,height]of activeView.heights||[])windowHeights.set(key,height);
      for(const [key,value]of activeView?.tools||[])toolMemory.set(key,value);
      restoreSavedPosition(activeView?.position);
      if(cached?.snapshot){acceptSnapshot(state,cached.snapshot,epoch,{committed:true});syncEditor();}
      if(client.production&&record)saveNavigation('chat',initial);
      renderShell();attach();pages?.permissions();refreshBackgroundTasks();
      const anchor=messageWindow.anchor;
      if(anchor&&!state.snapshot?.messages.some(message=>message.id===anchor.id))await readHistory({around:anchor.id},{around:true});
      else if(messageWindow.following&&state.snapshot?.history?.has_newer)await readHistory({},{around:true});
    }catch(reason){if(!disposed&&receivedEpoch===epoch){cacheFailure(reason);}}
  }
  function creationSpace(){return selectedAssistant?spaces.find(space=>space.id===selectedAssistant.space_id):spaces.find(space=>space.kind==='local')||spaces[0];}
  function clearSelectedAssistant() {
    initialLayout.cancel();
    const space=selectedAssistant?.space_id||client.scope().space;
    selectionGeneration++;unsubscribe();historyAbort.abort();historyAbort=new AbortController();chatSession?.close().catch(()=>{});chatSession=null;epoch=beginSubscription(state);activeView=null;selectedAssistant=null;editorOwner=null;pendingSelection=null;
    client.setScope({space,owner:''});pages.clearApprovals();completions?.reset();
    clearTimeout(viewTimer);cancelChatPosition();messageWindow.reset();windowHeights.clear();toolMemory.clear();chatProjection=null;renderingSnapshot=null;renderingSending=null;renderingForest=null;
    messageWindow.setMessages([]);messageNodes.clear();windowHost.replaceChildren();messageColumn.replaceChildren(historyStatus,windowHost,chatApprovals);
    input.value='';directoryOpen=true;error='';
  }
  const stopResources=domains.directory.watch(snapshot=>{if(disposed)return;if(snapshot.error){directoryStorageFailed=!!snapshot.error.storage;error=t(directoryStorageFailed?'storage.failed':'loading.stale');updateControls();}else if(snapshot.data){if(directoryStorageFailed){directoryStorageFailed=false;if(error===t('storage.failed'))error='';}loadDirectory();}});
  const directoryQueue=createRefreshQueue(()=>readDirectory(),{active:()=>!disposed});
  function loadDirectory(){return directoryQueue.run();}
  async function readDirectory() {
    if (!client.production) return;
    const notificationEpoch=notifications?.epoch;
    const previousMachines=machinePresentation(client).revision;
    const ticket = ++directoryEpoch,previousSpaces=JSON.stringify(spaces),previousOwners=JSON.stringify(assistants.map(item=>[item.space_id,item.id,item.name,item.profile_name,item.avatar,item.origin,item.offline,item.capabilities]));
    try {
      const directory=await domains.directory.query();if(ticket!==directoryEpoch||disposed)return;
      spaces=directory.spaces;assistants=directory.assistants;client.spaces=spaces;client.assistants=assistants;
      if(!selectedAssistant){client.setScope({space:directory.activeSpace,owner:''});updates?.refresh(client.scope());}
      const changed=[];if(previousMachines!==machinePresentation(client).revision)changed.push('machines');if(previousSpaces!==JSON.stringify(spaces))changed.push('spaces');if(previousOwners!==JSON.stringify(assistants.map(item=>[item.space_id,item.id,item.name,item.profile_name,item.avatar,item.origin,item.offline,item.capabilities])))changed.push('assistants');if(changed.length)pages.refresh(changed,{domainNotified:true});
      for(const item of assistants){const arrival=replies.observe(controlOf(item),item.id,item.reply_id||'',item.reply_count||0,!item.offline);if(arrival.arrived&&route==='chat'&&document.visibilityState!=='hidden'&&!reducedMotion.matches){reactions.set(replyKey(controlOf(item),item.id),performance.now());setTimeout(()=>{if(!disposed){directorySignature='';renderShell();}},650);}}saveReads();
      if(directory.partial)error=t('partialOffline');else if(error===t('partialOffline'))error='';
      if(selectedAssistant&&!pages.editing&&!assistants.some(item=>item.id===selectedAssistant.id&&item.space_id===selectedAssistant.space_id)){
        const missing=selectedAssistant;try{const confirmed=await domains.directory.confirm(missing);if(ticket!==directoryEpoch||disposed)return;if(confirmed){assistants=[...assistants,confirmed];client.assistants=assistants;}else clearSelectedAssistant();}catch(reason){if(reason.storage){directoryStorageFailed=true;error=t('storage.failed');}else error=t('partialOffline');}
      }
      if (selectedAssistant){const taskAccess=can('task.list');selectedAssistant = assistants.find(item => item.id===selectedAssistant.id&&item.space_id===selectedAssistant.space_id) || selectedAssistant;if(selectedAssistant.offline)connection='offline';if(taskAccess!==can('task.list'))refreshBackgroundTasks();}
      renderShell();renderMessages();
      if(!assistants.length&&route==='chat'&&!pages.editing){
        const available=!directory.partial&&spaces.some(space=>space.connected!==false);
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
    } catch (reason) { if (ticket===directoryEpoch&&!disposed) {error = reason.storage?t('storage.failed'):reason.message;if(reason.storage)directoryStorageFailed=true;else connection='offline';updateControls();if(!assistants.length){const empty=el('div','empty-chat',error);const retry=el('button','control',t('retry'));retry.onclick=()=>domains.directory.refresh().catch(reason=>{error=reason.storage?t('storage.failed'):reason.message;updateControls();});empty.append(document.createElement('br'),retry);messageColumn.replaceChildren(empty);}} }
  }
  const preferencesChanged = event => {
    if(event.detail.key==='pot-ui-locale') locale=event.detail.value;
    if(event.detail.key==='pot-ui-theme') theme=event.detail.value;
    renderShell();renderMessages();window.dispatchEvent(new Event('pot:locale'));
  };
  window.addEventListener('pot:preference',preferencesChanged);
  const forgotten=()=>{if(client.production&&!client.desktop)location.reload();};window.addEventListener('pot:cache-forgotten',forgotten);
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
      domains.changed(keys,source);
 if(relevantChange(keys,['updates','spaces','runtime'],client.scope().space,source))updates?.refresh(client.scope());
      if(source.boot&&state.snapshot&&(!source.space||source.space===client.scope().space)&&state.snapshot.boot_id!==source.boot)loadSnapshot(true);
      if(source.reset){notifications?.refresh({reset:true,space:source.space});loadDirectory();}
      else if(relevantChange(keys,['task-outcomes','permissions']))notifications?.refresh({topics:keys,space:source.space});
      if(relevantChange(keys,['spaces','assistants','assistant-mail','agent-links','conversation','runtime','permissions','tasks','runner']))loadDirectory();
      if(relevantChange(keys,['tasks','runner'],client.scope().space,source))refreshBackgroundTasks();
      pages.refresh(keys,{...source,domainNotified:true});if(relevantChange(keys,['permissions','spaces','runtime'],pages.approvalSpace,source))pages.permissions();
    },{space:''});
  }
  if(client.production){attachChanges();loadDirectory();updates?.refresh(client.scope());}else attach();
  const stopResume=bindResumeRecovery(async()=>{
    if(disposed)return;
    const snapshot=(client.desktop||!client.production)&&(selectedAssistant||!client.production)?loadSnapshot(true):Promise.resolve();
    if(client.production){if(client.desktop)attachChanges();pages.refresh();pages.permissions();refreshBackgroundTasks();}
    await Promise.all([snapshot,loadDirectory()]);
  });
  // Receipt checks own their coalescing and cancellation. Directory refreshes
  // may still be pending when offline/online arrives in the same event burst.
  const resumeSends=()=>{if(document.visibilityState!=='hidden')confirmations.resume();};
  const pageHidden=()=>{rememberChatPosition();confirmations.suspend();};
  const persistChat=()=>{if(document.visibilityState==='hidden')pageHidden();else resumeSends();};
  const offline=()=>confirmations.suspend({offline:true});
  for(const name of ['focus','pageshow','online'])window.addEventListener(name,resumeSends);
  window.addEventListener('pagehide',pageHidden);window.addEventListener('offline',offline);document.addEventListener('visibilitychange',persistChat);
  return () => {
    if(disposed)return;
    rememberChatPosition();clearTimeout(viewTimer);initialLayout.cancel();historyAbort.abort();
    window.removeEventListener('pagehide',pageHidden);window.removeEventListener('offline',offline);document.removeEventListener('visibilitychange',persistChat);
    for(const name of ['focus','pageshow','online'])window.removeEventListener(name,resumeSends);
    chatSession?.close().catch(()=>{});
    disposed = true;updates?.close();confirmations.close();cancelAnimationFrame(renderFrame);viewportObserver.disconnect();stopReading();messageWindow.close();stopResources?.();releaseDomains();stopResume();taskClock.dispose();onboarding?.dispose();notifications?.dispose();directoryQueue.close();backgroundTasks?.close();backgroundTaskView.close();directoryEpoch++; unsubscribe(); stopChanges(); pages?.dispose(); editor.destroy();
    stopMessageSelection();completions?.dispose();attachmentUI?.destroy();client.releaseAttachments?.();
    stopRead();reducedMotion.removeEventListener('change',reducedChanged);window.removeEventListener('focus',focusRead);document.removeEventListener('visibilitychange',focusRead);window.removeEventListener('storage',readStorage);
    root.removeEventListener('pointerdown',pointerDown);root.removeEventListener('pointermove',pointerMove);root.removeEventListener('pointerup',pointerUp);root.removeEventListener('pointercancel',pointerCancel);
    window.removeEventListener('pot:preference',preferencesChanged);window.removeEventListener('pot:cache-forgotten',forgotten);
    document.removeEventListener('keydown', keydown);window.removeEventListener('popstate',popNavigation);
    media.removeEventListener('change', mediaChanged);
    window.removeEventListener('resize', closeAssistantProfile);
    stopViewport();
    root.replaceChildren();
  };
}
