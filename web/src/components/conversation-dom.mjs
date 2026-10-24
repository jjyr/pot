export function moveConversationNode(host,child,before=null){
  if(child.parentNode===host&&child.nextSibling===before)return;
  // Keep explicit selection endpoints too: browser implementations of atomic
  // moves differ in how they retain a range across a native details subtree.
  const selection=getSelection(),selected=selection?.rangeCount&&!selection.isCollapsed
    &&(child.contains(selection.anchorNode)||child.contains(selection.focusNode));
  const endpoints=selected?[selection.anchorNode,selection.anchorOffset,selection.focusNode,selection.focusOffset]:null;
  const focus=child.contains(document.activeElement)?document.activeElement:null;
  if(host.moveBefore&&child.isConnected&&host.isConnected)host.moveBefore(child,before);
  else host.insertBefore(child,before);
  if(focus&&document.activeElement!==focus)focus.focus({preventScroll:true});
  if(endpoints&&endpoints[0].isConnected&&endpoints[2].isConnected)selection.setBaseAndExtent(...endpoints);
}
