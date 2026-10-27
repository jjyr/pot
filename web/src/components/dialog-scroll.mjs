// A modal's inert background can still receive native scroll chaining. Lock the
// launching scroll surfaces as well as the document, and restore their state.
export function lockDialogScroll(trigger){
  const saved=[];
  for(let element=trigger;element;element=element.parentElement){
    if(element!==document.documentElement&&element.scrollHeight<=element.clientHeight&&element.scrollWidth<=element.clientWidth)continue;
    saved.push({element,top:element.scrollTop,left:element.scrollLeft,value:element.style.getPropertyValue('overflow'),priority:element.style.getPropertyPriority('overflow')});
    element.style.setProperty('overflow','hidden','important');
  }
  let released=false;
  return ()=>{
    if(released)return;released=true;
    for(const {element,top,left,value,priority} of saved){
      if(value)element.style.setProperty('overflow',value,priority);else element.style.removeProperty('overflow');
      element.scrollTop=top;element.scrollLeft=left;
    }
  };
}
