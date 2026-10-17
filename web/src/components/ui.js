import {createToolCall} from './tool-call.mjs';
import {toolForest} from '../state/tool-calls.mjs';
import {avatar} from './avatar.mjs';
import {icon} from './icons.mjs';
import {messages,translate} from '../locales/messages.mjs';
import {acceptSnapshot, beginSend, beginSubscription, createConversationState, draft, ownerKey, setDraft, settleSend, validSnapshot} from '../state/conversation.mjs';
import {bindComposer} from './composer.mjs';
import {renderMarkdown} from './markdown.mjs';
import {createPages} from './pages.mjs';
import {createReplies,replyKey} from '../state/replies.mjs';
import {bindCompletions} from './completions.mjs';
import {projectChatReplies} from '../state/chat-replies.mjs';
import {createReplyBubble} from './reply.mjs';

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
  const t = key => translate(locale,key);
  root.replaceChildren();
  root.className = `pot-app surface-${surface === 'tray' ? 'tray' : 'main'}`;
  const rail = el('nav', 'feature-rail');
  const titlebar = el('header', 'titlebar');
  const directory = el('aside', 'assistant-directory');
  const workspace = el('main', 'workspace');
  const notice = el('p', 'preview-notice');
  const approvals = el('div', 'approval-host');
  const resourceNotices=el('div','resource-notices');resourceNotices.hidden=true;resourceNotices.dataset.empty='true';
  const history = el('div', 'history');
  history.setAttribute('role', 'log');
  history.setAttribute('aria-label', t('chat'));
  const messageColumn = el('div', 'message-column');
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
  workspace.append(notice, approvals, resourceNotices, history, composerArea, unavailable);
  const menu = el('nav', 'mobile-menu');
  menu.hidden = true;
  const preferences = el('dialog', 'preferences');
  root.append(rail, titlebar, directory, workspace, menu, preferences);
  const pages = client.production ? createPages({onApprovalsChanged:()=>renderMessages(),client, host: unavailable, approvalHost: approvals, locale: () => locale, notify: value => {error = value; updateControls();}, changed: value => {if(value?.assistant)pendingSelection=value.assistant;loadDirectory();}, titleChanged: () => renderShell(),routeChanged:value=>{route=value;renderShell();}, openChat:()=>{route='chat';directoryOpen=false;renderShell();if(!selectedAssistant)loadDirectory();}}) : null;
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
  const viewport = window.visualViewport;
  const viewportChanged = () => {
    root.style.height = `${viewport ? viewport.height : innerHeight}px`;
    editor.resize();
  };
  const editor = bindComposer(input, {
    onInput: text => { setDraft(state, text, editorOwner); updateControls(); },
    onSend: submit,
    onCompositionEnd: () => { syncEditor(); updateControls(); },
  });
  const completions=client.production?bindCompletions(input,{client,host:composerArea,locale:()=>locale}):null;
  function syncEditor() {
    if (!state.snapshot) return;
    const nextOwner = ownerKey(state.snapshot);
    // Finish an IME commit into its original draft before adopting a newly
    // attached owner. Reconnecting must not disable or replace native preedit.
    if (editor.composing && editorOwner !== nextOwner) return;
    editorOwner = nextOwner;
    editor.setValue(draft(state, editorOwner));
  }
  function requestID() { return crypto.randomUUID(); }
  async function submit() {
    if ((!client.production&&state.snapshot?.running) || connection !== 'connected' || editorOwner !== (state.snapshot && ownerKey(state.snapshot))) return;
    const receipt = beginSend(state, requestID(),{allowRunning:client.production});
    if (!receipt) return;
    error = '';
    updateControls();
    try {
      const result = await client.send(receipt.request);
      if (typeof result.message_id !== 'string' || typeof result.duplicate !== 'boolean') throw Error('Invalid message receipt');
      settleSend(state, receipt, true);
    } catch (reason) {
      if (settleSend(state, receipt, false, reason)&&receipt.request.user_message_id) error = reason.message || String(reason);
    }
    if (!disposed) { syncEditor(); updateControls(); }
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
  form.addEventListener('submit', event => { event.preventDefault(); submit(); });
  send.addEventListener('click', () => state.snapshot?.running&&!draft(state).trim() ? cancel() : submit());
  function updateControls() {
    const snapshot = state.snapshot;
    const pending = snapshot && state.pending.has(ownerKey(snapshot));
    const running = snapshot?.running;
    const stopping=running&&(!client.production||!draft(state).trim());
    const label = stopping ? (cancelPending ? t('stopping') : t('stop')) : running?(locale==='zh-CN'?'发送指导':'Send guidance'):t('send');
    send.replaceChildren(icon(stopping ? 'stop' : 'send'));
    send.title = label;
    send.setAttribute('aria-label', label);
    send.disabled = stopping ? selectedAssistant?.offline||cancelPending || connection !== 'connected' : selectedAssistant?.offline|| !snapshot || editorOwner !== ownerKey(snapshot) || pending || !draft(state).trim() || connection !== 'connected';
    input.disabled = !snapshot && !editorOwner;
    const visibleConnection=selectedAssistant?.offline?'offline':connection;
    status.replaceChildren();
    status.classList.toggle('has-error', Boolean(error || connectionError) || visibleConnection === 'offline');
    status.classList.toggle('is-connected',!error&&!connectionError&&visibleConnection==='connected');
    const replyOwnsConnection=state.snapshot?.running||state.sends.has(editorOwner);
    if(error||!replyOwnsConnection)status.append(el('span', '', error || connectionError || (visibleConnection === 'connected' ? t(client.production?'connected':'preview') : t(visibleConnection))));
    if (visibleConnection === 'offline'&&!replyOwnsConnection) {
      const retry = el('button', 'text-button', t('retry'));
      retry.type = 'button';
      retry.onclick = () => loadSnapshot(true);
      status.append(retry);
    }
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
    if(client.production){preferences.classList.add('product-actions');preferences.replaceChildren();const close=button('close',t('close'));close.onclick=()=>preferences.close();preferences.append(close);const action=(label,run)=>{const choice=el('button','menu-destination',label);choice.type='button';choice.onclick=()=>{preferences.close();run();};preferences.append(choice);};if(selectedAssistant){action(t('assistantSettings'),()=>{route='settings';pages.assistant(selectedAssistant);renderShell();});action(t('usage'),()=>{route='usage';pages.conversationUsage(selectedAssistant.name);renderShell();});}if(state.snapshot?.running){const captured={...client.scope()},target=state.snapshot.turn_id,paused=!!state.snapshot.turn?.paused;action(paused?(locale==='zh-CN'?'恢复回复':'Resume turn'):(locale==='zh-CN'?'暂停回复':'Pause turn'),async()=>{try{await client.controlTurn({user_message_id:target,action:paused?'resume':'pause'},captured);}catch(reason){error=reason.message;updateControls();}});}action(t('settings'),()=>navigate('settings'));return;}
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
  preferences.addEventListener('close', () => { preferencesOpen = false; });
  function renderShell() {
    markVisibleRead();
    if(route!=='chat')completions?.reset();
    window.potErrors?.setTranslator(t);
    document.documentElement.dataset.theme = theme;
    document.documentElement.lang = locale;
    const narrow = media.matches && surface !== 'tray';
    root.dataset.route = route;
    root.classList.toggle('show-directory', route === 'chat' && narrow && directoryOpen);
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
    title.append(el('span', '', route === 'chat' ? (narrow && directoryOpen ? t('assistants') : selectedAssistant?.name || 'Pot') : pages?.title || t(route)));
    titlebar.append(title);
    if (surface === 'tray' && client.openMain) {
      const open = button('open', t('openMain'));
      open.onclick = async () => { try { await client.openMain(); } catch (reason) { error = reason.message; updateControls(); } };
      titlebar.append(open);
    }
    if (client.production && route === 'chat' && selectedAssistant) { const settings = button('settings', t('assistantSettings')); settings.onclick = () => {route = 'settings'; pages.assistant(selectedAssistant); renderShell();}; titlebar.append(settings); }
    if(!client.production){const cloud=el('span','icon-button');cloud.title=t('remote');cloud.append(icon('cloud'));titlebar.append(cloud);}
    if (state.snapshot?.configuration) titlebar.append(el('span', 'title-meta', `${state.snapshot.configuration.name || ''} · ${state.snapshot.configuration.model || ''}`));
    if (state.snapshot?.usage) {const value=state.snapshot.usage,format=new Intl.NumberFormat(locale,{notation:'compact',maximumFractionDigits:1}),usage=el('span','title-meta token-meta'),cached=locale==='zh-CN'?'缓存输入':'Cached input';usage.append(icon('clock'),el('span','',`${format.format(value.input+value.output)} tokens${value.cache==null?'':` · ${cached} ${format.format(value.cache)}`}`));usage.title=`${locale==='zh-CN'?'输入（含缓存）':'Input (includes cache)'}: ${value.input} · ${locale==='zh-CN'?'输出':'Output'}: ${value.output}${value.cache==null?'':` · ${cached}: ${value.cache}`}`;titlebar.append(usage);}
    const more = button(client.production?'more':'settings', t(client.production ? 'preferences' : 'more'));
    more.setAttribute('aria-haspopup', 'dialog');
    more.onclick = () => { preferencesOpen = true; renderPreferences(); preferences.showModal(); };
    if(!client.production||route==='chat')titlebar.append(more);
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
    const nextDirectorySignature = JSON.stringify([locale,selectedAssistant?.space_id,selectedAssistant?.id,assistants,state.snapshot?.assistants,replies.records()]);
    if (nextDirectorySignature !== directorySignature || !directory.childElementCount) {
    directorySignature=nextDirectorySignature;
    const searchValue = directory.querySelector('input')?.value || '';
    const scrollTop = directory.querySelector('.assistant-list')?.scrollTop || 0;
    directory.replaceChildren();
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
    const rows = [];
    for (const item of items) {
      const selected = (!client.production&&item.id==='main') || selectedAssistant?.id === item.id && selectedAssistant?.space_id === item.space_id;
      const row = el('button', `assistant-row${selected ? ' is-selected' : ''}`); row.type = 'button';
      row.append(assistantAvatar(item,32)); const labels = el('span', 'assistant-labels'); const name = el('span', 'assistant-name');name.append(el('span','assistant-title',item.name));
      const badge=el('span','owner-badge badge-icon');badge.title=item.space_name||t(item.space_kind==='remote'?'remote':'local');badge.append(icon(item.space_kind==='remote'?'cloud':'machines'));name.append(badge);
      const unread=replies.unread(controlOf(item),item.id,item.reply_count||0);if(unread){const badge=el('span','unread-badge',unread>9?'9+':String(unread));badge.title=String(unread);badge.setAttribute('aria-label',`${unread} ${locale==='zh-CN'?'条未读回复':'unread replies'}`);name.append(badge);row.classList.add('has-unread');}
      labels.append(name, el('span', 'assistant-preview', item.offline ? t('offline') : item.preview || ({failed:t('replyFailed'),needs_configuration:t('setupAI'),ready:t('connected')}[item.phase] || item.phase || '')));
      row.append(labels); row.onclick = () => client.production ? selectAssistant(item) : (directoryOpen = false, renderShell());
      list.append(row); rows.push({row, name:item.name.toLowerCase()});
    }
    const empty = el('p', 'hint', t(items.length ? 'searchEmpty' : 'noAssistants'));
    const filter = () => { let count = 0; for (const item of rows) {item.row.hidden = !item.name.includes(search.value.toLowerCase()); if(!item.row.hidden) count++;} empty.hidden = count > 0; };
    search.oninput = filter; list.append(empty);
    directory.append(heading, search, list); filter(); list.scrollTop = scrollTop;
    }
    menu.replaceChildren();
    const menuHeading = el('div', 'mobile-menu-heading', 'Pot');
    const closeMenu = button('close', t('close'));
    closeMenu.onclick = () => { menuOpen = false; renderShell(); };
    menuHeading.append(closeMenu);
    menu.append(menuHeading, navigation(true));
    menu.hidden = !menuOpen || !narrow;
    menu.setAttribute('aria-label', t('menu'));
    workspace.inert = directory.inert = titlebar.inert = !menu.hidden;
    if (!menu.hidden) closeMenu.focus({preventScroll: true});
    if (preferencesOpen) renderPreferences();
    updateControls();
    requestAnimationFrame(() => { if (!disposed) editor.resize(); });
  }
  function nearBottom() { return history.scrollHeight - history.clientHeight - history.scrollTop < 80; }
  function scrollBottom() { history.scrollTop = history.scrollHeight; latest.hidden = true; }
  history.addEventListener('scroll', () => { latest.hidden = nearBottom(); }, {passive: true});
  latest.onclick = scrollBottom;
  const messageNodes = new Map(),toolMemory=new Map();
  const taskCache=new Map();let taskRefresh=0;
  async function refreshBackgroundTasks(){
    if(!client.production||!selectedAssistant)return;
    const scope=client.scope(),key=JSON.stringify([scope.space,scope.owner]),ticket=++taskRefresh;
    try{const value=await client.request('/api/v1/tasks/query?scope=active&limit=50',{...scope});if(disposed||ticket!==taskRefresh||key!==JSON.stringify([client.scope().space,client.scope().owner]))return;taskCache.set(key,(value.items||[]).filter(task=>task.phase==='running').map(task=>({...task,_space_id:scope.space,assistant_id:scope.owner})));if(route==='chat')renderMessages();}catch{/* Keep owner-scoped cached rows until an authoritative refresh succeeds. */}
  }
  function renderMessages() {
    const follow = nearBottom();
    const previousTop = history.scrollTop;
    const snapshot = state.snapshot||(client.production&&selectedAssistant?cachedSnapshots.get(JSON.stringify([selectedAssistant.space_id||'',selectedAssistant.id])):null);
    const sending=state.sends.get(editorOwner||(snapshot&&ownerKey(snapshot)));
    const visibleMessages=projectChatReplies(snapshot,sending,{offline:connection==='offline'||disconnected||selectedAssistant?.offline});
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
      if(message.role==='tool'&&message.tool){
        const scope=client.scope?.()||{},key=JSON.stringify([scope.space,scope.owner,message.id]);
        if(!record.tool){record.tool=createToolCall({memory:toolMemory,key,locale:()=>locale,onDecision:(approval,action)=>pages.decideApproval(approval,action)});bubble.append(record.tool.element);}
        record.tool.update(message,{approval:pages?.approvalFor(message.tool),offline:connection==='offline'||disconnected,children:forest.byID.get(message.tool.turn_id+':'+message.tool.id)?.children.map(entry=>entry.message)||[]});
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
        else {const scope=client.scope?.();bubble.append(renderMarkdown(message.text,{downloadFile:client.downloadFile?async({task,path})=>{try{await client.downloadFile(task,path,scope);}catch(reason){error=reason.message;updateControls();}}:undefined}));}
        record.text = message.text; record.role = message.role;
        row.className = `message role-${message.role}`;
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
      if(parent&&parent.children.some(entry=>entry.message.id===message.id)&&messageNodes.get(parent.message.viewKey)?.tool){messageNodes.get(parent.message.viewKey).tool.children.append(row);}
      else {if (messageColumn.children[position] !== row) messageColumn.insertBefore(row, messageColumn.children[position] || null);position++;}
    }
    const lastAssistant=visibleMessages.findLast(message=>message.role==='assistant');
    for(const [id,record]of messageNodes){if(id!==lastAssistant?.viewKey){record.tasks?.remove();record.tasks=null;}}
    if(client.production&&lastAssistant){const record=messageNodes.get(lastAssistant.viewKey),tasks=taskCache.get(JSON.stringify([client.scope().space,client.scope().owner]))||[];record.tasks?.remove();record.tasks=null;if(tasks.length){const strip=el('div','background-tasks');strip.setAttribute('aria-label',locale==='zh-CN'?'正在执行的后台任务':'Executing background tasks');for(const task of tasks.slice(0,3)){const open=el('button','background-task');open.type='button';open.title=task.title;open.append(el('span','task-activity-dot'),el('span','background-task-title',task.title));open.onclick=()=>{route='tasks';pages.openTask(task);renderShell();};strip.append(open);}if(tasks.length>3){const more=el('button','text-button',locale==='zh-CN'?`还有 ${tasks.length-3} 项任务`:`${tasks.length-3} more tasks`);more.onclick=()=>navigate('tasks');strip.append(more);}record.row.append(strip);record.tasks=strip;}}
    if (snapshot && !visibleMessages.length) {
      if(!messageColumn.querySelector('.empty-chat'))messageColumn.append(el('p', 'empty-chat', t('first')));
    }else if(visibleMessages.length){
      messageColumn.querySelector('.empty-chat')?.remove();
    }
    if (follow) requestAnimationFrame(() => { if (!disposed) scrollBottom(); });
    else { history.scrollTop = previousTop; latest.hidden = false; }
  }
  function pendingSend(){return state.snapshot&&state.pending.has(ownerKey(state.snapshot));}
  function onSnapshot(snapshot, receivedEpoch = epoch) {
    if (disposed || receivedEpoch !== state.epoch) return;
    if (state.snapshot && validSnapshot(snapshot)
        && (snapshot.boot_id !== state.snapshot.boot_id || ownerKey(snapshot) !== ownerKey(state.snapshot))) {
      connection = 'offline'; error = t('identityChanged'); updateControls(); return;
    }
    const initialSnapshot = !state.snapshot;
    if (!acceptSnapshot(state, snapshot, receivedEpoch)){if(state.snapshot&&validSnapshot(snapshot)&&state.snapshot.cursor===snapshot.cursor){connection='connected';updateControls();}return;}
    connection = 'connected';
    connectionError = '';
    if(client.production)cachedSnapshots.set(JSON.stringify([client.scope().space,client.scope().owner]),snapshot);
    const restorePosition = editorOwner !== ownerKey(snapshot) ? positions.get(ownerKey(snapshot)) : undefined;
    syncEditor();
    if(markVisibleRead())renderShell();
    renderMessages(); if (restorePosition !== undefined) requestAnimationFrame(() => {history.scrollTop=restorePosition; latest.hidden=nearBottom();}); updateControls();
    if (client.production) { pages.permissions(); if(initialSnapshot)renderShell(); } else if(initialSnapshot)renderShell();
  }
  const abort = new AbortController();
  async function loadSnapshot(reattach = false) {
    if (disposed) return;
    if (reattach) {epoch = beginSubscription(state);disconnected=false;attach();return;}
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
    const receivedEpoch = epoch;
    unsubscribe = client.subscribe(value => onSnapshot(value, receivedEpoch), (value, reason) => {
      if (disposed || receivedEpoch !== state.epoch) return;
      if (value === 'offline') {disconnected = true;connectionError = reason?.message || t('streamDisconnected');}
      if (value === 'connected') connectionError = '';
      if (value === 'connected' && disconnected) { disconnected = false; loadSnapshot(true); }
      else { connection = value; updateControls(); }
    });
    loadSnapshot();
  }
  async function selectAssistant(item, initial = false,record=true) {
    completions?.reset();
    if (editor.composing) { input.blur(); if (editor.composing) return; }
    if (pages && !await pages.leave()) return;
    if (state.snapshot) positions.set(ownerKey(state.snapshot), history.scrollTop);
    pages?.clearApprovals();selectedAssistant = item; client.setScope({owner:item.id, space:item.space_id || ''});
    epoch = beginSubscription(state); editorOwner = null; route = 'chat'; if(!initial)directoryOpen = false;
    messageColumn.replaceChildren(); messageNodes.clear(); error = ''; connectionError = ''; disconnected = false;
    const cached=cachedSnapshots.get(JSON.stringify([item.space_id||'',item.id]));
    if(cached){acceptSnapshot(state,cached,epoch);syncEditor();renderMessages();}
    if(client.production&&record)saveNavigation('chat',initial);
    renderShell();attach();pages?.permissions();refreshBackgroundTasks();
  }
  async function loadDirectory(autoSelect = true) {
    if (!client.production) return;
    const ticket = ++directoryEpoch;
    try {
      const catalog = await client.request('/api/v1/spaces', {owner:'', space:''});
      if (ticket !== directoryEpoch || disposed) return;
      spaces = catalog.items || [];client.spaces=spaces;
      if (!spaces.length) throw Error('No serving Space');
      if(!selectedAssistant)client.setScope({space:catalog.active_space_id||spaces[0].id,owner:''});
      const projectAssistants=(value,space,offline)=>{let items=value;if(typeof items==='string'){try{items=JSON.parse(items);}catch{items=[];}}return (items?.items||items||[]).map(item=>({...item,control_id:space.control_id||space.id,space_id:space.id,space_name:space.name,space_kind:space.kind,offline}));};
      const results = await Promise.allSettled(spaces.map(async space => {
        const items = await client.request('/api/v1/assistants', {owner:'', space:space.id});
        return projectAssistants(items,space,space.connected===false);
      }));
      if (ticket !== directoryEpoch || disposed) return;
      assistants = results.flatMap((result, index) => result.status === 'fulfilled' ? result.value : (()=>{const space=spaces[index],previous=assistants.filter(item=>item.space_id===space.id);return previous.length?previous.map(item=>({...item,offline:true})):projectAssistants(space.assistants||[],space,true);})());
      client.assistants=assistants;
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
      if(pendingSelection){const chosen=assistants.find(item=>item.id===pendingSelection.id&&item.space_id===pendingSelection.space_id);pendingSelection=null;if(chosen){await selectAssistant(chosen);return;}}
      if (!selectedAssistant && assistants.length && !pages.editing) {
        const requested=params.get('assistant_id');const requestedSpace=params.get('space_id');
        const selected=requested?assistants.find(item=>item.id===requested&&(!requestedSpace||item.space_id===requestedSpace)):assistants[0];
        if(!selected){error=t('missingAssistant');connection='offline';updateControls();return;}
        await selectAssistant(selected,!requested);
      }
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
      if(value.potTask){route='tasks';await pages.openTask(value.potTask,false);return;}
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
    if(!menuOpen&&event.clientX>24)return;
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
  viewport?.addEventListener('resize', viewportChanged);
  window.addEventListener('resize', viewportChanged);
  renderShell();viewportChanged();
  if(client.production){stopChanges=client.changes(topics=>{const keys=Array.isArray(topics)?topics:Object.keys(topics||{});if(!keys.length||keys.some(key=>/^(spaces|assistant|conversation|runtime)/.test(key)))loadDirectory(false);if(keys.some(key=>/^(tasks|runner)/.test(key)))refreshBackgroundTasks();pages.refresh();pages.permissions();},{space:''});loadDirectory();}else attach();
  return () => {
    disposed = true; directoryEpoch++; abort.abort(); unsubscribe(); stopChanges(); pages?.dispose(); editor.destroy();
    completions?.dispose();
    stopRead();reducedMotion.removeEventListener('change',reducedChanged);window.removeEventListener('focus',focusRead);document.removeEventListener('visibilitychange',focusRead);window.removeEventListener('storage',readStorage);
    root.removeEventListener('pointerdown',pointerDown);root.removeEventListener('pointermove',pointerMove);root.removeEventListener('pointerup',pointerUp);root.removeEventListener('pointercancel',pointerCancel);
    window.removeEventListener('pot:preference',preferencesChanged);
    document.removeEventListener('keydown', keydown);window.removeEventListener('popstate',popNavigation);
    media.removeEventListener('change', mediaChanged);
    viewport?.removeEventListener('resize', viewportChanged);
    window.removeEventListener('resize', viewportChanged);
    root.replaceChildren();
  };
}
