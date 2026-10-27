import {moveConversationNode} from './conversation-dom.mjs';
// A preparation is local to one render transaction. It carries no backend or
// persisted state. Async component work stays owned by its candidate DOM tree.
import {translate} from '../locales/messages.mjs';
const components='pot-resource,pot-message-file,pot-mermaid,pot-file-image';
let rendering;
const componentKey=element=>JSON.stringify([element.localName,element.renderSlot||'',
  element.identity||element.sourceText&&[element.sourceText,element.closed]||element.file&&[element.file,element.label]||
  element.presentation&&[element.presentation,element.line,element.noticeKey]]);
const deadlineKey=element=>element.localName==='pot-mermaid'&&element.renderSlot?JSON.stringify([element.localName,element.renderSlot]):componentKey(element);
const deadlineMember=(element,fallback)=>element.closest('article[data-message-id]')?.dataset.messageId||fallback;
const componentDeadlineKey=(element,member)=>JSON.stringify([deadlineMember(element,member),deadlineKey(element)]);
const imageDeadlineKey=(element,member)=>JSON.stringify([deadlineMember(element,member),'image',element.getAttribute('src')]);

// Only a committed absence or a real unmount ends a logical component lifetime.
// Candidate replacement and changing Mermaid tokens keep the existing deadline.
export function retirePreparedDeadlines(deadlines,members,root,member=''){
  const owned=new Set(members),present=new Set();
  if(root){for(const element of root.querySelectorAll(components))present.add(componentDeadlineKey(element,member));for(const image of root.querySelectorAll('img'))present.add(imageDeadlineKey(image,member));}
  for(const key of deadlines.keys())if(key!=='fonts'&&owned.has(JSON.parse(key)[0])&&!present.has(key))deadlines.delete(key);
}

export function currentPreparedRender(){return rendering;}
export function createPreparationBudget(limit=32){
  let active=0,scheduled=false;const queue=[];
  const schedule=()=>{if(!scheduled){scheduled=true;queueMicrotask(()=>{scheduled=false;while(active<limit&&queue.length){const entry=queue.shift();if(entry.cancelled||!entry.element.isConnected||!entry.context.alive){entry.release();continue;}active++;entry.active=true;entry.start();}});}};
  return {enqueue(entry){entry.release=()=>{if(entry.cancelled)return;entry.cancelled=true;entry.observer?.disconnect();entry.placeholder.remove();entry.element.preparationTicket=null;entry.element.preparationAdmitted=false;if(entry.active)active--;schedule();};queue.push(entry);schedule();}};
}
export function deferPreparedConnection(element,start){
  if(element.preparationAdmitted)return false;
  let context;for(let parent=element;parent&&!context;parent=parent.parentElement)context=parent.preparedContext;
  if(!context?.alive)return false;
  if(element.preparationTicket)return true;
  const placeholder=element.ownerDocument.createElement('span');placeholder.className='resource-status';placeholder.setAttribute('role','status');placeholder.textContent=translate(element.ownerDocument.documentElement.lang,'resource.loading');element.append(placeholder);element.dataset.state='loading';
  const entry={element,context,placeholder,start(){
    placeholder.remove();element.preparationAdmitted=true;
    entry.observer=new MutationObserver(()=>{if(element.dataset.state!=='loading')entry.release();});entry.observer.observe(element,{attributes:true,attributeFilter:['data-state']});start();
    if(element.dataset.state!=='loading')entry.release();
  }};element.preparationTicket=entry;context.budget.enqueue(entry);return true;
}
export function preparedLayoutRect(node){
  const box=node.getBoundingClientRect(),context=rendering?.context;
  if(context?.phase!=='prepare'||!context.root||context.liveTop===undefined)return box;
  const top=(typeof context.liveTop==='function'?context.liveTop():context.liveTop)+box.top-context.root.getBoundingClientRect().top;
  return {top,bottom:top+box.height,left:box.left,right:box.right,width:box.width,height:box.height};
}
export function withPreparedRender(context,member,work){
  const previous=rendering;rendering={context,member};
  try{return work();}finally{rendering=previous;}
}

export function createPreparedRender({previousRoot,renderContext,deadlines=new Map(),member='',reframe=false,budget=createPreparationBudget()}={}){
  const context={phase:'prepare',resourceStates:new Map(),nativeIslands:new Map(),disclosures:new Map(),now:Date.now(),previousRoot,previousRoots:new Map(),registeredRoots:new WeakSet(),renderContext,deadlines,member,budget,connectionContext:{budget,alive:true},localDeadlines:new Map(),reframe,frames:new Map(),islands:new Map(),slots:new Map(),incompleteSources:new Map(),assets:new Map(),used:new Map(),alive:true};
  registerPreparedRoot(context,previousRoot);return context;
}
export function preparedPreviousRoot(context,member){return context.previousRoots.get(member)||context.previousRoot;}
function disclosureKey(detail,member,counts){
  const base=JSON.stringify([detail.closest('article[data-message-id]')?.dataset.messageId||member,detail.dataset.cacheKey||detail.dataset.approvalId||detail.className]);
  const index=counts.get(base)||0;counts.set(base,index+1);return base+':'+index;
}
export function registerPreparedRoot(context,root){
  if(!root||context.registeredRoots.has(root))return;context.registeredRoots.add(root);
  for(const row of [root,...root.querySelectorAll('article[data-message-id]')])if(row.dataset.messageId)context.previousRoots.set(row.dataset.messageId,row);
  const counts=new Map();for(const detail of [root,...root.querySelectorAll('details')].filter(node=>node.localName==='details')){const key=disclosureKey(detail,context.member,counts);if(detail.preparedOpenIntent!==undefined)context.disclosures.set(key,detail.preparedOpenIntent);}
  for(const element of root.querySelectorAll(components)){
    const member=element.closest('article[data-message-id]')?.dataset.messageId||'';
    const key=member+':'+componentKey(element);
    if(!context.islands.has(key))context.islands.set(key,[]);if(!context.islands.get(key).includes(element))context.islands.get(key).push(element);
    context.slots.set(member+':'+deadlineKey(element),element);
  }
}

// Surrogates never acquire resources or run custom-element connection work.
// The old island remains live, including its selection/media/viewer state.
export function staticPreparationClone(source){
  const clone=source.cloneNode(true),originals=[source,...source.querySelectorAll('*')],copies=[clone,...clone.querySelectorAll('*')],pairs=originals.map((old,index)=>[old,copies[index]]);
  for(let index=0;index<copies.length;index++){
    const next=copies[index],old=originals[index];
    next.removeAttribute('id');next.removeAttribute('autofocus');
    if(next.localName.includes('-')){
      const environment=currentPreparedRender();
      if(environment?.context.phase==='prepare'&&environment.context.reframe&&old.dataset.preparedFallback!=='true'){next.classList.remove('prepared-frame','initial-layout-slot');environment.context.frames.set(old,next);}
      next.preparationSurrogate=true;next.preparationOriginal=old.preparationOriginal||old;
      for(const key of ['identity','sourceText','closed','file','label','presentation','line','noticeKey','renderSlot'])next[key]=old[key];
    }
    if(['img','iframe','audio','video','source'].includes(next.localName)){
      const box=old.getBoundingClientRect();next.removeAttribute('src');next.removeAttribute('srcset');next.removeAttribute('srcdoc');next.removeAttribute('autoplay');
      const reframing=currentPreparedRender()?.context.phase==='prepare'&&currentPreparedRender().context.reframe;
      // Ready media must reflow from intrinsic dimensions and current CSS. An
      // old rendered box is valid only while its layout generation is valid.
      // Keep documents/media inert: no second iframe, playback or resource lease.
      const intrinsicWidth=old.naturalWidth||old.videoWidth,intrinsicHeight=old.naturalHeight||old.videoHeight;
      if(reframing&&old.dataset.preparedFallback!=='true'){
        if(intrinsicWidth&&intrinsicHeight&&!old.hasAttribute('width')&&!old.hasAttribute('height')){next.setAttribute('width',String(intrinsicWidth));next.setAttribute('height',String(intrinsicHeight));}
      }else{next.style.width=box.width+'px';next.style.height=box.height+'px';}
      next.preparationMedia=true;
    }
  }
  for(const [old,next]of pairs){old.projectPreparedState?.(next);const environment=currentPreparedRender();if(environment?.context.phase==='prepare'&&old.projectPreparedState)environment.context.resourceStates.set(old,next);if(environment?.context.phase==='prepare'&&environment.context.reframe&&old.localName==='pot-resource'&&old.dataset.kind==='html'&&old.dataset.state==='ready'&&old.dataset.preparedFallback!=='true'){const iframe=old.querySelector('iframe'),projection=next.querySelector('iframe');if(iframe&&projection)environment.context.nativeIslands.set(iframe,{context:environment.context,original:iframe,projection,owner:old,stable:0});}}
  return clone;
}

export function preparedIsland(element){
  if(!rendering||rendering.context.phase!=='prepare')return element;
  const {context,member}=rendering,key=member+':'+componentKey(element),index=context.used.get(key)||0;
  context.used.set(key,index+1);
  const old=context.islands.get(key)?.[index];
  if(old){const copy=staticPreparationClone(old);if(element.localName==='pot-message-file')copy.preparationUpdates={reference:element.reference,message:element.message,options:element.options};return copy;}
  const previous=context.slots.get(member+':'+deadlineKey(element));
  if(!context.reframe&&previous?.dataset.preparedFallback==='true'){
    framePreparedElement(element,previous.getBoundingClientRect().height);element.dataset.preparedFallback='true';
  }
  return element;
}

export function consumePreparationSurrogates(root){
  for(const element of [...root.childNodes]){
    if(element.preparationOriginal){element.preparationOriginal.commitPreparedState?.(element);moveConversationNode(element.parentNode,element.preparationOriginal,element);element.remove();}
    else if(element.nodeType===1)consumePreparationSurrogates(element);
  }
  return root;
}

// The real iframe keeps its DOM parent and browsing context. Its temporary
// transparent top-layer viewport supplies current document layout even while
// the conversation surface is hidden or clipped by a containing block.
function stageNativeIslands(context){
  for(const iframe of context.root.querySelectorAll('pot-resource[data-kind="html"] iframe'))if(!iframe.preparationMedia&&!iframe.nativePreparation&&!context.nativeIslands.has(iframe))context.nativeIslands.set(iframe,{context,original:iframe,owner:iframe.closest('pot-resource'),stable:0});
  for(const entry of context.nativeIslands.values()){
    const {original,owner}=entry;if(entry.home)continue;
    if(!original.isConnected)throw Error('Prepared native document lost its original owner');
    const box=(entry.projection||original).getBoundingClientRect(),oldBox=original.getBoundingClientRect();
    // Outer result/notice revisions still measure their new DOM in the hidden
    // candidate. An unchanged embedded viewport already has its real layout:
    // staging it would unnecessarily retire the browser's current input surface.
    if(entry.projection&&box.width===oldBox.width&&box.height===oldBox.height&&entry.projection.clientWidth===original.clientWidth&&entry.projection.clientHeight===original.clientHeight)continue;
    original.nativePresentation?.cancel();
    owner.nativeReflowDeadline??=performance.now()+5000;
    entry.style=original.getAttribute('style');entry.framed=original.classList.contains('prepared-frame');entry.popover=original.getAttribute('popover');entry.inert=original.inert;entry.hidden=original.getAttribute('aria-hidden');entry.tabindex=original.getAttribute('tabindex');entry.focus=document.activeElement===original;
    entry.home=original.ownerDocument.createElement('div');entry.home.className=original.className;entry.home.style.cssText='box-sizing:border-box;width:'+oldBox.width+'px;height:'+oldBox.height+'px;max-width:100%';entry.home.inert=true;entry.home.setAttribute('aria-hidden','true');entry.home.dataset.conversationHold='';entry.home.dataset.nativePreparationPlaceholder='';
    // Prepare the temporary status in the same connected hidden surface. The
    // old measured frame is a hold, never ready content or a reading anchor.
    const status=original.ownerDocument.createElement('p');status.className='resource-status';status.textContent=translate(original.ownerDocument.documentElement.lang,'resource.loading');entry.home.append(status);context.root.append(entry.home);entry.home.getBoundingClientRect();moveConversationNode(original.parentNode,entry.home,original);
    if(entry.projection){const projection=original.ownerDocument.createElement('div');projection.className=entry.projection.className;projection.style.cssText=entry.projection.style.cssText;entry.projection.replaceWith(projection);entry.projection=projection;}else entry.projection=entry.home;
    entry.projection.dataset.nativePreparation='';entry.projection.nativePreparation=entry;original.nativePreparation=entry;
    original.style.cssText+=';position:fixed;inset:auto;left:0;top:0;margin:0;padding:0;border:0;max-height:none;opacity:0;visibility:visible;pointer-events:none;box-sizing:border-box;width:'+box.width+'px;min-width:0;max-width:none';if(!entry.focus)original.inert=true;original.setAttribute('aria-hidden','true');
    if(original.showPopover){original.setAttribute('popover','manual');original.showPopover();}
    const height=owner.resourceRenderState?.resize;if(height!==undefined)original.style.setProperty('--resource-height',height+'px');
  }
}
function nativeIslandsPending(context,deadlineFor,now){
  let pending=false;
  for(const entry of context.nativeIslands.values()){
    if(!entry.home)continue;
    const {original,projection,owner}=entry;
    const box=original.getBoundingClientRect(),next=JSON.stringify([box.width,box.height,owner.nativeResizeRevision||0]);
    if(next!==entry.previous)for(let element=projection.parentElement;element&&context.root.contains(element);element=element.parentElement)if(element.matches(components)&&element.dataset.preparedFallback!=='true')element.classList.remove('prepared-frame');
    projection.style.width=box.width+'px';projection.style.height=box.height+'px';projection.style.boxSizing='border-box';
    owner.projectPreparedState?.(projection.closest('pot-resource'));
    entry.stable=next===entry.previous?entry.stable+1:0;entry.previous=next;
    if(entry.stable<3){
      const deadline=owner.dataset.state==='ready'?owner.nativeReflowDeadline:deadlineFor(componentDeadlineKey(owner,context.member));
      if(now>=deadline){
        // Bound the actual child viewport too. Late document-height reports
        // then update its scrollable interior, not its native input surface.
        entry.fixedHeight=box.height;framePreparedElement(original,entry.fixedHeight);
        frame(projection.closest('pot-resource')||owner,true);
      }
      else pending=true;
    }
  }
  return pending;
}
function presentNativeIsland(entry){
  const {original,owner,home,context}=entry,view=original.ownerDocument.defaultView,parent=original.parentElement;
  const source=original.getAttribute('src'),generation=owner.generation,window=original.contentWindow;
  const position=parent.style.getPropertyValue('position'),priority=parent.style.getPropertyPriority('position'),positioned=getComputedStyle(parent).position==='static';
  if(positioned)parent.style.position='relative';
  // The iframe has its final flow, styles and input eligibility while the
  // existing loading hold covers it. Removing this sibling later does not
  // perform another native viewport/input-surface transition.
  home.style.cssText='position:absolute;inset:0;width:auto;height:auto;max-width:none;max-height:none;margin:0;z-index:1;background:var(--surface);pointer-events:auto';home.inert=false;parent.append(home);
  original.setAttribute('aria-hidden','true');if(original.ownerDocument.activeElement!==original)original.setAttribute('tabindex','-1');
  let animation=0,previous,stable=0,closed=false;
  const presentation={cancel:finish};original.nativePresentation=presentation;
  function finish(){
    if(closed)return;closed=true;view.cancelAnimationFrame(animation);home.remove();
    if(positioned&&parent.style.position==='relative'){if(position)parent.style.setProperty('position',position,priority);else parent.style.removeProperty('position');}
    if(original.nativePresentation===presentation){
      delete original.nativePresentation;
      if(entry.hidden===null)original.removeAttribute('aria-hidden');else original.setAttribute('aria-hidden',entry.hidden);
      if(entry.tabindex===null)original.removeAttribute('tabindex');else original.setAttribute('tabindex',entry.tabindex);
    }
    context.nativePresentationChanged(presentation,false);
  }
  function check(){
    if(closed)return;
    if(original.nativePresentation!==presentation||!original.isConnected||owner.generation!==generation||original.getAttribute('src')!==source||original.contentWindow!==window)return finish();
    if(original.closest('[data-preparation-surface]')){previous=undefined;stable=0;animation=view.requestAnimationFrame(check);return;}
    const box=original.getBoundingClientRect(),next=JSON.stringify([box.x,box.y,box.width,box.height,original.clientWidth,original.clientHeight,entry.fixedHeight===undefined?owner.nativeResizeRevision||0:0]);
    stable=next===previous?stable+1:0;previous=next;
    // RAF runs before rendering. Two matching subsequent callbacks leave a
    // full rendering opportunity after the final flow/child viewport update;
    // a layout or unbounded native resize restarts this presentation boundary.
    // Reports inside a fixed viewport still require matching real box/client sizes.
    if(stable>=2)return finish();
    animation=view.requestAnimationFrame(check);
  }
  context.nativePresentationChanged(presentation,true);if(!closed)animation=view.requestAnimationFrame(check);
}
export function finishPreparedNative(context,{commit=false,preparedOnly=false}={}){
  for(const entry of [...context.nativeIslands.values()]){
    const {original,home,projection}=entry;
    if(preparedOnly&&!context.root.contains(original))continue;
    // A commit only ends this transaction. The original component keeps its
    // budget through later content/resize revisions until retireRendering.
    if(!home)continue;
    const height=original.style.getPropertyValue('--resource-height');
    if(original.matches(':popover-open'))original.hidePopover();
    if(entry.popover===null)original.removeAttribute('popover');else original.setAttribute('popover',entry.popover);
    if(entry.style===null)original.removeAttribute('style');else original.setAttribute('style',entry.style);
    if(entry.fixedHeight!==undefined){if(commit)framePreparedElement(original,entry.fixedHeight);else if(!entry.framed)original.classList.remove('prepared-frame');}
    if(commit&&height)original.style.setProperty('--resource-height',height);
    if(commit&&projection.closest('pot-resource')?.dataset.preparedFallback==='true')entry.owner.dataset.preparedFallback='true';
    original.inert=entry.inert;if(entry.hidden===null)original.removeAttribute('aria-hidden');else original.setAttribute('aria-hidden',entry.hidden);
    delete original.nativePreparation;delete projection.nativePreparation;home.remove();context.nativeIslands.delete(original);
    if(context.nativePresentationChanged&&original.isConnected)presentNativeIsland(entry);
    // The browsing context never moved. Preserve its current native focus;
    // the focus captured before an async handoff cannot override a later Tab
    // or click destination, including during cancellation and owner retirement.
  }
}
export function cancelPreparedNative(root){
  for(const original of [root,...root.querySelectorAll('iframe')])original.nativePresentation?.cancel();
  const contexts=new Set([root,...root.querySelectorAll(components+',iframe,[data-native-preparation]')].map(element=>element.nativePreparation?.context).filter(Boolean));
  for(const context of contexts)finishPreparedNative(context);
}
export function commitPreparedResources(context){
  for(const [original,projection]of context.resourceStates)if(projection.preparedResourceState?.revision!==(original.resourceRenderState?.revision||0))throw Error('Prepared resource state does not match its committed revision');
  for(const [original,projection]of context.resourceStates)original.commitPreparedState(projection);
}
export function preparedResourcesCurrent(context){
  return [...context.resourceRevisions||[]].every(([element,revision])=>(element.resourceRenderState?.revision||0)===revision);
}

// Each caller supplies a stable slot within its logical record. Parsing calls
// cannot be matched merely by source text: identical text can occur at different
// nesting widths, and retained views can skip different calls on the next pass.
export function preparedAsset(slot,source,create){
  if(!rendering)return create();
  const {context,member}=rendering,key=member+':'+slot;
  if(context.phase==='prepare'){
    const value=create();context.assets.set(key,{source,value,nodes:value.nodeType===11?[...value.childNodes]:null});return value;
  }
  if(context.phase==='commit'){
    const entry=context.assets.get(key);
    if(!entry||entry.source!==source)throw Error('Prepared render slot does not match its committed revision: '+key);
    context.assets.delete(key);if(!entry.nodes)return entry.value;
    const fragment=entry.value.ownerDocument.createDocumentFragment();
    for(const node of entry.nodes)moveConversationNode(fragment,node);
    return fragment;
  }
  return create();
}

// A mutable view keeps its stable container and state, while every entering
// child is taken from its connected prepared projection. Callers may retain
// existing native islands by replacing their surrogates before the transfer.
export function adoptPreparedChildren(target,source,{retain=[]}={}){
  if(target===source)return;
  const attributes=[...source.attributes].map(attribute=>[attribute.name,attribute.value]);
  const oldText=target.childNodes.length===1&&target.firstChild.nodeType===3?target.firstChild:null,newText=source.childNodes.length===1&&source.firstChild.nodeType===3?source.firstChild:null;
  const selection=getSelection(),endpoints=oldText&&newText&&selection?.rangeCount?[selection.anchorNode,selection.anchorOffset,selection.focusNode,selection.focusOffset]:null;
  for(const [candidate,live]of retain)if(candidate&&live&&candidate!==live&&candidate.parentNode){moveConversationNode(candidate.parentNode,live,candidate);candidate.remove();}
  consumePreparationSurrogates(source);
  const nodes=[...source.childNodes];
  for(const [index,node]of nodes.entries())moveConversationNode(target,node,target.childNodes[index]||null);
  if(endpoints&&[0,2].every(index=>endpoints[index]!==oldText||oldText.data.slice(0,endpoints[index+1])===newText.data.slice(0,endpoints[index+1])))selection.setBaseAndExtent(...endpoints.map((value,index)=>index%2===0&&value===oldText?newText:value));
  for(const node of [...target.childNodes])if(!nodes.includes(node))node.remove();
  source.removeAttribute('id');for(const attribute of [...target.attributes])if(!attributes.some(([name])=>name===attribute.name))target.removeAttribute(attribute.name);
  for(const [name,value]of attributes)target.setAttribute(name,value);
}
export function preparedDOM(target,slot,source,render,options){
  const environment=currentPreparedRender();
  if(environment?.context.phase==='commit'){
    const projection=preparedAsset('dom:'+slot,source,()=>{throw Error('Missing prepared DOM slot: '+slot);});
    adoptPreparedChildren(target,projection,typeof options==='function'?options(projection):options);return;
  }
  render(target);
  if(environment?.context.phase==='prepare')preparedAsset('dom:'+slot,source,()=>target);
}
export function preparedText(target,slot,value){
  const text=String(value??''),environment=currentPreparedRender();
  if(environment?.context.phase==='commit'){
    if(target.textContent===text)return;
    const candidate=preparedAsset('text:'+slot,text,()=>{throw Error('Missing prepared text: '+slot);});
    for(const node of candidate.nodes)moveConversationNode(target,node);
    for(const node of [...target.childNodes])if(!candidate.nodes.includes(node))node.remove();
    return;
  }
  if(target.textContent!==text)target.textContent=text;
  if(environment?.context.phase==='prepare')preparedAsset('text:'+slot,text,()=>({nodes:[...target.childNodes]}));
}

export function retirePreparedTree(root){
  // Retire while still connected too: connection alone does not prove that a
  // candidate belongs to the current owner/conversation/layout generation.
  cancelPreparedNative(root);
  for(const element of [root,...root.querySelectorAll(components)])element.retireRendering?.();
  root.remove();
}

export function framePreparedElement(element,height,width){
  element.style.setProperty('--prepared-height',height+'px');if(width!==undefined)element.style.setProperty('--prepared-width',width+'px');element.classList.add('prepared-frame');
}
function frame(element,fallback){
  // Reframed surrogates also need the committed component's overflow/box rules.
  // Measuring their unframed margin collapse and framing only the original at
  // commit changes line boxes (notably several adjacent saved-file blocks).
  const box=element.getBoundingClientRect();if(!box.height)return;
  const inline=element.matches('pot-message-file[data-block="false"]');
  const unresolvedFallback=fallback||element.preparationSurrogate&&element.dataset.preparedFallback==='true'&&element.dataset.state==='loading';
  const height=unresolvedFallback&&!inline?Math.max(240,box.height):box.height;
  element.style.setProperty('--prepared-height',height+'px');
  if(inline)element.style.setProperty('--prepared-width',box.width+'px');
  element.classList.add('prepared-frame');
  if(fallback)element.dataset.preparedFallback='true';
}

export function settlePreparedTree(root,context,{valid=()=>true,layout=()=>{},timeout=5000}={}){
  context.root ||= root;
  const view=root.ownerDocument.defaultView;let animation,timer,observer,cancelRun,closed=false;
  const finish=()=>{closed=true;view.cancelAnimationFrame(animation);view.clearTimeout(timer);observer?.disconnect();};
  const promise=new Promise((resolve,reject)=>{
    let previous,fontWaiting=false;const decoded=new WeakMap();
    const deadlineFor=key=>{
      let deadline=context.localDeadlines.get(key)??context.deadlines.get(key);
      if(deadline===undefined)deadline=performance.now()+timeout;
      context.localDeadlines.set(key,deadline);context.deadlines.delete(key);context.deadlines.set(key,deadline);
      while(context.deadlines.size>2048)context.deadlines.delete(context.deadlines.keys().next().value);
      return deadline;
    };
    const fail=reason=>{if(closed)return;finish();try{finishPreparedNative(context);}finally{reject(reason);}};
    const cancel=()=>fail(new DOMException('Prepared render retired','AbortError'));cancelRun=cancel;
    function check(){
      try{
      animation=0;if(closed)return;
      if(!context.alive||!valid())return cancel();
      if(!root.isConnected)return schedule();
      stageNativeIslands(context);layout();const counts=new Map();for(const detail of [root,...root.querySelectorAll('details')].filter(node=>node.localName==='details')){const key=disclosureKey(detail,context.member,counts);if(context.disclosures.has(key)){detail.preparedOpenIntent=context.disclosures.get(key);detail.open=detail.preparedOpenIntent;}}observer.takeRecords();
      if(nativeIslandsPending(context,deadlineFor,performance.now())){previous=undefined;schedule();return;}
      if(root.querySelector('[data-prepared-pending]')){previous=undefined;return schedule();}
      const now=performance.now(),all=[...root.querySelectorAll(components)],pending=[];
      for(const element of all){
        if(element.preparationSurrogate||element.closest('[hidden]')||!element.getBoundingClientRect().height||element.dataset.state!=='loading')continue;
        const key=componentDeadlineKey(element,context.member||root.dataset.messageId||'');
        const deadline=deadlineFor(key);
        if(now>=deadline)frame(element,true);else pending.push({element,deadline});
      }
      for(const image of root.querySelectorAll('img')){
        if(image.preparationMedia||image.closest('[hidden]')||decoded.get(image)==='ready'||image.complete&&!image.naturalWidth)continue;
        if(!decoded.has(image)){decoded.set(image,'loading');image.decode().catch(()=>{}).then(()=>{decoded.set(image,'ready');schedule();});}
        const key=imageDeadlineKey(image,context.member||root.dataset.messageId||''),deadline=deadlineFor(key);
        if(now>=deadline)frame(image,true);else pending.push({element:image,deadline});
      }
      if(root.ownerDocument.fonts?.status==='loading'&&now<deadlineFor('fonts')){
        if(!fontWaiting){fontWaiting=true;root.ownerDocument.fonts.ready.then(schedule);}
        pending.push({element:root,deadline:deadlineFor('fonts')});
      }
      const unresolved=pending.filter(({element})=>!element.classList.contains('prepared-frame'));
      if(unresolved.length){
        previous=undefined;view.clearTimeout(timer);timer=view.setTimeout(check,Math.max(0,Math.min(...unresolved.map(item=>item.deadline))-performance.now()));return;
      }
      // Freeze inner frames before measuring their parent. An already-ready
      // Markdown resource can still contain a later iframe/diagram update.
      for(const element of all.reverse())frame(element,false);
      const geometry=JSON.stringify([root.getBoundingClientRect().width,root.getBoundingClientRect().height,
        ...all.map(element=>{const box=element.getBoundingClientRect();return [box.width,box.height];})]);
      if(geometry!==previous){previous=geometry;return schedule();}
      const box=root.getBoundingClientRect(),style=getComputedStyle(root);
      const marginTop=style.display==='none'?0:parseFloat(style.marginTop||0),marginBottom=style.display==='none'?0:parseFloat(style.marginBottom||0);
      context.resourceRevisions=new Map([...root.querySelectorAll('pot-resource')].map(element=>{const original=element.preparationOriginal||element;return [original,original.resourceRenderState?.revision||0];}));
      finishPreparedNative(context,{commit:true,preparedOnly:true});
      context.ready=true;finish();resolve({height:box.height,size:box.height+marginTop+marginBottom,marginTop,marginBottom});
      }catch(reason){fail(reason);}
    }
    function schedule(){if(!closed&&!animation)animation=view.requestAnimationFrame(check);}
    observer=new MutationObserver(schedule);observer.observe(root,{subtree:true,childList:true,attributes:true,characterData:true});check();
  });
  return {promise,cancel(){context.alive=false;context.connectionContext.alive=false;cancelRun();}};
}

const layoutProperties=['width','min-width','max-width','height','min-height','max-height','font','line-height','letter-spacing','box-sizing','overflow'];
export function rememberPreparedLayout(element){
  const style=getComputedStyle(element);
  element.preparedLayoutSnapshot={original:layoutProperties.map(key=>[key,element.style.getPropertyValue(key),element.style.getPropertyPriority(key)]),values:layoutProperties.map(key=>[key,style.getPropertyValue(key)])};
}
export function freezePreparedLayout(element){
  const saved=element.preparedLayoutSnapshot;if(!saved)return;
  for(const [key,value]of saved.values)element.style.setProperty(key,value);
  element.style.maxWidth='none';element.style.minWidth='0';
  const height=saved.values.find(([key])=>key==='height')?.[1];
  if(height&&height!=='auto'){element.style.minHeight=height;element.style.maxHeight=height;}
  element.style.overflow='clip';
}
export function releasePreparedLayout(element){
  for(const [key,value,priority]of element.preparedLayoutSnapshot?.original||[])if(value)element.style.setProperty(key,value,priority);else element.style.removeProperty(key);
}
export function createPreparationSurface(scroller,host){
  const document=host.ownerDocument,root=scroller.cloneNode(false),style=getComputedStyle(scroller);root.removeAttribute('id');
  root.className=scroller.className;root.classList.add('prepared-conversation');root.dataset.preparationSurface='';root.inert=true;root.setAttribute('aria-hidden','true');
  root.style.cssText+=';position:fixed;left:-100000px;top:0;visibility:hidden;pointer-events:none;box-sizing:border-box;height:auto;min-height:0;max-height:none;overflow:visible;flex:none;';
  root.style.width=scroller.clientWidth+'px';root.style.paddingLeft=style.paddingLeft;root.style.paddingRight=style.paddingRight;
  const path=[];for(let parent=host;parent&&parent!==scroller;parent=parent.parentElement)path.unshift(parent);
  let target=root;
  for(const parent of path){const copy=parent.cloneNode(false);copy.removeAttribute('id');for(const [key,value,priority]of parent.preparedLayoutSnapshot?.original||[])if(value)copy.style.setProperty(key,value,priority);else copy.style.removeProperty(key);if(parent!==host){copy.style.minHeight='';copy.style.height='';}target.append(copy);target=copy;}
  (scroller.parentElement||document.body).append(root);
  return {root,host:target,close:()=>retirePreparedTree(root)};
}
