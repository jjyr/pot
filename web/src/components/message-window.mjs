// Measured DOM windows share the chat scroller, including nested tool lists.
// Data, disclosure memory and send lifecycle live outside recycled DOM nodes.
export function createMessageWindow({scroller, host, render, dispose, requestRender, heights = new Map()}) {
  let rows = [], mounted = new Map(), closed = false;
  const spacers = [];
  function measure() {
    let changed = false;
    for (const [key, record] of mounted) {
      changed = record.childrenWindow?.measure() || changed;
      const style = getComputedStyle(record.row), box = record.row.getBoundingClientRect();
      const height = box.height + parseFloat(style.marginBottom || 0) + parseFloat(style.marginTop || 0);
      if (box.height > 0 && Math.abs((heights.get(key) ?? -1) - height) > .5) { heights.set(key, height); changed = true; }
    }
    return changed;
  }
  const observer = new ResizeObserver(() => {
    // display:none has no usable measurement. Margins alone must never replace
    // the last visible height while a conversation is behind another page.
    const changed = measure();
    while (heights.size > 1280) heights.delete(heights.keys().next().value);
    if (changed && !closed) requestRender(true);
  });
  const estimate=item=>heights.get(item.viewKey)??(item.role==='user'?120:item.role==='tool'?70:180);
  function set(next,{anchor,target}={}) {
    let shift=0;
    if(anchor){
      const root=rows.find(item=>item.id===anchor||[...(mounted.get(item.viewKey)?.row.querySelectorAll('article.message')||[])].some(row=>row.dataset.messageId===anchor));
      if(root&&next.some(item=>item.viewKey===root.viewKey)){
        const prefix=list=>{let offset=0;for(const item of list){if(item.viewKey===root.viewKey)return offset;offset+=estimate(item);}return offset;};
        shift=prefix(next)-prefix(rows);
      }
    }
    rows = next;
    const viewport = scroller.getBoundingClientRect(), box = host.getBoundingClientRect();
    let start = viewport.top - box.top + shift;
    if (target) {
      let offset = 0;
      for (const item of rows) {
        if (item.id === target.id || item.viewKey === target.id) { start = offset - (target.offset ?? scroller.clientHeight / 3); break; }
        offset += estimate(item);
      }
      if (target.end) start = Math.max(0, offset - scroller.clientHeight);
    }
    const end = start + scroller.clientHeight + 600;
    start -= 600;
    let offset = 0, gap = 0, spacerIndex = 0;
    const selected = new Set(), layout = [];
    const selection = getSelection();
    for (const item of rows) {
      const key = item.viewKey, record = mounted.get(key);
      const height = heights.get(key) ?? (item.role === 'user' ? 120 : item.role === 'tool' ? 70 : 180);
      const pinned = record && (record.row.contains(document.activeElement) || selection?.rangeCount && !selection.isCollapsed && (record.row.contains(selection.anchorNode) || record.row.contains(selection.focusNode)));
      if (offset + height >= start && offset <= end || pinned) {
        if (gap) { layout.push({gap}); gap = 0; }
        selected.add(key); layout.push({item});
      } else gap += height;
      offset += height;
    }
    if (gap) layout.push({gap});
    for (const [key, record] of mounted) if (!selected.has(key)) { observer.unobserve(record.row); dispose(record); mounted.delete(key); }
    let index = 0;
    for (const part of layout) {
      let element;
      if (part.gap) {
        element = spacers[spacerIndex];
        if (!element) { element = document.createElement('div'); element.className = 'message-window-spacer'; element.setAttribute('aria-hidden', 'true'); spacers[spacerIndex] = element; }
        spacerIndex++; element.style.height = `${part.gap}px`;
      } else {
        const key = part.item.viewKey;
        const record = render(part.item, mounted.get(key));
        mounted.set(key, record); element = record.row; element.dataset.windowKey = key; observer.observe(element);
      }
      if (host.children[index] !== element) host.insertBefore(element, host.children[index] || null);
      index++;
    }
    while (host.children.length > index) host.lastElementChild.remove();
    if(shift)scroller.scrollTop+=shift;
  }
  return {set, measure, layout(next,{anchor,target,restore}={}) {
    // Resolve estimates, mount the destination and measure it in the same paint.
    // The bound keeps pathological content from monopolising the UI thread;
    // later media/font resizes still use the ordinary observer/anchor path.
    let top = scroller.scrollTop;
    for (let pass = 0; pass < 6; pass++) {
      set(next,{anchor,target});
      const changed = measure();
      restore?.();
      if (!changed && Math.abs(scroller.scrollTop - top) < .5) break;
      top = scroller.scrollTop;
    }
  }, seek(id) {
    let offset=0;
    for(const item of rows){if(item.id===id||item.viewKey===id){scroller.scrollTop+=host.getBoundingClientRect().top-scroller.getBoundingClientRect().top+offset-scroller.clientHeight/3;requestRender();return true;}offset+=heights.get(item.viewKey)??(item.role==='user'?120:item.role==='tool'?70:180);}
    return false;
  }, close() { closed = true; observer.disconnect(); for (const record of mounted.values()) dispose(record); mounted.clear(); for (const spacer of spacers) spacer.remove(); }};
}

export function readingAnchor(scroller) {
  const top = scroller.getBoundingClientRect().top;
  const nodes = [...scroller.querySelectorAll('article.message')];
  // Prefer the deepest message crossing the top, then the first visible row.
  const candidates = nodes.filter(row => { const box = row.getBoundingClientRect(); return box.height && box.bottom > top && box.top < top + scroller.clientHeight; });
  const row = candidates.findLast(row => row.getBoundingClientRect().top <= top + 4) || candidates[0];
  return row ? {id: row.dataset.messageId, offset: row.getBoundingClientRect().top - top, sequence: row.dataset.sequence} : null;
}
export function restoreReadingAnchor(scroller, anchor) {
  if (!anchor) return;
  const row = [...scroller.querySelectorAll('article.message')].find(row => row.dataset.messageId === anchor.id);
  if (row) scroller.scrollTop += row.getBoundingClientRect().top - scroller.getBoundingClientRect().top - anchor.offset;
}
