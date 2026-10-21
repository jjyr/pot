// Preserve visible content rather than a numeric scrollTop when rows above it
// are inserted, removed or moved. Keys also work after rebuilding a conversation.
export function captureReadingPosition(history,{ignoreToggle=false}={}) {
  const top=history.getBoundingClientRect().top,bottom=top+history.clientHeight;
  const anchors=[...history.querySelectorAll('[data-scroll-anchor]')].flatMap(node=>{
    if(ignoreToggle&&node.dataset.scrollAnchor==='background-tasks-toggle')return [];
    const rect=node.getBoundingClientRect();
    return rect.bottom>top&&rect.top<bottom?[{key:node.dataset.scrollAnchor,offset:rect.top-top}]:[];
  });
  return {top:history.scrollTop,anchors};
}

export function restoreReadingPosition(history,position) {
  const nodes=new Map([...history.querySelectorAll('[data-scroll-anchor]')].map(node=>[node.dataset.scrollAnchor,node]));
  const anchor=position?.anchors.find(item=>nodes.get(item.key)?.getBoundingClientRect().height>0);
  if(anchor){const top=history.getBoundingClientRect().top;history.scrollTop+=nodes.get(anchor.key).getBoundingClientRect().top-top-anchor.offset;}
  else if(position)history.scrollTop=position.top;
  return anchor;
}

export function readingAnchor(scroller) {
  const top = scroller.getBoundingClientRect().top;
  const nodes = [...scroller.querySelectorAll('article.message')];
  // Prefer the deepest message crossing the top, then the first visible row.
  const candidates = nodes.filter(row => { const box = row.getBoundingClientRect(); return box.height && box.bottom > top && box.top < top + scroller.clientHeight; });
  const row = candidates.findLast(row => row.getBoundingClientRect().top <= top + 4) || candidates[0];
  return row ? {id: row.dataset.messageId, offset: row.getBoundingClientRect().top - top, sequence: row.dataset.sequence} : null;
}
