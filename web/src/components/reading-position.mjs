// Preserve visible content rather than a numeric scrollTop when rows above it
// are inserted, removed or moved. Keys also work after rebuilding a conversation.
function visibleAnchor(node,history){
  if(!node||!node.getBoundingClientRect().height)return false;
  // Closed details can retain descendant layout boxes under content-visibility.
  // Those boxes are not readable anchors or evidence of an expanded child.
  for(let parent=node.parentElement;parent&&parent!==history;parent=parent.parentElement){
    if(parent.hidden||parent.localName==='details'&&!parent.open&&!parent.querySelector(':scope > summary')?.contains(node))return false;
  }
  return true;
}
export function captureReadingPosition(history,{ignoreToggle=false}={}) {
  const top=history.getBoundingClientRect().top,bottom=top+history.clientHeight;
  const anchors=[...history.querySelectorAll('[data-scroll-anchor]')].flatMap(node=>{
    if(ignoreToggle&&node.dataset.scrollAnchor==='background-tasks-toggle'||!visibleAnchor(node,history))return [];
    const rect=node.getBoundingClientRect();
    return rect.bottom>top&&rect.top<bottom?[{key:node.dataset.scrollAnchor,offset:rect.top-top}]:[];
  });
  return {top:history.scrollTop,anchors};
}

export function resolveReadingPosition(history,position) {
  const nodes=new Map([...history.querySelectorAll('[data-scroll-anchor]')].map(node=>[node.dataset.scrollAnchor,node]));
  let anchor=position?.anchors.find((item,index)=>index===0&&visibleAnchor(nodes.get(item.key),history));
  if(!anchor&&position?.anchor?.id){
    const row=[...history.querySelectorAll('article.message')].find(row=>(row.dataset.messageId===position.anchor.id||row.dataset.anchorMessageId===position.anchor.id)&&visibleAnchor(row,history));
    if(row){anchor={key:row.dataset.scrollAnchor,offset:position.anchor.offset};nodes.set(anchor.key,row);}
  }
  anchor ||= position?.anchors.find(item=>visibleAnchor(nodes.get(item.key),history));
  const top=anchor?history.scrollTop+nodes.get(anchor.key).getBoundingClientRect().top-history.getBoundingClientRect().top-anchor.offset:position?.top;
  return {anchor,top};
}

export function readingAnchor(scroller) {
  const top = scroller.getBoundingClientRect().top;
  const nodes = [...scroller.querySelectorAll('article.message')];
  // Prefer the deepest message crossing the top, then the first visible row.
  const candidates = nodes.filter(row => { const box = row.getBoundingClientRect(); return visibleAnchor(row,scroller) && box.bottom > top && box.top < top + scroller.clientHeight; });
  const row = candidates.findLast(row => row.getBoundingClientRect().top <= top + 4) || candidates[0];
  return row ? {id: row.dataset.anchorMessageId||row.dataset.messageId, offset: row.getBoundingClientRect().top - top, sequence: row.dataset.sequence} : null;
}
