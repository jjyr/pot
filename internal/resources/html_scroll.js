// Runs inside the opaque-origin preview, before author scripts. Contain the
// viewport's native scroll chain only when the document has somewhere to scroll.
// Short documents therefore keep the browser's ordinary handoff to the chat.
(() => {
  const boundary = document.createElement('style');
  document.head.append(boundary);
  let last = '', path = [], nested = [], scheduled = false;
  const scrollable = (element, axis) => {
    const style = getComputedStyle(element);
    return /^(auto|scroll|overlay)$/.test(style[axis === 'x' ? 'overflowX' : 'overflowY']) &&
      (axis === 'x' ? element.scrollWidth > element.clientWidth : element.scrollHeight > element.clientHeight);
  };
  const update = () => {
    const root = document.scrollingElement;
    if (!root) return;
    const rules = ['x', 'y'].filter(axis => {
      const property = axis === 'x' ? 'overflowX' : 'overflowY';
      const rootOverflow = getComputedStyle(root)[property];
      const overflow = rootOverflow === 'visible' && document.body ? getComputedStyle(document.body)[property] : rootOverflow;
      const viewport = !/^(hidden|clip)$/.test(overflow) &&
        (axis === 'x' ? root.scrollWidth > root.clientWidth : root.scrollHeight > root.clientHeight);
      return viewport || [...nested, ...path].some(element => element instanceof Element && element.isConnected && scrollable(element, axis));
    }).map(axis => `overscroll-behavior-${axis}:contain !important`).join(';');
    if (rules !== last) { boundary.textContent = `:root{${rules}}`; last = rules; }
  };
  const input = event => { path = event.composedPath(); update(); };
  // No event is cancelled or rewritten. Native scrolling, controls, gestures,
  // keyboard defaults and author listeners retain ownership of the input.
  for (const type of ['pointerover', 'wheel', 'touchstart', 'focusin']) {
    document.addEventListener(type, input, {capture: true, passive: true});
  }
  // An already exhausted nested list must be contained before the compositor
  // starts its first wheel gesture, even after clicking a control outside it.
  // Inspect scroll surfaces without modifying their overflow or event handlers.
  const observed = new Set();
  const refresh = () => {
    scheduled = false;
    nested = [...document.querySelectorAll('body, body *')].filter(element =>
      (element.scrollHeight > element.clientHeight || element.scrollWidth > element.clientWidth) &&
      (scrollable(element, 'x') || scrollable(element, 'y')));
    const next = new Set([document.documentElement, document.body, ...nested].filter(Boolean));
    for (const element of observed) if (!next.has(element)) { resize.unobserve(element); observed.delete(element); }
    for (const element of next) if (!observed.has(element)) { resize.observe(element); observed.add(element); }
    update();
  };
  const schedule = () => { if (!scheduled) { scheduled = true; requestAnimationFrame(refresh); } };
  const resize = new ResizeObserver(schedule);
  document.addEventListener('DOMContentLoaded', () => {
    refresh();
    new MutationObserver(records => { if (records.some(record => record.target !== boundary)) schedule(); })
      .observe(document.documentElement, {subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['style', 'class', 'hidden']});
    document.addEventListener('load', schedule, true);
  }, {once: true});
})();
