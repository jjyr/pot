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
  const anchor=position?.anchors.find(item=>nodes.has(item.key));
  if(anchor){const top=history.getBoundingClientRect().top;history.scrollTop+=nodes.get(anchor.key).getBoundingClientRect().top-top-anchor.offset;}
  else if(position)history.scrollTop=position.top;
}
