// Input ownership only. Reading intent and geometry live in the list controller.
export function bindConversationInput(history,{engage,endPointer,endMotion,endTouch=()=>{}}) {
  const listeners=[],view=history.ownerDocument.defaultView;
  let touchY=null;
  const listen=(target,name,run)=>{target.addEventListener(name,run,{passive:true});listeners.push([target,name,run]);};
  function owns(event,up){
    if(event.defaultPrevented)return false;
    for(let node=event.target instanceof Element?event.target:null;node&&node!==history;node=node.parentElement){
      if(node.scrollHeight>node.clientHeight&&/^(auto|scroll)$/.test(getComputedStyle(node).overflowY)){
        if(up?node.scrollTop>0:node.scrollTop+node.clientHeight<node.scrollHeight-1)return false;
      }
    }
    return true;
  }
  function input(event,kind,up=false){if(owns(event,up))return engage({event,kind,up});return false;}
  listen(history,'wheel',event=>{if(!event.ctrlKey&&!event.metaKey&&Math.abs(event.deltaY)>Math.abs(event.deltaX))input(event,'wheel',event.deltaY<0);});
  listen(history,'pointerdown',event=>input(event,'pointer'));
  listen(view,'pointerup',()=>endPointer());
  // A pointer cancellation may hand the gesture to native touch scrolling.
  listen(view,'pointercancel',()=>endPointer());
  listen(history,'scrollend',()=>endMotion());
  listen(history,'touchstart',event=>{touchY=event.touches.length===1?event.touches[0].clientY:null;if(touchY!==null)input(event,'touchstart');});
  listen(history,'touchmove',event=>{const next=event.touches.length===1?event.touches[0].clientY:null;if(next!==null&&touchY!==null&&next!==touchY)input(event,'touchmove',next>touchY);touchY=next;});
  const releaseTouch=()=>{touchY=null;endTouch();};
  listen(history,'touchend',releaseTouch);listen(history,'touchcancel',releaseTouch);
  listen(history,'keydown',event=>{
    if(event.altKey||event.target.closest('input,textarea,select,[contenteditable]'))return;
    if((event.ctrlKey||event.metaKey)&&!['ArrowUp','ArrowDown','Home','End'].includes(event.key))return;
    if(event.key!=='Tab'&&event.target.closest('button,a,summary')&&[' ','Enter'].includes(event.key))return;
    if(['ArrowUp','ArrowDown','PageUp','PageDown','Home','End','Tab',' '].includes(event.key))input(event,'key',['ArrowUp','PageUp','Home'].includes(event.key)||event.key===' '&&event.shiftKey);
  });
  return {engage:event=>input(event,'pointer'),close(){for(const [target,name,run]of listeners)target.removeEventListener(name,run);}};
}
