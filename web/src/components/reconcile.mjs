// Patch a read-only projection without replacing its live controls or containers.
// Event handlers must use event.currentTarget or persistent view state, never
// references to the temporary projection's nodes. Stateful forms use view models.
const handlers=['onclick','onchange','oninput','onsubmit','onkeydown','onpointerenter','onpointerleave','onfocus','onblur'];
const key=node=>node.nodeType===1?(node.dataset.viewKey||node.id||''):'';
const compatible=(a,b)=>a.nodeType===b.nodeType&&(a.nodeType!==1||(a.tagName===b.tagName&&key(a)===key(b)));
const disabledState=new WeakMap();
export function setDisabled(control,disabled){disabledState.set(control,!!disabled);control.disabled=!!disabled||control.dataset.pending==='true';}
export function setPending(control,pending){
  if(pending){if(!disabledState.has(control))disabledState.set(control,control.disabled);control.dataset.pending='true';control.disabled=true;}
  else{delete control.dataset.pending;control.disabled=disabledState.get(control)||false;}
}
export function reconcile(target,source) {
  if(target.nodeType===3){if(target.data!==source.data)target.data=source.data;return target;}
  const focused=target.contains(document.activeElement)?document.activeElement:null;
  const scrolls=[target,...target.querySelectorAll('[data-scroll-key],.table-scroll')].map(node=>[node,node.scrollTop,node.scrollLeft]);
  for(const attribute of [...target.attributes])if(!source.hasAttribute(attribute.name)&&!['open','data-pending'].includes(attribute.name))target.removeAttribute(attribute.name);
  for(const attribute of source.attributes)if(attribute.name!=='open'&&target.getAttribute(attribute.name)!==attribute.value)target.setAttribute(attribute.name,attribute.value);
  for(const name of handlers)target[name]=source[name];
  if('disabled'in source)setDisabled(target,source.disabled);
  if('value'in source&&target.value!==source.value)target.value=source.value;
  if('checked'in source)target.checked=source.checked;
  {
    const old=[...target.childNodes],keyed=new Map(old.filter(node=>key(node)).map(node=>[key(node),node])),used=new Set();
    for(const [index,next]of [...source.childNodes].entries()){
      let node=key(next)?keyed.get(key(next)):old.find(node=>!used.has(node)&&!key(node)&&compatible(node,next));
      if(!node||!compatible(node,next)){node=next;}else reconcile(node,next);
      used.add(node);if(target.childNodes[index]!==node)target.insertBefore(node,target.childNodes[index]||null);
    }
    for(const node of old)if(!used.has(node))node.remove();
  }
  if(focused?.isConnected&&document.activeElement!==focused)focused.focus({preventScroll:true});
  for(const [node,top,left]of scrolls){node.scrollTop=top;node.scrollLeft=left;}
  return target;
}

export function keyedRows(parent,entries,items,{key,create,update}) {
  const retained=new Set(),focused=parent.contains(document.activeElement)?document.activeElement:null,top=parent.scrollTop;
  for(const [index,item]of items.entries()){
    const id=key(item);retained.add(id);let entry=entries.get(id);
    if(!entry){entry=create(item);entries.set(id,entry);}entry.item=item;update(entry,item);
    if(parent.children[index]!==entry.element)parent.insertBefore(entry.element,parent.children[index]||null);
  }
  for(const [id,entry]of entries)if(!retained.has(id)){entry.element.remove();entry.dispose?.();entries.delete(id);}
  if(focused?.isConnected&&document.activeElement!==focused)focused.focus({preventScroll:true});parent.scrollTop=top;
}
