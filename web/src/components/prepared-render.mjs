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
  const context={phase:'prepare',disclosures:new Map(),now:Date.now(),previousRoot,previousRoots:new Map(),registeredRoots:new WeakSet(),renderContext,deadlines,member,budget,connectionContext:{budget,alive:true},localDeadlines:new Map(),reframe,frames:new Map(),islands:new Map(),slots:new Map(),incompleteSources:new Map(),assets:new Map(),used:new Map(),alive:true};
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
  const clone=source.cloneNode(true),originals=[source,...source.querySelectorAll('*')],copies=[clone,...clone.querySelectorAll('*')];
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
    if(element.preparationOriginal){moveConversationNode(element.parentNode,element.preparationOriginal,element);element.remove();}
    else if(element.nodeType===1)consumePreparationSurrogates(element);
  }
  return root;
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
  const view=root.ownerDocument.defaultView;let animation,timer,observer,cancelRun,closed=false;
  const finish=()=>{closed=true;view.cancelAnimationFrame(animation);view.clearTimeout(timer);observer.disconnect();};
  const promise=new Promise((resolve,reject)=>{
    let previous,fontWaiting=false;const decoded=new WeakMap();
    const deadlineFor=key=>{
      let deadline=context.localDeadlines.get(key)??context.deadlines.get(key);
      if(deadline===undefined)deadline=performance.now()+timeout;
      context.localDeadlines.set(key,deadline);context.deadlines.delete(key);context.deadlines.set(key,deadline);
      while(context.deadlines.size>2048)context.deadlines.delete(context.deadlines.keys().next().value);
      return deadline;
    };
    const cancel=()=>{if(closed)return;finish();reject(new DOMException('Prepared render retired','AbortError'));};cancelRun=cancel;
    function check(){
      animation=0;if(closed)return;
      if(!context.alive||!valid())return cancel();
      if(!root.isConnected)return schedule();
      layout();const counts=new Map();for(const detail of [root,...root.querySelectorAll('details')].filter(node=>node.localName==='details')){const key=disclosureKey(detail,context.member,counts);if(context.disclosures.has(key)){detail.preparedOpenIntent=context.disclosures.get(key);detail.open=detail.preparedOpenIntent;}}observer.takeRecords();
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
      finish();resolve({height:box.height,size:box.height+marginTop+marginBottom,marginTop,marginBottom});
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
