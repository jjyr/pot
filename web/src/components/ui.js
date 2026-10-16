import {messages} from '../locales/messages.mjs';
import {acceptSnapshot, beginSend, beginSubscription, createConversationState, draft, ownerKey, setDraft, settleSend, validSnapshot} from '../state/conversation.mjs';
import {bindComposer} from './composer.mjs';
import {renderMarkdown} from './markdown.mjs';

const paths = {
  menu: 'M4 6h16M4 12h16M4 18h16', back: 'm14 5-7 7 7 7', plus: 'M12 5v14M5 12h14',
  tasks: 'M9 5h11M9 12h11M9 19h11M3 5h1M3 12h1M3 19h1',
  machines: 'M3 4h18v12H3zM8 20h8M12 16v4', usage: 'M5 20V10M12 20V4M19 20v-7',
  settings: 'm9 3-1 3-3 1-2 5 2 5 3 1 1 3h6l1-3 3-1 2-5-2-5-3-1-1-3zM15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0',
  more: 'M5 12h.01M12 12h.01M19 12h.01', send: 'm6 10 6-6 6 6M12 4v16',
  open: 'M14 3h7v7M21 3l-9 9M10 3H3v18h18v-7',
  stop: 'M6 6h12v12H6z', down: 'm6 9 6 6 6-6', close: 'm6 6 12 12M6 18 18 6',
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
function avatar(size = 32) {
  const result = el('span', 'avatar');
  result.style.width = result.style.height = `${size}px`;
  result.setAttribute('aria-hidden', 'true');
  result.append(el('span', 'avatar-mark'));
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
  const t = key => messages[locale][key] || key;
  root.replaceChildren();
  root.className = `pot-app surface-${surface === 'tray' ? 'tray' : 'main'}`;
  const rail = el('nav', 'feature-rail');
  const titlebar = el('header', 'titlebar');
  const directory = el('aside', 'assistant-directory');
  const workspace = el('main', 'workspace');
  const notice = el('p', 'preview-notice');
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
  const unavailable = el('div', 'unavailable');
  workspace.append(notice, history, composerArea, unavailable);
  const menu = el('nav', 'mobile-menu');
  menu.hidden = true;
  const preferences = el('dialog', 'preferences');
  root.append(rail, titlebar, directory, workspace, menu, preferences);
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
    status.append(el('span', '', error || (connection === 'connected' ? t('preview') : t(connection))));
    if (connection === 'offline') {
      const retry = el('button', 'text-button', t('retry'));
      retry.type = 'button';
      retry.onclick = () => loadSnapshot(true);
      status.append(retry);
    }
  }
  function navigate(next) {
    route = next;
    menuOpen = false;
    if (next === 'chat') directoryOpen = true;
    renderShell();
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
          renderShell(); renderMessages();
        };
        label.append(text, select);
        preferences.append(label);
        preferenceControls.fields.set(key, {text, select});
      }
    }
    const {title, close, hint, fields} = preferenceControls;
    title.textContent = t('more'); hint.textContent = t('notice');
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
    if (surface !== 'tray') {
      const back = route === 'chat' && narrow && !directoryOpen;
      const leading = button(back ? 'back' : 'menu', t(back ? 'back' : 'menu'), 'icon-button narrow-navigation');
      leading.onclick = () => {
        if (back) { directoryOpen = true; renderShell(); }
        else { menuOpen = true; renderShell(); }
      };
      titlebar.append(leading);
    }
    const title = el('div', 'window-title');
    if (route === 'chat' && !(narrow && directoryOpen)) title.append(avatar(24));
    title.append(el('span', '', route === 'chat' ? (narrow && directoryOpen ? t('assistants') : 'Pot') : t(route)));
    titlebar.append(title);
    if (surface === 'tray' && client.openMain) {
      const open = button('open', t('openMain'));
      open.onclick = async () => { try { await client.openMain(); } catch (reason) { error = reason.message; updateControls(); } };
      titlebar.append(open);
    }
    const more = button('more', t('more'));
    more.setAttribute('aria-haspopup', 'dialog');
    more.onclick = () => { preferencesOpen = true; renderPreferences(); preferences.showModal(); };
    titlebar.append(more);
    notice.textContent = t('notice');
    input.placeholder = t('composer');
    input.setAttribute('aria-label', t('composer'));
    history.setAttribute('aria-label', t('chat'));
    latest.title = t('latest');
    latest.setAttribute('aria-label', t('latest'));
    history.hidden = composerArea.hidden = route !== 'chat';
    unavailable.hidden = route === 'chat';
    unavailable.textContent = t('unavailable');
    // Search is window state and survives narrow conversation navigation/resizes.
    const searchValue = directory.querySelector('input')?.value || '';
    const scrollTop = directory.querySelector('.assistant-list')?.scrollTop || 0;
    directory.replaceChildren();
    const heading = el('div', 'directory-heading');
    heading.append(el('span', '', t('assistants')));
    const add = button('plus', t('preview'));
    add.disabled = true; // creation is intentionally outside this first preview.
    heading.append(add);
    const search = el('input', 'assistant-search');
    search.type = 'search'; search.placeholder = t('search'); search.setAttribute('aria-label', t('search')); search.value = searchValue;
    const list = el('div', 'assistant-list');
    const row = el('button', 'assistant-row is-selected');
    row.type = 'button';
    row.append(avatar());
    const labels = el('span', 'assistant-labels');
    const name = el('span', 'assistant-name', 'Pot');
    name.append(el('span', 'owner-badge', t('local')));
    labels.append(name, el('span', 'assistant-preview', t('preview')));
    row.append(labels);
    row.onclick = () => { directoryOpen = false; renderShell(); };
    const empty = el('p', 'hint', t('searchEmpty'));
    const filter = () => { row.hidden = !'pot'.includes(search.value.toLowerCase()); empty.hidden = !row.hidden; };
    search.oninput = filter;
    list.append(row, empty);
    directory.append(heading, search, list); filter(); list.scrollTop = scrollTop;
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
        if (message.role === 'tool' || message.role === 'system') bubble.textContent = message.text;
        else bubble.append(renderMarkdown(message.text));
        record.text = message.text; record.role = message.role;
        row.className = `message role-${message.role}`;
      }
      if (message.role === 'assistant' && message.text && !record.copy) {
        const copy = el('button', 'copy-message', t('copy'));
        copy.type = 'button';
        copy.onclick = async () => {
          try { await navigator.clipboard.writeText(record.text); copy.textContent = t('copied'); }
          catch (reason) { error = reason.message; updateControls(); }
        };
        row.append(copy); record.copy = copy;
      }
      if (record.copy) record.copy.textContent = t('copy');
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
    if (!acceptSnapshot(state, snapshot, receivedEpoch)) return;
    connection = 'connected';
    syncEditor();
    renderMessages(); updateControls();
  }
  const abort = new AbortController();
  async function loadSnapshot(reattach = false) {
    if (disposed) return;
    if (reattach) epoch = beginSubscription(state);
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
  // Subscribe first: a newer event wins over an older in-flight initial GET.
  const unsubscribe = client.subscribe(onSnapshot, value => {
    if (disposed) return;
    if (value === 'offline') disconnected = true;
    if (value === 'connected' && disconnected) {
      disconnected = false;
      // A transport reconnect establishes a fresh identity. In-flight GETs from
      // the previous attachment retain their captured epoch and cannot win.
      loadSnapshot(true);
    } else { connection = value; updateControls(); }
  });
  const keydown = event => {
    if (event.key === 'Escape' && menuOpen) { menuOpen = false; renderShell(); }
    if (event.metaKey && event.key === '[' && media.matches && !directoryOpen) { event.preventDefault(); directoryOpen = true; renderShell(); }
  };
  const mediaChanged = () => { menuOpen = false; renderShell(); };
  document.addEventListener('keydown', keydown);
  media.addEventListener('change', mediaChanged);
  viewport?.addEventListener('resize', viewportChanged);
  window.addEventListener('resize', viewportChanged);
  renderShell(); viewportChanged(); loadSnapshot();
  return () => {
    disposed = true; abort.abort(); unsubscribe(); editor.destroy();
    document.removeEventListener('keydown', keydown);
    media.removeEventListener('change', mediaChanged);
    viewport?.removeEventListener('resize', viewportChanged);
    window.removeEventListener('resize', viewportChanged);
    root.replaceChildren();
  };
}
