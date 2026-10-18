import {bindMessageSelection,renderMessageReferences,referenceProblem} from './message-references.mjs';
import {createSystemNotifications} from '../adapters/notifications.mjs';
import {createToolCall} from './tool-call.mjs';
import {toolForest} from '../state/tool-calls.mjs';
import {projectScriptRuns} from '../state/script-runs.mjs';
import {avatar} from './avatar.mjs';
import {icon} from './icons.mjs';
import {messages,translate} from '../locales/messages.mjs';
import {acceptSnapshot, beginSend, beginSubscription, createConversationState, draft, draftReferences, setDraftReferences, ownerKey, setDraft, settleSend, validSnapshot} from '../state/conversation.mjs';
import {bindComposer} from './composer.mjs';
import {createAttachmentComposer,renderMessageAttachments,failedMessageInput} from './attachments.mjs';
import {renderMarkdown} from './markdown.mjs';
import {createPages} from './pages.mjs';
import {createReplies,replyKey} from '../state/replies.mjs';
import {bindCompletions} from './completions.mjs';
import {projectChatReplies} from '../state/chat-replies.mjs';
import {createReplyBubble} from './reply.mjs';
import {reconcile} from './reconcile.mjs';
import {createRefreshQueue,relevantChange} from '../state/refresh.mjs';
import {taskElapsed,bindTaskClock} from './task-time.mjs';
import {bindResumeRecovery} from '../state/resume.mjs';
import {bindViewport} from './viewport.mjs';

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
  const positions = new Map(), cachedSnapshots = new Map();
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
    const value=avatar(size,item?.request_id||item?.id||'main'),started=reactions.get(replyKey(controlOf(item),item?.id||''));
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
  function jumpToMessage(id){const record=[...messageNodes.values()].find(record=>record.row.dataset.messageId===id);if(!record)return;record.row.scrollIntoView({block:'center',behavior:reducedMotion.matches?'instant':'smooth'});record.row.classList.remove('reference-target');requestAnimationFrame(()=>record.row.classList.add('reference-target'));}
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
  async function submit() {
    if ((!client.production&&state.snapshot?.running) || connection !== 'connected' || editorOwner !== (state.snapshot && ownerKey(state.snapshot))) return;
    if(attachmentUI?.problem())return;
    const refs=attachmentUI?.refs()||[];
    const receipt = beginSend(state, requestID(),{allowRunning:client.production,attachments:refs,retryUserMessageID:retryAttachmentInput()});
    if (!receipt) return;
    if(refs.length||receipt.request.references?.length)attachmentAttempts.set(receipt.key,{requestID:receipt.request.request_id,cursor:state.snapshot.cursor});
    error = '';
    updateControls();
    try {
      const result = await client.send(receipt.request);
      if (typeof result.message_id !== 'string' || typeof result.duplicate !== 'boolean') throw Error('Invalid message receipt');
      if(settleSend(state, receipt, true))attachmentUI?.clearSubmitted(receipt.key,receipt.request.attachments||[]);
    } catch (reason) {
      if (settleSend(state, receipt, false, reason)&&receipt.request.user_message_id) error = reason.message || String(reason);
    }
    if (!disposed) { syncEditor();restoreFailedAttachments(); updateControls(); }
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
    const pending = snapshot && state.pending.has(ownerKey(snapshot));
    const running = snapshot?.running;
    const stopping=running&&(!client.production||(!draft(state).trim()&&!attachmentUI?.items().length));
    const label = stopping ? (cancelPending ? t('stopping') : t('stop')) : running?(locale==='zh-CN'?'发送指导':'Send guidance'):t('send');
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
    projection.classList.toggle('is-connected',!error&&!connectionError&&visibleConnection==='connected');
    const replyOwnsConnection=state.snapshot?.running||state.sends.has(editorOwner);
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
      const item = button(name, t(name), inMenu ? 'menu-destination' : `rail-button${name === 'settings' ? ' rail-bottom' : ''}`);
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
  function nearBottom() { return history.scrollHeight - history.clientHeight - history.scrollTop < 80; }
  function scrollBottom() { history.scrollTop = history.scrollHeight; latest.hidden = true; }
  history.addEventListener('scroll', () => { latest.hidden = nearBottom(); }, {passive: true});
  latest.onclick = scrollBottom;
  const messageNodes = new Map(),toolMemory=new Map();
  const taskCache=new Map();
  let backgroundTasks=null,backgroundTaskKey='';
  function refreshBackgroundTasks(){
    if(!client.production||!selectedAssistant)return Promise.resolve();
    const scope={...client.scope()},key=JSON.stringify([scope.space,scope.owner]);
    if(!backgroundTasks||key!==backgroundTaskKey){
      backgroundTasks?.close();backgroundTaskKey=key;
      backgroundTasks=createRefreshQueue(async()=>{const value=await client.request('/api/v1/tasks/query?scope=active&limit=50',{...scope,signal:abort.signal});if(disposed||key!==JSON.stringify([client.scope().space,client.scope().owner]))return;taskCache.set(key,(value.items||[]).filter(task=>['running','waiting_approval'].includes(task.phase)).map(task=>({...task,_space_id:scope.space,assistant_id:scope.owner})));pages.rememberTasks(taskCache.get(key));if(route==='chat')renderMessages();},{active:()=>!disposed&&key===JSON.stringify([client.scope().space,client.scope().owner])});
    }return backgroundTasks.run();
  }
  function renderMessages() {
    const follow = nearBottom();
    const previousTop = history.scrollTop;
    const snapshot = state.snapshot||(client.production&&selectedAssistant?cachedSnapshots.get(snapshotKey(selectedAssistant.space_id||'',selectedAssistant.id)):null);
    const sending=state.sends.get(editorOwner||(snapshot&&ownerKey(snapshot)));
    const activeTurn=snapshot?.running?(snapshot.messages.find(message=>message.id===snapshot.turn_id)?.request_id||snapshot.turn_id):null;
    const visibleMessages=projectScriptRuns(projectChatReplies(snapshot,sending,{offline:connection==='offline'||disconnected||selectedAssistant?.offline}),{activeTurn});
    const ids = new Set(visibleMessages.map(message => message.viewKey));
    const forest=toolForest(visibleMessages);
    for (const [id, record] of messageNodes) if (!ids.has(id)) { record.row.remove(); messageNodes.delete(id); }
    let position = 0;
    for (const message of visibleMessages) {
      let record = messageNodes.get(message.viewKey);
      if (!record) {
        const row = el('article', `message role-${message.role}`);
        const bubble = el('div', 'message-body');
        row.append(bubble);
        record = {row, bubble};
        messageNodes.set(message.viewKey, record);
      }
      const {row, bubble} = record;
      row.dataset.messageId=message.id;
      if(message.role==='tool'&&message.tool){
        const scope=client.scope?.()||{},key=JSON.stringify([scope.space,scope.owner,message.id]);
        if(!record.tool){record.tool=createToolCall({memory:toolMemory,key,locale:()=>locale,onDecision:(approval,action)=>pages.decideApproval(approval,action)});bubble.append(record.tool.element);}
        record.tool.update(message,{offline:connection==='offline'||disconnected,children:forest.byID.get(message.tool.turn_id+':'+message.tool.id)?.children.map(entry=>entry.message)||[]});
      }else if(message.role==='assistant'){
        record.reply ||= createReplyBubble(bubble);
        const scope=client.scope?.();
        record.reply.update(message,{t,downloadFile:client.downloadFile?async({task,path})=>{try{await client.downloadFile(task,path,scope);}catch(reason){error=reason.message;updateControls();}}:undefined,
          onSettings:async()=>{if(!await pages?.leave())return;route='settings';await pages.showProviders();renderShell();},
          onReconnect:()=>loadSnapshot(true),onRetrySend:submit,canRetrySend:connection==='connected'&&!pendingSend()&&draft(state)===sending?.request.text});
        record.text=message.text;record.role=message.role;
      }else if (record.text !== message.text || record.role !== message.role) {
        bubble.replaceChildren();
        if (message.role === 'tool') bubble.append(el('pre','',message.text));
        else if (message.role === 'system') bubble.textContent = message.text;
        else {const scope=client.scope?.(),prose=el('div','message-prose');prose.append(renderMarkdown(message.text,{downloadFile:client.downloadFile?async({task,path})=>{try{await client.downloadFile(task,path,scope);}catch(reason){error=reason.message;updateControls();}}:undefined}));bubble.append(prose);}
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
        record.attachmentNode?.remove();record.attachmentSignature=attachmentSignature;
        if(message.attachments?.length){record.attachmentNode=renderMessageAttachments(message.attachments,{client,caps:state.snapshot?.agent?.attachments,scope:{...client.scope()},locale:()=>locale,onError:reason=>{error=reason.message;updateControls();}});bubble.append(record.attachmentNode);}
      }
      if ((message.role === 'assistant'||message.role==='user') && message.text && !record.copy) {
        const copy = button('copy',t('copy'),'copy-message');
        copy.type = 'button';
        copy.onclick = async () => {
          try { await navigator.clipboard.writeText(record.text); copy.title = t('copied'); }
          catch (reason) { error = reason.message; updateControls(); }
        };
        row.append(copy); record.copy = copy;
      }
      if (record.copy) {record.copy.title=t('copy');record.copy.setAttribute('aria-label',t('copy'));}
      if (message.role==='tool'&&!message.tool&&message.status === 'streaming' && !record.dot) {
        record.dot = el('span', 'activity-dot'); row.append(record.dot);
      } else if (message.status !== 'streaming' && record.dot) { record.dot.remove(); record.dot = null; }
      record.dot?.setAttribute('aria-label', t('working'));
      if(message.role==='user'&&message.steering&&message.status==='accepted'){
        if(!record.received){record.received=el('span','guidance-received');record.received.append(icon('clock'));row.append(record.received);}record.received.title=locale==='zh-CN'?'已接收':'Received';record.received.setAttribute('aria-label',record.received.title);
      }else if(record.received){record.received.remove();record.received=null;}

      const parent=message.tool&&forest.byID.get(message.tool.turn_id+':'+message.tool.parent_id);
      if(parent&&parent.children.some(entry=>entry.message.id===message.id)&&messageNodes.get(parent.message.viewKey)?.tool){const host=messageNodes.get(parent.message.viewKey).tool.children;if(row.parentElement!==host)host.append(row);}
      else {if(messageColumn.children[position]===chatApprovals)position++;if (messageColumn.children[position] !== row) messageColumn.insertBefore(row, messageColumn.children[position] || null);position++;}
    }
    const lastAssistant=visibleMessages.findLast(message=>message.role==='assistant');
    for(const [id,record]of messageNodes){if(id!==lastAssistant?.viewKey){record.tasks?.remove();record.tasks=null;}}
    if(client.production&&lastAssistant){
      const record=messageNodes.get(lastAssistant.viewKey),tasks=[...(taskCache.get(JSON.stringify([client.scope().space,client.scope().owner]))||[])].sort((a,b)=>Number(pages.taskNeedsApproval(b.id,b.phase))-Number(pages.taskNeedsApproval(a.id,a.phase)));
      if(tasks.length){
        const strip=el('div','background-tasks');strip.setAttribute('aria-label',locale==='zh-CN'?'正在执行的后台任务':'Executing background tasks');
        for(const task of tasks.slice(0,3)){
          const open=el('button','background-task');open.type='button';open.dataset.viewKey=JSON.stringify([task._space_id,task.assistant_id,task.id]);open.title=task.title;
          const waiting=pages.taskNeedsApproval(task.id,task.phase),meta=el('span','background-task-meta');open.classList.toggle('needs-approval',waiting);
          open.append(el('span','task-activity-dot'),el('span','background-task-title',task.title));
          if(waiting)meta.append(el('span','background-task-status',t('tool.waiting_approval')));
          const time=taskElapsed(task.started_at_ms,Date.now(),locale);
          if(time){
            if(waiting){const separator=el('span','','·');separator.setAttribute('aria-hidden','true');meta.append(separator);}
            const elapsed=el('span','background-task-elapsed',time);elapsed.dataset.startedAt=task.started_at_ms;elapsed.setAttribute('role','timer');elapsed.setAttribute('aria-live','off');
            elapsed.title=locale==='zh-CN'?'自首次开始执行起的总耗时，包含中间等待':'Total time since execution first started, including waits';meta.append(elapsed);
          }
          if(meta.childNodes.length)open.append(meta);
          open.onclick=()=>{const returnTo={route:'chat',scope:client.scope()};route='tasks';pages.openTask(task,{returnTo});renderShell();};strip.append(open);
        }
        if(tasks.length>3){const more=el('button','text-button',locale==='zh-CN'?`还有 ${tasks.length-3} 项任务`:`${tasks.length-3} more tasks`);more.onclick=()=>navigate('tasks');strip.append(more);}
        if(record.tasks)reconcile(record.tasks,strip);else{record.row.append(strip);record.tasks=strip;}
      }else{record.tasks?.remove();record.tasks=null;}
    }
    taskClock.refresh();
    if (snapshot && !visibleMessages.length) {
      if(!messageColumn.querySelector('.empty-chat'))messageColumn.append(el('p', 'empty-chat', t('first')));
    }else if(visibleMessages.length){
      messageColumn.querySelector('.empty-chat')?.remove();
    }
    const approvalAnchor=lastAssistant&&messageNodes.get(lastAssistant.viewKey).row;
    if(approvalAnchor?approvalAnchor.nextElementSibling!==chatApprovals:messageColumn.lastElementChild!==chatApprovals){const focused=chatApprovals.contains(document.activeElement)?document.activeElement:null;if(approvalAnchor)approvalAnchor.after(chatApprovals);else messageColumn.append(chatApprovals);focused?.focus({preventScroll:true});}
    pages?.renderApprovals();
    if(!chatApprovals.hidden&&locateApprovals===JSON.stringify(client.scope?.())){locateApprovals='';requestAnimationFrame(()=>{if(!disposed&&route==='chat')chatApprovals.scrollIntoView({block:'nearest',behavior:reducedMotion.matches?'instant':'smooth'});});}
    if (follow) requestAnimationFrame(() => { if (!disposed) scrollBottom(); });
    else { history.scrollTop = previousTop; latest.hidden = false; }
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
    const initialSnapshot = !state.snapshot;
    if(validSnapshot(snapshot)){awaitingSnapshot=false;restoredSnapshot=false;}
    if (!acceptSnapshot(state, snapshot, receivedEpoch)){if(state.snapshot&&validSnapshot(snapshot)&&state.snapshot.cursor===snapshot.cursor){connection='connected';updateControls();}return;}
    connection = 'connected';
    connectionError = '';
    if(client.production)cachedSnapshots.set(snapshotKey(),snapshot);
    const restorePosition = editorOwner !== ownerKey(snapshot) ? positions.get(ownerKey(snapshot)) : undefined;
    syncEditor();
    restoreFailedAttachments();
    if(markVisibleRead())renderShell();
    renderMessages(); if (restorePosition !== undefined) requestAnimationFrame(() => {history.scrollTop=restorePosition; latest.hidden=nearBottom();}); updateControls();
    if(client.production)pages.syncApprovals();
    if(initialSnapshot){if(client.production)pages.permissions();renderShell();}
  }
  const abort = new AbortController();
  async function loadSnapshot(reattach = false) {
    if (disposed) return;
    if (reattach) {epoch = beginSubscription(state,{retainPending:true});disconnected=false;return attach();}
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
    if (state.snapshot) positions.set(ownerKey(state.snapshot), history.scrollTop);
    pages?.clearApprovals();locateApprovals=item.pending_approvals?JSON.stringify({owner:item.id,space:item.space_id||''}):'';selectedAssistant = item; client.setScope({owner:item.id, space:item.space_id || ''});
    epoch = beginSubscription(state); editorOwner = null; route = 'chat'; if(!initial)directoryOpen = false;
    messageColumn.replaceChildren(); messageNodes.clear(); error = ''; connectionError = ''; disconnected = false;
    const cached=cachedSnapshots.get(snapshotKey(item.space_id||'',item.id));
    restoredSnapshot=!!cached;connection='connecting';
    if(cached){acceptSnapshot(state,cached,epoch);syncEditor();renderMessages();const top=positions.get(ownerKey(cached));if(top!==undefined)requestAnimationFrame(()=>{if(!disposed&&editorOwner===ownerKey(cached)){history.scrollTop=top;latest.hidden=nearBottom();}});}
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
      if(!selectedAssistant)client.setScope({space:catalog.active_space_id||spaces[0].id,owner:''});
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
      if(value.potTask){route='tasks';await pages.openTask(value.potTask,{recordHistory:false,returnTo:value.potTaskReturn||{route:'tasks'}});return;}
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
  let swipe = null;
  const pointerDown = event => {
    if(!media.matches || surface==='tray' || event.pointerType==='mouse')return;
    if(!menuOpen&&event.clientX>root.getBoundingClientRect().left+parseFloat(getComputedStyle(root).paddingLeft)+24)return;
    swipe={id:event.pointerId,x:event.clientX,y:event.clientY,menu:menuOpen,active:false};
  };
  const pointerMove = event => {
    if(!swipe||swipe.id!==event.pointerId)return;
    const dx=event.clientX-swipe.x,dy=event.clientY-swipe.y;
    if(!swipe.active&&Math.abs(dy)>Math.abs(dx)&&Math.abs(dy)>10){swipe=null;return;}
    if(Math.abs(dx)>12&&Math.abs(dx)>Math.abs(dy))swipe.active=true;
    if(!swipe.active)return;
    if(swipe.menu&&dx<0){menu.style.transform=`translateX(${dx}px)`;event.preventDefault();}
  };
  const pointerUp = event => {
    if(!swipe||swipe.id!==event.pointerId)return;
    const gesture=swipe;swipe=null;menu.style.transform='';
    if(!gesture.active||Math.abs(event.clientY-gesture.y)>Math.abs(event.clientX-gesture.x))return;
    const dx=event.clientX-gesture.x,threshold=Math.min(innerWidth/4,100);
    if(gesture.menu&&dx < -threshold){menuOpen=false;renderShell();}
    else if(!gesture.menu&&dx>threshold){if(pages?.hasParent)pages.back();else if(route==='chat'&&!directoryOpen){directoryOpen=true;renderShell();}else{menuOpen=true;renderShell();}}
  };
  const pointerCancel = () => {swipe=null;menu.style.transform='';};
  root.addEventListener('pointerdown',pointerDown,{passive:true});root.addEventListener('pointermove',pointerMove,{passive:false});root.addEventListener('pointerup',pointerUp);root.addEventListener('pointercancel',pointerCancel);
  const mediaChanged = () => { menuOpen = false; renderShell(); };
  document.addEventListener('keydown', keydown);
  media.addEventListener('change', mediaChanged);
  renderShell();editor.resize();
  function attachChanges(){
    stopChanges();stopChanges=client.changes((topics,source={})=>{
      const keys=Array.isArray(topics)?topics:Object.keys(topics||{});
      client.resources?.invalidate(keys,source);
      if(source.boot){for(const [key,value]of cachedSnapshots)if((!source.space||JSON.parse(key)[1]===source.space)&&value.boot_id!==source.boot)cachedSnapshots.delete(key);if(state.snapshot&&(!source.space||source.space===client.scope().space)&&state.snapshot.boot_id!==source.boot)loadSnapshot(true);}
      if(source.reset){notifications?.refresh({reset:true,space:source.space});loadDirectory();}
      else if(relevantChange(keys,['task-outcomes','permissions']))notifications?.refresh({topics:keys,space:source.space});
      if(relevantChange(keys,['spaces','assistants','assistant-mail','conversation','runtime','permissions']))loadDirectory();
      if(relevantChange(keys,['tasks','runner'],client.scope().space,source))refreshBackgroundTasks();
      pages.refresh(keys,source);if(relevantChange(keys,['permissions','spaces','runtime'],pages.approvalSpace,source))pages.permissions();
    },{space:''});
  }
  if(client.production){attachChanges();loadDirectory();}else attach();
  const stopResume=bindResumeRecovery(async()=>{
    if(disposed)return;
    const snapshot=selectedAssistant||!client.production?loadSnapshot(true):Promise.resolve();
    if(client.production){attachChanges();pages.refresh();pages.permissions();refreshBackgroundTasks();}
    await Promise.all([snapshot,loadDirectory()]);
  });
  return () => {
    disposed = true; stopResources?.();client.resources?.clear();stopResume();taskClock.dispose();notifications?.dispose();directoryQueue.close();backgroundTasks?.close();directoryEpoch++; abort.abort(); unsubscribe(); stopChanges(); pages?.dispose(); editor.destroy();
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
