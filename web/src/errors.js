// Keep crash reporting outside the product tree so a failed render cannot hide it.
(() => {
  const storageKey = 'pot-ui-errors';
  const limit = 20;
  const fallback = {
    'web.error.title': 'The interface encountered an error',
    'web.error.message': 'Some controls may stop responding. Refresh the page to recover.',
    'web.error.reload': 'Refresh page',
    'web.error.details': 'View details',
    'web.error.close': 'Close',
    'web.error.log': 'Error log',
    'web.error.copy': 'Copy log',
    'web.error.clear': 'Clear log',
    'web.error.report': 'Report issue',
    'web.error.issueTitle': 'Interface error: {message}',
    'web.error.unknownVersion': 'Unknown (please fill in)',
    'web.error.reportTruncated': 'The log was shortened to fit the link. Use Copy log in Pot to add the full log.',
    'web.error.reportFailed': 'Could not open GitHub. Please copy the log and report the issue manually.',
    'web.error.copied': 'Log copied',
    'web.error.copy-failed': 'Could not copy. Select and copy the log below.',
  };
  let translate = key => fallback[key];
  const t = key => translate(key) || fallback[key];
  let issueContext = {};
  let records = [];
  const observed = new WeakSet();
  let toast, details, log, launcher, copyStatus, timer;
  let pendingShow = false, hovered = false, dismissedAt = -Infinity;

  function clean(value, length) {
    return String(value ?? '')
      .replace(/https?:\/\/[^\s<>"')]+/g, raw => {
        try {
          const url = new URL(raw);
          url.username = ''; url.password = ''; url.search = ''; url.hash = '';
          return url.href;
        } catch { return '[URL]'; }
      })
      .replace(/\bBearer\s+\S+|\bsk-[\w-]+/gi, '[credential]')
      .replace(/\b(?:api[_ -]?key|access[_ -]?token|authorization)\s*[=:]\s*["']?[^\s,"';]+/gi, '[credential]')
      .slice(0, length);
  }

  function restore() {
    try {
      const saved = JSON.parse(sessionStorage.getItem(storageKey) || '[]');
      if (!Array.isArray(saved)) return;
      records = saved.slice(-limit).filter(record => record && typeof record.time === 'string').map(record => ({
        time: clean(record.time, 32), lastSeen: clean(record.lastSeen, 32),
        kind: clean(record.kind, 32), message: clean(record.message, 2048),
        stack: clean(record.stack, 6144), page: clean(record.page, 32),
        count: Number.isSafeInteger(record.count) && record.count > 0 ? record.count : 1,
      }));
    } catch { /* Storage can be disabled or contain an incomplete previous write. */ }
  }

  function persist() {
    try { sessionStorage.setItem(storageKey, JSON.stringify(records)); } catch { /* Console and in-memory logs still work. */ }
  }

  function text(items = records) {
    return items.map(record => `${record.time} [${record.kind}] ${record.page}\n${record.message}\n${record.stack}\nCount: ${record.count}; last seen: ${record.lastSeen}`).join('\n\n');
  }

  function issueURL() {
    const latest = records.reduce((last, record) => !last || record.lastSeen > last.lastSeen ? record : last, null);
    const url = new URL('https://github.com/jjyr/pot/issues/new');
    url.searchParams.set('template', 'bug_report.yml');
    url.searchParams.set('title', t('web.error.issueTitle').replace('{message}', () => (latest?.message || '').split('\n')[0].slice(0, 120)));
    url.searchParams.set('affected-area', issueContext.desktop ? 'Desktop app' : 'Web browser');
    url.searchParams.set('pot-version', clean(issueContext.version || t('web.error.unknownVersion'), 128));
    url.searchParams.set('environment', clean(navigator.userAgent || '', 256));
    // Indentation keeps diagnostic text literal in GitHub Markdown. Keep the
    // latest occurrence first so a long history cannot crowd out the new fault.
    const ordered = [...records].sort((a, b) => b.lastSeen.localeCompare(a.lastSeen));
    const lines = text(ordered).split('\n').map(line => `    ${line}`).join('\n');
    const setLog = value => url.searchParams.set('additional-context', value);
    setLog(lines);
    if (url.href.length > 7800) {
      const chars = Array.from(lines), notice = `\n\n${t('web.error.reportTruncated')}`;
      let low = 0, high = chars.length;
      while (low < high) {
        const middle = Math.ceil((low + high) / 2);
        setLog(chars.slice(0, middle).join('') + notice);
        if (url.href.length <= 7800) low = middle;
        else high = middle - 1;
      }
      setLog(chars.slice(0, low).join('') + notice);
    }
    return url.href;
  }

  function pause() { clearTimeout(timer); }
  function schedule() {
    pause();
    if (toast && !toast.hidden && !details.open && !hovered && !toast.contains(document.activeElement)) {
      timer = setTimeout(hide, 8000);
    }
  }
  function hide() {
    pause();
    hovered = false;
    toast.hidden = true;
    dismissedAt = Date.now();
  }
  function show(expanded = false) {
    if (!toast) { pendingShow = true; return; }
    toast.hidden = false;
    details.open = expanded;
    copyStatus.textContent = '';
    render();
    schedule();
  }

  function render() {
    if (!toast) return;
    for (const element of toast.querySelectorAll('[data-error-text]')) {
      const key = element.dataset.errorText;
      element.textContent = translate(key) || fallback[key];
    }
    launcher.textContent = `${translate('web.error.log') || fallback['web.error.log']} (${records.length})`;
    launcher.hidden = records.length === 0;
    log.textContent = text();
  }

  function report(kind, error, message, source = '') {
    try {
      const now = new Date().toISOString();
      const page = new URLSearchParams(location.search).get('page') || 'assistants';
      const record = {
        time: now, lastSeen: now, kind,
        message: clean(message || error?.message || error || 'Unknown error', 2048),
        stack: clean(error?.stack || source, 6144),
        page: ['chat', 'assistants', 'task', 'tasks', 'machines', 'settings', 'usage', 'schedules', 'conversation-usage', 'quota'].includes(page) ? page : 'unknown',
        count: 1,
      };
      const previous = records.find(item => item.kind === kind && item.message === record.message && item.stack === record.stack);
      const current = previous || record;
      const firstInPage = !observed.has(current);
      observed.add(current);
      if (previous) { previous.count++; previous.lastSeen = now; }
      else { records.push(record); records = records.slice(-limit); }
      persist();
      // Do not log event payloads, form values or the full page URL.
      if (firstInPage) console.error('[Pot UI error]', current);
      render();
      // A panic can cause a cascade of traps. Preserve the current toast and its timer.
      if ((!toast || toast.hidden) && firstInPage && Date.now() - dismissedAt > 1000) show();
    } catch { /* Reporting must never throw into the application's failing event loop. */ }
  }

  function element(tag, key) {
    const node = document.createElement(tag);
    if (key) node.dataset.errorText = key;
    return node;
  }

  function mount() {
    toast = element('section');
    toast.id = 'pot-error-toast';
    toast.hidden = true;
    toast.setAttribute('role', 'alert');
    const title = element('strong', 'web.error.title');
    const message = element('p', 'web.error.message');
    const actions = element('div');
    actions.className = 'pot-error-actions';
    const reload = element('button', 'web.error.reload');
    reload.onclick = () => location.reload();
    const close = element('button', 'web.error.close');
    close.onclick = hide;
    actions.append(reload, close);
    details = element('details');
    details.addEventListener('toggle', schedule);
    const summary = element('summary', 'web.error.details');
    const copy = element('button', 'web.error.copy');
    const clear = element('button', 'web.error.clear');
    const reportIssue = element('button', 'web.error.report');
    reportIssue.onclick = async () => {
      try {
        const url = issueURL();
        if (issueContext.openExternal) await issueContext.openExternal(url);
        else window.open(url, '_blank', 'noopener,noreferrer');
      } catch {
        copyStatus.textContent = t('web.error.reportFailed');
      }
    };
    clear.onclick = () => {
      records = [];
      persist();
      hide();
      dismissedAt = -Infinity;
      details.open = false;
      copyStatus.textContent = '';
      render();
    };
    copyStatus = element('span');
    copyStatus.setAttribute('role', 'status');
    copy.onclick = async () => {
      try {
        await navigator.clipboard.writeText(text());
        copyStatus.textContent = translate('web.error.copied') || fallback['web.error.copied'];
      } catch {
        copyStatus.textContent = translate('web.error.copy-failed') || fallback['web.error.copy-failed'];
      }
    };
    log = element('pre');
    details.append(summary, copy, clear, reportIssue, copyStatus, log);
    toast.append(title, message, actions, details);
    toast.addEventListener('mouseenter', () => { hovered = true; pause(); });
    toast.addEventListener('mouseleave', () => { hovered = false; schedule(); });
    toast.addEventListener('focusin', pause);
    toast.addEventListener('focusout', () => setTimeout(schedule, 0));
    launcher = element('button');
    launcher.id = 'pot-error-log';
    launcher.onclick = () => show(true);
    document.body.append(toast, launcher);
    render();
    if (pendingShow) { pendingShow = false; show(); }
  }

  restore();
  window.potErrors = {
    setTranslator(value) { translate = value; render(); },
    configureIssueReport({version, desktop, openExternal}) { issueContext = {version, desktop, openExternal}; },
    reportPanic(message) { report('panic', new Error(message), message); },
    logs() { return records.map(record => ({...record})); },
  };
  window.potReportPanic = message => window.potErrors.reportPanic(message);
  window.addEventListener('error', event => {
    // Browsers defer undelivered resize observations to another frame. These
    // notifications carry no exception; leave browser diagnostics intact.
    if (!event.error && (event.message === 'ResizeObserver loop completed with undelivered notifications.' || event.message === 'ResizeObserver loop limit exceeded')) return;
    if (event.message) report('error', event.error, event.message, `${event.filename || ''}:${event.lineno || 0}:${event.colno || 0}`);
  });
  window.addEventListener('unhandledrejection', event => report('unhandledrejection', event.reason));
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', mount, {once: true});
  else mount();
})();
