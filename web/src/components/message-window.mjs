// Measured DOM windows share the chat scroller, including nested tool lists.
// Data, disclosure memory and send lifecycle live outside recycled DOM nodes.
export function createMessageWindow({scroller, host, render, dispose, requestRender, compensate, heights = new Map()}) {
  let rows = [], mounted = new Map(), closed = false, previousTop = 0, previousTime = 0, previousFrame = -1, travel = 0;
  const cached = new Map();
  const sources = new WeakMap(), weights = new WeakMap();
  let cachedBytes = 0;
  const spacers = [];
  function retainedBytes(record) {
    const source = sources.get(record);
    let bytes = weights.get(source);
    if (bytes === undefined) {
      bytes = 512;
      const pending = [source], seen = new Set();
      while (pending.length && bytes <= 2 * 1024 * 1024) {
        const value = pending.pop();
        if (typeof value === 'string') bytes += value.length * 2;
        else if (value && typeof value === 'object' && !seen.has(value)) {
          seen.add(value); bytes += 64;
          if (Array.isArray(value)) bytes += value.length * 8;
          if (bytes <= 2 * 1024 * 1024) for (const child of Object.values(value)) pending.push(child);
        }
      }
      weights.set(source, bytes);
    }
    return bytes + record.row.textContent.length * 2;
  }
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
    const now = performance.now(), stamp = document.timeline?.currentTime ?? now;
    // All measurement passes in one frame share the same look-ahead. Anchor
    // compensation must not replace the reader's direction mid-layout.
    if (stamp !== previousFrame) {
      const elapsed = now - previousTime;
      travel = elapsed > 0 && elapsed < 200 ? (scroller.scrollTop - previousTop) / elapsed * 120 : 0;
      previousTop = scroller.scrollTop; previousTime = now; previousFrame = stamp;
    }
    if (!rows.length) { previousTime = 0; previousFrame = -1; travel = 0; }
    if (target) {
      let offset = 0;
      for (const item of rows) {
        if (item.id === target.id || item.viewKey === target.id) { start = offset - (target.offset ?? scroller.clientHeight / 3); break; }
        offset += estimate(item);
      }
      if (target.end) start = Math.max(0, offset - scroller.clientHeight);
    }
    const overscan = Math.max(600, scroller.clientHeight * 2);
    const ahead = Math.min(scroller.clientHeight * 2, Math.abs(travel));
    const end = start + scroller.clientHeight + overscan + (travel > 0 ? ahead : 0);
    start -= overscan + (travel < 0 ? ahead : 0);
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
    // Prepare incoming rows before retiring the old window. A nearby return
    // reuses its prose/disclosures; disconnected media release their own leases.
    for (const part of layout) if (part.item) {
      const key = part.item.viewKey, retained = cached.get(key);
      if (retained) { cached.delete(key); cachedBytes -= retained.bytes; }
      part.record = render(part.item, mounted.get(key) || retained?.record);
      sources.set(part.record, part.item);
    }
    const available = new Set(rows.map(item => item.viewKey));
    for (const [key, record] of mounted) if (!selected.has(key)) {
      observer.unobserve(record.row); record.row.remove(); mounted.delete(key);
      if (available.has(key)) {
        const bytes = retainedBytes(record);
        cached.set(key, {record, bytes}); cachedBytes += bytes;
      } else dispose(record);
    }
    for (const [key, value] of cached) if (!available.has(key)) { cached.delete(key); cachedBytes -= value.bytes; dispose(value.record); }
    while (cached.size > 32 || cachedBytes > 2 * 1024 * 1024) {
      const [key, value] = cached.entries().next().value;
      cached.delete(key); cachedBytes -= value.bytes; dispose(value.record);
    }
    let index = 0;
    for (const part of layout) {
      let element;
      if (part.gap) {
        element = spacers[spacerIndex];
        if (!element) { element = document.createElement('div'); element.className = 'message-window-spacer'; element.setAttribute('aria-hidden', 'true'); spacers[spacerIndex] = element; }
        spacerIndex++; element.style.height = `${part.gap}px`;
      } else {
        const key = part.item.viewKey;
        const record = part.record;
        mounted.set(key, record); element = record.row; element.dataset.windowKey = key; observer.observe(element);
      }
      if (host.children[index] !== element) host.insertBefore(element, host.children[index] || null);
      index++;
    }
    while (host.children.length > index) host.lastElementChild.remove();
    if(shift)compensate(shift);
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
  }, close() { closed = true; observer.disconnect(); for (const record of mounted.values()) dispose(record); for (const value of cached.values()) dispose(value.record); mounted.clear(); cached.clear(); cachedBytes = 0; for (const spacer of spacers) spacer.remove(); }};
}
