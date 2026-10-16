import {messages} from '../locales/messages.mjs';
import {acceptSnapshot, beginSend, beginSubscription, createConversationState, draft, ownerKey, setDraft, settleSend, validSnapshot} from '../state/conversation.mjs';
import {bindComposer} from './composer.mjs';
import {renderMarkdown} from './markdown.mjs';
import {createPages} from './pages.mjs';

const paths = {
  menu: 'M4 6h16M4 12h16M4 18h16', back: 'm14 5-7 7 7 7', plus: 'M12 5v14M5 12h14',
  tasks: 'M9 5h11M9 12h11M9 19h11M3 5h1M3 12h1M3 19h1',
  machines: 'M3 4h18v12H3zM8 20h8M12 16v4', usage: 'M5 20V10M12 20V4M19 20v-7',
  settings: 'm9 3-1 3-3 1-2 5 2 5 3 1 1 3h6l1-3 3-1 2-5-2-5-3-1-1-3zM15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0',
  more: 'M5 12h.01M12 12h.01M19 12h.01', send: 'm6 10 6-6 6 6M12 4v16',
  open: 'M14 3h7v7M21 3l-9 9M10 3H3v18h18v-7',
  stop: 'M6 6h12v12H6z', down: 'm6 9 6 6 6-6', close: 'm6 6 12 12M6 18 18 6',
  cloud:'M6 18h12a4 4 0 0 0 0-8h-1a6 6 0 0 0-11 1 3.5 3.5 0 0 0 0 7',
  clock:'M12 8v5h4M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0',
  copy: 'M9 9h11v11H9zM15 5V3H3v12h2',
};
function el(tag, className = '', text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}
function icon(name) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  for (const [key, value] of Object.entries({viewBox: '0 0 24 24', width: '20', height: '20', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.7', 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true'})) svg.setAttribute(key, value);
  const path = document.createElementNS(svg.namespaceURI, 'path');
  path.setAttribute('d', paths[name] || paths.more);
  svg.append(path);
  return svg;
}
function button(name, label, className = 'icon-button') {
  const item = el('button', className);
  item.type = 'button';
  item.title = label;
  item.setAttribute('aria-label', label);
  item.append(icon(name));
  return item;
}
function avatar(size = 32, key = '') {
  const result = el('span', 'avatar');
  result.style.width = result.style.height = `${size}px`;
  result.setAttribute('aria-hidden', 'true');
  const mark=el('span','avatar-mark');result.append(mark);
  if(key&&crypto.subtle)crypto.subtle.digest('SHA-256',new TextEncoder().encode(key)).then(value=>{const hash=new Uint8Array(value),foreground=['#276451','#904e39','#685083','#3a6590','#8a6928','#8b4a68','#40546c','#5a6f3c'],background=['#dce9e2','#f5ddd2','#e5ddf1','#dce8f4','#f4e8c8','#f1dce6','#dde4ec','#e3e8d9'];mark.style.background=foreground[hash[0]%8];result.style.background=background[hash[1]%8];});
  return result;
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
  let epoch = beginSubscription(state);
  let disconnected = false;
  let disposed = false;
  let route = 'chat';
  let connection = 'connecting';
  let error = '';
  let directoryOpen = !params.has('conversation');
  let cancelPending = false;
  let editorOwner = null;
  let menuOpen = false;
  let preferencesOpen = false;
  let assistants = [], spaces = [], selectedAssistant = null, pendingSelection = null;
  let unsubscribe = () => {}, stopChanges = () => {};
  let directoryEpoch = 0, directorySignature = '';
  const positions = new Map(), cachedSnapshots = new Map();
  const t = key => messages[locale][key] || key;
  root.replaceChildren();
  root.className = `pot-app surface-${surface === 'tray' ? 'tray' : 'main'}`;
  const rail = el('nav', 'feature-rail');
  const titlebar = el('header', 'titlebar');
  const directory = el('aside', 'assistant-directory');
  const workspace = el('main', 'workspace');
  const notice = el('p', 'preview-notice');
  const approvals = el('div', 'approval-host');
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
  workspace.append(notice, approvals, history, composerArea, unavailable);
  const menu = el('nav', 'mobile-menu');
  menu.hidden = true;
  const preferences = el('dialog', 'preferences');
  root.append(rail, titlebar, directory, workspace, menu, preferences);
  const pages = client.production ? createPages({client, host: unavailable, approvalHost: approvals, locale: () => locale, notify: value => {error = value; updateControls();}, changed: value => {if(value?.assistant)pendingSelection=value.assistant;loadDirectory();}, titleChanged: () => renderShell(), openChat: () => navigate('chat')}) : null;
  const media = matchMedia('(max-width: 719px)');
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
    if (state.snapshot?.running || connection !== 'connected' || editorOwner !== (state.snapshot && ownerKey(state.snapshot))) return;
    const receipt = beginSend(state, requestID());
    if (!receipt) return;
    error = '';
    updateControls();
    try {
      const result = await client.send(receipt.request);
      if (typeof result.message_id !== 'string' || typeof result.duplicate !== 'boolean') throw Error('Invalid preview receipt');
      settleSend(state, receipt, true);
    } catch (reason) {
      if (settleSend(state, receipt, false)) error = reason.message || String(reason);
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
  send.addEventListener('click', () => state.snapshot?.running ? cancel() : submit());
  function updateControls() {
    const snapshot = state.snapshot;
    const pending = snapshot && state.pending.has(ownerKey(snapshot));
    const running = snapshot?.running;
    const label = running ? (cancelPending ? t('stopping') : t('stop')) : t('send');
    send.replaceChildren(icon(running ? 'stop' : 'send'));
    send.title = label;
    send.setAttribute('aria-label', label);
    send.disabled = running ? cancelPending || connection !== 'connected' : !snapshot || editorOwner !== ownerKey(snapshot) || pending || !draft(state).trim() || connection !== 'connected';
    input.disabled = !snapshot && !editorOwner;
    status.replaceChildren();
    status.classList.toggle('has-error', Boolean(error) || connection === 'offline');
    status.classList.toggle('is-connected',!error&&connection==='connected');
    status.append(el('span', '', error || (connection === 'connected' ? t(client.production?'connected':'preview') : t(connection))));
    if (connection === 'offline') {
      const retry = el('button', 'text-button', t('retry'));
      retry.type = 'button';
      retry.onclick = () => loadSnapshot(true);
      status.append(retry);
    }
  }
  async function navigate(next) {
    if (pages && !await pages.leave()) return;
    route = next;
    menuOpen = false;
    if (next === 'chat') directoryOpen = true;
    renderShell();
    if (pages && next !== 'chat') pages.show(next);
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
    document.documentElement.dataset.theme = theme;
    document.documentElement.lang = locale;
    const narrow = media.matches && surface !== 'tray';
    root.dataset.route = route;
    root.classList.toggle('show-directory', route === 'chat' && narrow && directoryOpen);
    rail.replaceChildren(navigation(false));
    rail.setAttribute('aria-label', t('menu'));
    titlebar.replaceChildren();
    if (pages?.editing) { const back = button('back', t('back')); back.onclick = () => pages.back(); titlebar.append(back); }
    if (surface !== 'tray') {
      const back = route === 'chat' && narrow && !directoryOpen;
      const leading = button(back ? 'back' : 'menu', t(back ? 'back' : 'menu'), 'icon-button narrow-navigation');
      leading.onclick = () => {
        if (back) { directoryOpen = true; renderShell(); }
        else { menuOpen = true; renderShell(); }
      };
      titlebar.append(leading);
      if(!narrow&&route==='chat'&&!client.production){const back=button('back',t('back'));back.onclick=()=>{directoryOpen=true;renderShell()};titlebar.append(back);}
    }
    const title = el('div', 'window-title');
    if (route === 'chat' && !(narrow && directoryOpen)) title.append(avatar(24,selectedAssistant?.request_id||selectedAssistant?.id||'main'));
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
    if (state.snapshot?.usage) {const usage=el('span','title-meta token-meta');usage.append(icon('clock'),el('span','',`${new Intl.NumberFormat('en',{notation:'compact',maximumFractionDigits:1}).format(state.snapshot.usage.input+state.snapshot.usage.output)} tokens`));titlebar.append(usage);}
    const more = button(client.production?'more':'settings', t(client.production ? 'preferences' : 'more'));
    more.setAttribute('aria-haspopup', 'dialog');
    more.onclick = () => { preferencesOpen = true; renderPreferences(); preferences.showModal(); };
    titlebar.append(more);
    if(!client.production&&titlebar.querySelector('.title-meta'))titlebar.insertBefore(more,titlebar.querySelector('.title-meta'));
    notice.textContent = client.production ? '' : t('notice');
    input.placeholder = t('composer');
    input.setAttribute('aria-label', t('composer'));
    history.setAttribute('aria-label', t('chat'));
    latest.title = t('latest');
    latest.setAttribute('aria-label', t('latest'));
    approvals.hidden = history.hidden = composerArea.hidden = route !== 'chat';
    unavailable.hidden = route === 'chat';
    if (!client.production) unavailable.textContent = t('unavailable');
    // Search is window state and survives narrow conversation navigation/resizes.
    const nextDirectorySignature = JSON.stringify([locale,selectedAssistant?.space_id,selectedAssistant?.id,assistants,state.snapshot?.assistants]);
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
      row.append(avatar(32,item.request_id||item.id)); const labels = el('span', 'assistant-labels'); const name = el('span', 'assistant-name', item.name);
      const badge=el('span','owner-badge badge-icon');badge.title=item.space_name||t(item.space_kind==='remote'?'remote':'local');badge.append(icon(item.space_kind==='remote'?'cloud':'machines'));name.append(badge);
      labels.append(name, el('span', 'assistant-preview', item.offline ? t('offline') : item.preview || item.phase || ''));
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
  const messageNodes = new Map();
  let activityNode;
  function renderMessages() {
    const follow = nearBottom();
    const previousTop = history.scrollTop;
    activityNode?.remove();
    activityNode = null;
    const snapshot = state.snapshot;
    const ids = new Set((snapshot?.messages || []).map(message => message.id));
    for (const [id, record] of messageNodes) if (!ids.has(id)) { record.row.remove(); messageNodes.delete(id); }
    let position = 0;
    for (const message of snapshot?.messages || []) {
      let record = messageNodes.get(message.id);
      if (!record) {
        const row = el('article', `message role-${message.role}`);
        const bubble = el('div', 'message-body');
        row.append(bubble);
        record = {row, bubble};
        messageNodes.set(message.id, record);
      }
      const {row, bubble} = record;
      if (record.text !== message.text || record.role !== message.role) {
        bubble.replaceChildren();
        if (message.role === 'tool') {const detail=el('details','tool-details');detail.append(el('summary','',message.text.split('\n')[0].slice(0,100)),el('pre','',message.text));bubble.append(detail);}
        else if (message.role === 'system') bubble.textContent = message.text;
        else bubble.append(renderMarkdown(message.text));
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
      if (message.status === 'streaming' && !record.dot) {
        record.dot = el('span', 'activity-dot'); row.append(record.dot);
      } else if (message.status !== 'streaming' && record.dot) { record.dot.remove(); record.dot = null; }
      record.dot?.setAttribute('aria-label', t('working'));
      if (messageColumn.children[position] !== row) messageColumn.insertBefore(row, messageColumn.children[position] || null);
      position++;
    }
    if (snapshot && !snapshot.messages.length) {
      activityNode = el('p', 'empty-chat', t('first')); messageColumn.append(activityNode);
    }
    if (snapshot?.running && !snapshot.messages.some(message => message.role === 'assistant' && message.status === 'streaming')) {
      activityNode = el('div', 'activity', t('working'));
      const dots = el('span', 'waiting-dots');
      dots.setAttribute('aria-hidden', 'true');
      dots.append(el('i'), el('i'), el('i'));
      activityNode.append(dots); messageColumn.append(activityNode);
    }
    if (follow) requestAnimationFrame(() => { if (!disposed) scrollBottom(); });
    else { history.scrollTop = previousTop; latest.hidden = false; }
  }
  function onSnapshot(snapshot, receivedEpoch = epoch) {
    if (disposed || receivedEpoch !== state.epoch) return;
    if (state.snapshot && validSnapshot(snapshot)
        && (snapshot.boot_id !== state.snapshot.boot_id || ownerKey(snapshot) !== ownerKey(state.snapshot))) {
      connection = 'offline'; error = t('identityChanged'); updateControls(); return;
    }
    const initialSnapshot = !state.snapshot;
    if (!acceptSnapshot(state, snapshot, receivedEpoch)) return;
    connection = 'connected';
    if(client.production)cachedSnapshots.set(JSON.stringify([client.scope().space,client.scope().owner]),snapshot);
    const restorePosition = editorOwner !== ownerKey(snapshot) ? positions.get(ownerKey(snapshot)) : undefined;
    syncEditor();
    renderMessages(); if (restorePosition !== undefined) requestAnimationFrame(() => {history.scrollTop=restorePosition; latest.hidden=nearBottom();}); updateControls();
    if (client.production) { pages.permissions(); if(initialSnapshot)renderShell(); } else if(initialSnapshot)renderShell();
  }
  const abort = new AbortController();
  async function loadSnapshot(reattach = false) {
    if (disposed) return;
    if (reattach) {epoch = beginSubscription(state);attach();return;}
    const receivedEpoch = epoch;
    connection = 'connecting'; error = ''; updateControls();
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
    unsubscribe(); stopChanges();
    const receivedEpoch = epoch;
    unsubscribe = client.subscribe(value => onSnapshot(value, receivedEpoch), value => {
      if (disposed || receivedEpoch !== state.epoch) return;
      if (value === 'offline') disconnected = true;
      if (value === 'connected' && disconnected) { disconnected = false; loadSnapshot(true); }
      else { connection = value; updateControls(); }
    });
    if (client.production) stopChanges = client.changes(() => {loadDirectory(false); pages.refresh(); pages.permissions();});
    loadSnapshot();
  }
  async function selectAssistant(item, initial = false) {
    if (editor.composing) { input.blur(); if (editor.composing) return; }
    if (pages && !await pages.leave()) return;
    if (state.snapshot) positions.set(ownerKey(state.snapshot), history.scrollTop);
    selectedAssistant = item; client.setScope({owner:item.id, space:item.space_id || ''});
    epoch = beginSubscription(state); editorOwner = null; route = 'chat'; if(!initial)directoryOpen = false;
    messageColumn.replaceChildren(); messageNodes.clear(); error = '';
    const cached=cachedSnapshots.get(JSON.stringify([item.space_id||'',item.id]));
    if(cached){acceptSnapshot(state,cached,epoch);syncEditor();renderMessages();}
    renderShell();attach();
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
      const results = await Promise.allSettled(spaces.map(async space => {
        const items = await client.request('/api/v1/assistants', {owner:'', space:space.id});
        return (items.items || items || []).map(item => ({...item, space_id:space.id, space_name:space.name, space_kind:space.kind, offline:false}));
      }));
      if (ticket !== directoryEpoch || disposed) return;
      assistants = results.flatMap((result, index) => result.status === 'fulfilled' ? result.value : assistants.filter(item => item.space_id === spaces[index].id).map(item => ({...item,offline:true})));
      if (results.some(result => result.status === 'rejected')) error = t('partialOffline');
      if (selectedAssistant) selectedAssistant = assistants.find(item => item.id===selectedAssistant.id&&item.space_id===selectedAssistant.space_id) || selectedAssistant;
      renderShell();
      if(!assistants.length){const empty=el('div','empty-chat',t('noAssistants'));const setup=el('button','control',t('setupAI'));setup.onclick=()=>navigate('settings');empty.append(document.createElement('br'),setup);messageColumn.replaceChildren(empty);}
      if(pendingSelection){const chosen=assistants.find(item=>item.id===pendingSelection.id&&item.space_id===pendingSelection.space_id);pendingSelection=null;if(chosen){await selectAssistant(chosen);return;}}
      if (autoSelect && !selectedAssistant && assistants.length) {
        const requested=params.get('assistant_id');const requestedSpace=params.get('space_id');
        const selected=requested?assistants.find(item=>item.id===requested&&(!requestedSpace||item.space_id===requestedSpace)):assistants[0];
        if(!selected){error=t('missingAssistant');connection='offline';updateControls();return;}
        await selectAssistant(selected,!requested);
      }
    } catch (reason) { if (ticket===directoryEpoch&&!disposed) {error = reason.message; connection='offline'; updateControls();} }
  }
  const preferencesChanged = event => {
    if(event.detail.key==='pot-go-preview-locale') locale=event.detail.value;
    if(event.detail.key==='pot-go-preview-theme') theme=event.detail.value;
    renderShell();renderMessages();window.dispatchEvent(new Event('pot:locale'));
  };
  window.addEventListener('pot:preference',preferencesChanged);
  const keydown = event => {
    if (event.key === 'Escape' && menuOpen) { menuOpen = false; renderShell(); }
    if (event.metaKey && event.key === '[' && pages?.editing) {event.preventDefault(); pages.back(); return;}
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
    else if(!gesture.menu&&dx>threshold){if(pages?.editing)pages.back();else if(route==='chat'&&!directoryOpen){directoryOpen=true;renderShell();}else{menuOpen=true;renderShell();}}
  };
  const pointerCancel = () => {swipe=null;menu.style.transform='';};
  root.addEventListener('pointerdown',pointerDown,{passive:true});root.addEventListener('pointermove',pointerMove,{passive:false});root.addEventListener('pointerup',pointerUp);root.addEventListener('pointercancel',pointerCancel);
  const mediaChanged = () => { menuOpen = false; renderShell(); };
  document.addEventListener('keydown', keydown);
  media.addEventListener('change', mediaChanged);
  viewport?.addEventListener('resize', viewportChanged);
  window.addEventListener('resize', viewportChanged);
  renderShell(); viewportChanged(); if (client.production) loadDirectory(); else attach();
  return () => {
    disposed = true; directoryEpoch++; abort.abort(); unsubscribe(); stopChanges(); pages?.dispose(); editor.destroy();
    root.removeEventListener('pointerdown',pointerDown);root.removeEventListener('pointermove',pointerMove);root.removeEventListener('pointerup',pointerUp);root.removeEventListener('pointercancel',pointerCancel);
    window.removeEventListener('pot:preference',preferencesChanged);
    document.removeEventListener('keydown', keydown);
    media.removeEventListener('change', mediaChanged);
    viewport?.removeEventListener('resize', viewportChanged);
    window.removeEventListener('resize', viewportChanged);
    root.replaceChildren();
  };
}
