import {preparedResourcesCurrent,commitPreparedResources,finishPreparedNative,cancelPreparedNative,currentPreparedRender,withPreparedRender,createPreparedRender,createPreparationBudget,createPreparationSurface,settlePreparedTree,preparedLayoutRect,framePreparedElement,retirePreparedDeadlines,consumePreparationSurrogates,rememberPreparedLayout,freezePreparedLayout,releasePreparedLayout} from './prepared-render.mjs';
import {moveConversationNode} from './conversation-dom.mjs';
import {Virtualizer, observeElementRect, defaultRangeExtractor} from '../vendor/tanstack-virtual-core.mjs';
import {createConversationController} from './conversation-controller.mjs';

// The controller owns intent and compensation; this adapter commits its measured
// flow ranges. Child adapters share that controller and never write the scroller.
export function createMessageWindow({scroller, host, render, dispose, requestRender, controller:parentController, active, interact, upward, downward, scrolled, canReadEarlier, earlierBoundary, onEarlierVisible, heights = new Map(), nested = false}) {
  let rows = [], mounted = new Map(), closed = false, suspended = false, updating = false, dirty = false;
  let margin = 0, reportRect, reportOffset, owner, controller=parentController, frame=0,inputStarted=()=>{};
  const spacers = [],regionRevisions=new Map(),geometry=new Map(),rowRevisions=new Map(),revealedRows=new Set();
  let lastReadable=[],lastReadableTop=0;const held=new Map(),layoutHosts=new Set([host]);
  let requested=rows,requestedRevision='[]',committedRevision='[]',requestedOptions={},flight=null,layoutEpoch=0,layoutFingerprint='',committing=null,measuredOptions={},layoutReframe=false,queueUpdating=false,preparationError=false;
  const inheritedPreparation=currentPreparedRender();
  const deadlines=inheritedPreparation?.context.deadlines||new Map();
  const preparationBudget=inheritedPreparation?.context.budget||createPreparationBudget();
  const indexesByKey = new Map(), indexesByID = new Map();
  const sources = new WeakMap();
  function retireDeadlines(record,retained){
    const message=sources.get(record),members=[message?.id,...(message?.members||[]).map(member=>member.id)].filter(id=>id&&!retained.has(id));
    retirePreparedDeadlines(deadlines,members);
  }
  function navigationIndex() {
    if(controller?.following)return rows.length-1;
    const id=controller?.destination?.id||controller?.anchor?.id;
    let index=indexesByID.get(id)??indexesByKey.get(id);
    if(index===undefined)for(const parent of controller?.visibilityPath.keys()||[])if(indexesByID.has(parent)){index=indexesByID.get(parent);break;}
    return index??-1;
  }
  function invalidate(){if(!closed&&!suspended&&!frame)frame=scroller.ownerDocument.defaultView.requestAnimationFrame(()=>{frame=0;update();});}
  function pinnedIndexes(range) {
    const indexes = new Set(defaultRangeExtractor(range)), selection = getSelection();
    // A navigation target must exist before the first paint, even while the
    // native scroll event still reports the previous conversation's offset.
    // A reset, history trim or reorder can move/remove the target. Resolve its
    // identity in the current list before passing an index to TanStack.
    const index=navigationIndex();
    if(index>=0&&index<range.count)for(const i of defaultRangeExtractor({...range,startIndex:index,endIndex:index}))indexes.add(i);
    for (const record of mounted.values()) {
      if (record.row.contains(document.activeElement) || selection?.rangeCount && !selection.isCollapsed && (record.row.contains(selection.anchorNode) || record.row.contains(selection.focusNode))) {const index=indexesByKey.get(record.row.dataset.windowKey);if(index!==undefined)indexes.add(index);}
    }
    return [...indexes].filter(index=>index>=0&&index<rows.length).sort((a,b)=>a-b);
  }
  const virtualizer = new Virtualizer({
    count: 0, getScrollElement:()=>scroller,
    getItemKey:index=>rows[index].viewKey,
    estimateSize:index=>knownSize(rows[index].viewKey),
    observeElementRect(instance,callback){reportRect=callback;return observeElementRect(instance,callback);},
    observeElementOffset(instance,callback){
      reportOffset=callback;
      const element=instance.scrollElement,view=element.ownerDocument.defaultView,supportsScrollend='onscrollend' in view;
      let idleTimer=0,endFrame=0,inputGeneration=0;
      const deliver=scrolling=>{
        // Capture reader movement before TanStack commits a different DOM range.
        if(!nested){owner?.nativeScroll();holdReadable();}
        callback(element.scrollTop,scrolling);
        if(!nested&&!scrolling)owner?.idle();
      };
      const armIdle=()=>{const generation=++inputGeneration;view.clearTimeout(idleTimer);view.cancelAnimationFrame(endFrame);endFrame=0;if(!supportsScrollend)idleTimer=view.setTimeout(()=>{if(generation===inputGeneration)deliver(false);},instance.options.isScrollingResetDelay);};
      inputStarted=armIdle;
      const scroll=()=>{armIdle();deliver(true);},end=()=>{
        // A geometry write can enqueue scrollend while a native key animation
        // still has a compositor frame to deliver. Validate that end against
        // the next rendering step; newer scroll/input generations retire it.
        const generation=inputGeneration,offset=element.scrollTop;view.clearTimeout(idleTimer);view.cancelAnimationFrame(endFrame);
        endFrame=view.requestAnimationFrame(()=>{endFrame=0;if(generation!==inputGeneration)return;if(Math.abs(element.scrollTop-offset)>.5){armIdle();deliver(true);}else deliver(false);});
      };
      element.addEventListener('scroll',scroll,{passive:true});if(supportsScrollend)element.addEventListener('scrollend',end,{passive:true});
      return ()=>{view.clearTimeout(idleTimer);view.cancelAnimationFrame(endFrame);inputStarted=()=>{};element.removeEventListener('scroll',scroll);element.removeEventListener('scrollend',end);};
    },
    indexAttribute:'data-virtual-index',
    useAnimationFrameWithResizeObserver:true,
    rangeExtractor:pinnedIndexes, overscan:12,
    anchorTo:'start', followOnAppend:false, scrollEndThreshold:1,
    initialOffset:()=>scroller.scrollTop,
    scrollToFn(offset,{adjustments=0}) {
      // The only writer for a conversation's native scroll position. Every
      // command and resize correction is arbitrated by TanStack first.
      if(nested||closed||suspended)return;
      const top=Math.max(0,Math.min(offset+adjustments,scroller.scrollHeight-scroller.clientHeight));
      if(Math.abs(top-scroller.scrollTop)>1e-7){scroller.scrollTop=top;owner?.wrote(scroller.scrollTop,top);}
    },
    onChange(_instance, sync) {
      if (closed || suspended) return;
      dirty = true;
      if (!updating&&!queueUpdating) {
        // Native scrolling and resize corrections must commit their ranges in
        // the same frame as the offset change, retaining existing nodes.
        if (sync && rows.length && host.isConnected && host.getClientRects().length) update();
        else invalidate();
      }
    },
  });
  if(nested)virtualizer.shouldAdjustScrollPositionOnItemSizeChange=()=>false;
  const unmount = virtualizer._didMount();
  if(!nested){owner=createConversationController({scroller,virtualizer,requestLayout:invalidate,intentChanged:()=>requestRender?.(),syncOffset:offset=>reportOffset?.(offset,true),inputStarted:()=>inputStarted(),active,interact,upward,downward,scrolled,canReadEarlier,earlierBoundary,onEarlierVisible});controller=owner.api;}
  const geometryObserver=nested?null:new ResizeObserver(invalidate);
  geometryObserver?.observe(scroller);if(host.parentElement)geometryObserver?.observe(host.parentElement);

  function rememberReadable(){
    if(held.size)return;
    const viewport=scroller.getBoundingClientRect();
    lastReadable=[...mounted.values()].filter(record=>{const box=record.row.getBoundingClientRect();return Math.min(box.bottom,viewport.bottom)-Math.max(box.top,viewport.top)>=24;});
    lastReadableTop=scroller.scrollTop;
  }
  function holdReadable(){
    if(nested||!lastReadable.length)return;
    const viewport=scroller.getBoundingClientRect();
    if(!held.size&&[...mounted.values()].some(record=>{const box=record.row.getBoundingClientRect();return Math.min(box.bottom,viewport.bottom)-Math.max(box.top,viewport.top)>=24;}))return;
    // A jump may leave the previously materialized range before its destination
    // is ready. Temporarily retain those actual nodes at their last readable
    // positions. Their native identities do not change and reading APIs exclude
    // this explicitly marked hold; it never supplies a canvas offset or anchor.
    for(const record of lastReadable){
      if(!record.row.isConnected||!host.contains(record.row))continue;
      if(!held.has(record.row)){held.set(record.row,record.row.style.transform);record.row.dataset.conversationHold='';}
      record.row.style.transform=`translateY(${scroller.scrollTop-lastReadableTop}px)`;
    }
  }
  function releaseHold(){for(const [row,transform]of held){row.style.transform=transform;delete row.dataset.conversationHold;}held.clear();}
  function draw() {
    // Moving flow rows/spacers can briefly shrink the list. Reserve its extent
    // until this DOM commit is complete so native scroll clamping cannot turn
    // that intermediate layout into a new reading position (also in children).
    const minHeight=host.style.minHeight;
    host.style.minHeight=`${Math.max(host.getBoundingClientRect().height,virtualizer.getTotalSize())}px`;
    try{drawRows();}finally{host.style.minHeight=minHeight;}
  }
  function drawRows() {
    const items = virtualizer.getVirtualItems(), selected = new Set(items.map(item=>item.key));
    const retainedMembers=new Set(items.flatMap(item=>[rows[item.index].id,...(rows[item.index].members||[]).map(member=>member.id)]));
    const layout = []; let offset=0, spacerIndex=0;
    for(const item of items){
      const start=item.start-margin;
      if(start>offset+.5||!layout.length&&start<-.5)layout.push({gap:start-offset});
      const message=rows[item.index], old=mounted.get(item.key);
      let record=old;
      const preparing=currentPreparedRender();
      if(nested&&preparing)record=withPreparedRender(preparing.context,message.id,()=>render(message,old));
      else if(committing?.has(item.key)){
        const job=committing.get(item.key);job.context.phase='commit';
        for(const [element,clone]of job.context.frames){const box=clone.getBoundingClientRect();framePreparedElement(element,box.height,element.matches('pot-message-file[data-block="false"]')?box.width:undefined);}
        commitPreparedResources(job.context);
        record=withPreparedRender(job.context,message.id,()=>old?render(message,old):job.record.commit?.(message)||job.record);
        finishPreparedNative(job.context,{commit:true});record.preparedSignature=job.signature;job.context.phase='live';
        record.row.preparedContext=job.context.connectionContext;
        record.row.classList.add('prepared-row');
        record.row.style.setProperty('--prepared-margin-top',job.measurement.marginTop+'px');record.row.style.setProperty('--prepared-margin-bottom',job.measurement.marginBottom+'px');
        if(old)disposePreparation(job);committing.delete(item.key);
        record.row.dataset.messageId=message.id;
        const previous=sources.get(old),removed=[previous?.id,...(previous?.members||[]).map(member=>member.id)].filter(id=>id&&!retainedMembers.has(id));
        retirePreparedDeadlines(deadlines,[message.id,...(message.members||[]).map(member=>member.id),...removed],record.row,message.id);
      }
      if(!record){layout.push({gap:item.size});offset=item.end-margin;continue;}
      sources.set(record,message);
      record.row.dataset.windowKey=item.key;record.row.dataset.virtualIndex=String(item.index);
      // Stable reading identity belongs to the list contract, including callers
      // whose renderer only supplies content and a row element.
      record.row.dataset.messageId=message.id;record.row.dataset.scrollAnchor=`message:${item.key}`;
      layout.push({record});
      offset=item.end-margin;
    }
    const total=virtualizer.getTotalSize();
    if(total>offset+.5)layout.push({gap:total-offset});
    for(const [key,record]of mounted)if(!selected.has(key)){
      virtualizer.observer.unobserve(record.row);virtualizer.elementsCache.delete(key);
      mounted.delete(key);retireDeadlines(record,retainedMembers);dispose(record);
    }
    let index=0;
    for(const part of layout){
      let element;
      if(part.gap){element=spacers[spacerIndex];if(!element){element=document.createElement('div');element.className='message-window-spacer';element.setAttribute('aria-hidden','true');spacers[spacerIndex]=element;}spacerIndex++;element.style.height=`${Math.max(0,part.gap)}px`;element.style.marginTop=`${Math.min(0,part.gap)}px`;}
      else{element=part.record.row;mounted.set(element.dataset.windowKey,part.record);}
      if(host.children[index]!==element)moveConversationNode(host,element,host.children[index]||null);index++;
    }
    while(host.children.length>index)host.lastElementChild.remove();
  }
  // All sizes enter through a prepared transaction. Live ResizeObserver entries
  // must never replace the geometry of an unprepared content revision.
  function measure(){return false;}
  function knownSize(key){
    const size=heights.get(key);
    if(size===undefined)throw Error('Missing prepared message geometry: '+key);
    return size;
  }
  function configure(next) {
    const viewport=scroller.getBoundingClientRect(),box=preparedLayoutRect(host);
    const nextMargin=scroller.scrollTop+box.top-viewport.top;
    const rect={width:scroller.clientWidth,height:scroller.clientHeight};
    if(virtualizer.scrollRect?.width!==rect.width||virtualizer.scrollRect?.height!==rect.height)reportRect?.(rect);
    if(rows!==next){
      indexesByKey.clear();indexesByID.clear();
      next.forEach((item,index)=>{indexesByKey.set(item.viewKey,index);indexesByID.set(item.id,index);for(const member of item.members||[])indexesByID.set(member.id,index);});
    }
    if(rows!==next||Math.abs(nextMargin-margin)>.5){
      const list=next; margin=nextMargin;
      virtualizer.setOptions({...virtualizer.options,count:list.length,getItemKey:index=>list[index].viewKey,estimateSize:index=>knownSize(list[index].viewKey),rangeExtractor:range=>pinnedIndexes(range),scrollMargin:margin});
      rows=next;
      const available=new Set(rows.map(item=>item.viewKey));
      for(const key of virtualizer.itemSizeCache.keys())if(!available.has(key))virtualizer.itemSizeCache.delete(key);
    }
  }
  const fontChanged=()=>{deadlines.delete('fonts');layoutFingerprint='';layoutReframe=true;if(!nested)invalidate();};
  const interaction=event=>{
    if(event.type==='click'&&!event.target.closest?.('summary'))return;
    if(event.type==='click'){
      const summary=event.target.closest('summary'),detail=summary?.parentElement;
      if(detail?.localName==='details'&&!detail.matches('.tool-call,.tool-raw,.work-records,.task-goal')&&!summary.closest('.prepared-frame')&&!event.target.closest('button,a,input,select,textarea')){
        event.preventDefault();detail.preparedOpenIntent=!(detail.preparedOpenIntent??detail.open);requestRender?.();
      }
    }
    let row=event.target.closest?.('article[data-window-key]');
    for(let parent=row?.parentElement.closest('article[data-window-key]');parent&&host.contains(parent);parent=parent.parentElement.closest('article[data-window-key]'))row=parent;
    if(row&&host.contains(row)){revealedRows.delete(row.dataset.windowKey);rowRevisions.set(row.dataset.windowKey,(rowRevisions.get(row.dataset.windowKey)||0)+1);}
    else{layoutEpoch++;layoutFingerprint='';}
    layoutReframe=true;queueMicrotask(invalidate);
  };
  if(!nested){scroller.addEventListener('click',interaction,true);scroller.addEventListener('pot:prepare-resize',interaction);}
  if(!nested){host.ownerDocument.fonts?.addEventListener('loading',fontChanged);host.ownerDocument.fonts?.addEventListener('loadingdone',fontChanged);}
  function disposePreparation(job){if(job.record){cancelPreparedNative(job.record.row);dispose(job.record);}}
  function releaseLayout(){for(const element of layoutHosts)releasePreparedLayout(element);}
  function discardPreparation(){
    if(!flight)return;
    const retired=flight;flight=null;
    for(const job of retired.jobs.values()){job.context.alive=false;job.context.connectionContext.alive=false;job.wait?.cancel();disposePreparation(job);job.surface.close();}
    for(const job of retired.regions.values()){job.context.alive=false;job.context.connectionContext.alive=false;job.wait?.cancel();job.surface.close();}
    delete host.dataset.preparedPending;host.removeAttribute('aria-busy');
  }
  function preparationFailed(reason,current=flight){
    if(current&&flight===current){preparationError=true;committing=null;discardPreparation();releaseHold();releaseLayout();layoutReframe=true;cancelAnimationFrame(frame);frame=0;}
  }
  function rejectPreparation(reason,current){
    if(reason.name==='AbortError')return;
    preparationFailed(reason,current);
    queueMicrotask(()=>{throw reason;});
  }
  function refreshRegions(current){
    let changed=false,valid=true;
    current.options.regions=(current.options.regions||[]).map(region=>{
      const requestedRegion=(requestedOptions.regions||[]).find(item=>item.key===region.key);
      if((!region.current||region.current())&&(!requestedRegion||requestedRegion.revision===region.revision))return region;
      const latest=region.latest?.()||requestedRegion;
      if(!latest||latest===region||latest.host!==region.host||latest.current&&!latest.current()){valid=false;requestRender?.();return region;}
      const job=current.regions.get(region.key);if(job){job.wait.cancel();job.surface.close();current.regions.delete(region.key);}
      changed=true;return latest;
    });
    return {changed,valid};
  }
  function preparationTarget(next){
    if(controller?.following)return next.length-1;
    const id=controller?.destination?.id||controller?.anchor?.id;
    const key=controller?.position?.anchors?.[0]?.key?.replace(/^message:/,'');
    return Math.max(0,next.findIndex(message=>message.id===id||message.viewKey===key||message.members?.some(member=>member.id===id)));
  }
  function selectedPreparation(current){
    const next=current.rows,target=preparationTarget(next);
    // This is the complete loaded collection, not a viewport forecast. Every
    // missing revision is measured before any of its offsets can be published.
    const missing=[];
    next.forEach((message,index)=>{if(!current.sizes.has(message.viewKey))missing.push(index);});
    if(missing.length){
      current.indexed=false;
      // Preserve the actual ready destination cohort across measurement batches.
      // Numeric entries for other rows survive disposal of their candidate DOM.
      const retained=next.flatMap((message,index)=>current.jobs.get(message.viewKey)?.measurement?[index]:[]).sort((a,b)=>Math.abs(a-target)-Math.abs(b-target)).slice(0,64);
      return [...retained,...missing.sort((a,b)=>Math.abs(a-target)-Math.abs(b-target)).slice(0,96-retained.length)];
    }
    current.indexed=true;
    const size=message=>current.sizes.get(message.viewKey);
    const start=next.slice(0,target).reduce((sum,message)=>sum+size(message),margin),total=next.reduce((sum,message)=>sum+size(message),margin);
    let offset=scroller.scrollTop;
    if(controller?.following)offset=Math.max(0,total-scroller.clientHeight);
    else if(controller?.anchor)offset=Math.max(0,start-controller.anchor.offset);
    const range=new Virtualizer({count:next.length,getScrollElement:()=>null,getItemKey:index=>next[index].viewKey,estimateSize:index=>size(next[index]),initialRect:{width:scroller.clientWidth,height:scroller.clientHeight},initialOffset:offset,overscan:12,scrollMargin:margin,scrollToFn:()=>{}});
    const indexes=new Set(range.getVirtualItems().map(item=>item.index));
    if(next.length)for(let index=Math.max(0,target-12);index<=Math.min(next.length-1,target+12);index++)indexes.add(index);
    const selection=getSelection();
    for(const [key,record]of mounted)if(record.row.contains(document.activeElement)||selection?.rangeCount&&!selection.isCollapsed&&(record.row.contains(selection.anchorNode)||record.row.contains(selection.focusNode))){const index=next.findIndex(message=>message.viewKey===key);if(index>=0)indexes.add(index);}
    return [...indexes].sort((a,b)=>a-b);
  }
  function signature(message){
    const path=controller?.visibilityPath,destination=controller?.destination;
    const reveal=!!controller?.revealing&&(path?.has(message.id)||destination?.expand&&destination.id===message.id||message.members?.some(member=>path?.has(member.id)||destination?.expand&&destination.id===member.id));
    if(reveal)revealedRows.add(message.viewKey);
    return layoutEpoch+':'+(rowRevisions.get(message.viewKey)||0)+':'+Number(revealedRows.has(message.viewKey))+':'+JSON.stringify(message);
  }
  function measuredStart(current,index){
    let offset=0;for(let i=0;i<index;i++){const size=current.sizes.get(current.rows[i].viewKey);if(size===undefined)return;offset+=size;}
    return offset;
  }
  function update(next,options){
    if(closed)return;
    if(next!==undefined)preparationError=false;
    if(preparationError)return;
    const inherited=currentPreparedRender()||inheritedPreparation?.context.phase==='prepare'&&inheritedPreparation;
    if(nested){
      if(!inherited?.context.alive||!['prepare','commit'].includes(inherited.context.phase)){requestRender();return;}
      if(inherited.context.phase==='commit'){measuredOptions.beforeCommit?.();updateMeasured(rows,measuredOptions);measuredOptions.afterCommit?.();return;}
      options={...options,renderContext:inherited.context.renderContext};
    }
    if(next!==undefined){const revision=JSON.stringify(next);if(revision!==requestedRevision){requested=next;requestedRevision=revision;}requestedOptions=options||requestedOptions;suspended=false;}
    if(queueUpdating)return;queueUpdating=true;
    try{
    if(!host.isConnected||!host.clientWidth||!scroller.clientWidth||!scroller.clientHeight){discardPreparation();return;}
    if(requestedOptions.parents)owner?.setParents(requestedOptions.parents);
    const probe=createPreparationSurface(scroller,host),availableWidth=probe.host.clientWidth;probe.close();
    const style=getComputedStyle(scroller),fingerprint=JSON.stringify([availableWidth,style.font,style.lineHeight,style.letterSpacing,host.ownerDocument.documentElement.lang,requestedOptions.layoutEpoch]);
    if(fingerprint!==layoutFingerprint){layoutReframe||=!!layoutFingerprint;layoutFingerprint=fingerprint;layoutEpoch++;discardPreparation();}
    if(flight&&flight.revision!==requestedRevision){
      const current=flight,keys=new Set(requested.map(message=>message.viewKey));
      for(const [key,job]of current.jobs)if(!keys.has(key)){job.wait.cancel();disposePreparation(job);job.surface.close();current.jobs.delete(key);}
      for(const key of current.sizes.keys())if(!keys.has(key)){current.sizes.delete(key);current.measurements.delete(key);}
      const renderContext=requestedOptions.renderContext&&Object.fromEntries(Object.entries(requestedOptions.renderContext).map(([key,value])=>[key,typeof value==='function'?value:structuredClone(value)]));
      current.rows=structuredClone(requested);current.revision=requestedRevision;current.options={...requestedOptions,regions:current.options.regions,renderContext};
    }
    // Measured overscan may prepare one more row than the measured live range
    // ultimately needs. That candidate is retired after commit. A later layout
    // check must reuse the already committed flow instead of repeatedly preparing
    // and retiring that offscreen row, which would keep initial reveal unready.
    // Intent can pin a new identity without changing messages. Resolve that
    // range before deciding whether every entering node is already prepared.
    virtualizer.setOptions({...virtualizer.options,rangeExtractor:range=>pinnedIndexes(range)});
    const liveItems=virtualizer.getVirtualItems();
    if(!flight&&requestedRevision===committedRevision&&!layoutReframe&&
      (liveItems.length||!requested.length)&&liveItems.every(item=>mounted.get(item.key)?.preparedSignature===signature(rows[item.index]))&&
      (requestedOptions.regions||[]).every(region=>regionRevisions.get(region.key)===layoutEpoch+':'+region.revision)){
      requestedOptions.beforeCommit?.();updateMeasured(rows,requestedOptions);requestedOptions.afterCommit?.();return;
    }
    if(!requested.length&&!requestedOptions.regions?.length){releaseLayout();releaseHold();discardPreparation();heights.clear();geometry.clear();requestedOptions.beforeCommit?.();updateMeasured(requested,requestedOptions);committedRevision=requestedRevision;layoutReframe=false;requestedOptions.afterCommit?.();return;}
    if(!flight){
      for(const region of requestedOptions.regions||[])layoutHosts.add(region.host);
      for(const element of layoutHosts)freezePreparedLayout(element);
      const renderContext=requestedOptions.renderContext&&Object.fromEntries(Object.entries(requestedOptions.renderContext).map(([key,value])=>[key,typeof value==='function'?value:structuredClone(value)]));
      flight={rows:structuredClone(requested),revision:requestedRevision,options:{...requestedOptions,renderContext},jobs:new Map(),sizes:new Map(),measurements:new Map(),regions:new Map(),epoch:layoutEpoch,reframe:layoutReframe};
    }
    const current=flight;
    for(const message of current.rows){
      const stamp=signature(message),measurement=current.measurements.get(message.viewKey),job=current.jobs.get(message.viewKey);
      if(measurement&&measurement.signature!==stamp){current.measurements.delete(message.viewKey);current.sizes.delete(message.viewKey);}
      if(job&&(job.signature!==stamp||job.measurement&&!preparedResourcesCurrent(job.context))){job.wait.cancel();disposePreparation(job);job.surface.close();current.jobs.delete(message.viewKey);current.measurements.delete(message.viewKey);current.sizes.delete(message.viewKey);geometry.delete(message.viewKey);}
      const cached=geometry.get(message.viewKey);if(cached?.signature===signature(message)){current.sizes.set(message.viewKey,cached.measurement.size);current.measurements.set(message.viewKey,cached);}}
    holdReadable();
    const indexes=selectedPreparation(current),selected=new Set(indexes.map(index=>current.rows[index].viewKey));let waiting=false;
    if(!refreshRegions(current).valid){invalidate();return;}
    // Reader movement can retire an offscreen candidate while a slow preview
    // waits. Its identity deadline survives; its DOM/resources do not accumulate.
    for(const [key,job]of current.jobs)if(!selected.has(key)){job.wait.cancel();disposePreparation(job);job.surface.close();current.jobs.delete(key);}
    host.setAttribute('aria-busy','true');host.dataset.preparedPending='';
    for(const region of current.options.regions||[]){
      const stamp=layoutEpoch+':'+region.revision;if(regionRevisions.get(region.key)===stamp)continue;
      let job=current.regions.get(region.key);
      if(job?.measurement&&!preparedResourcesCurrent(job.context)){job.wait.cancel();job.surface.close();current.regions.delete(region.key);job=null;}
      if(!job){
        const surface=createPreparationSurface(scroller,region.host),context=createPreparedRender({previousRoot:region.host,renderContext:current.options.renderContext,deadlines,member:region.key,reframe:current.reframe,budget:preparationBudget});
        surface.host.preparedContext=context.connectionContext;context.connectionContext.changed=invalidate;
        surface.host.classList.remove('prepared-region');
        job={region,signature:stamp,surface,context};current.regions.set(region.key,job);
        withPreparedRender(context,region.key,()=>region.render(surface.host));context.root=surface.host;
        job.wait=settlePreparedTree(surface.host,context,{valid:()=>!closed&&flight===current&&layoutEpoch===current.epoch});
        job.wait.promise.then(measurement=>{if(flight!==current||closed)return;job.measurement=measurement;invalidate();}).catch(reason=>rejectPreparation(reason,current));
      }
      if(!job.measurement)waiting=true;
    }
    let admitted=0;
    for(const index of indexes){
      const message=current.rows[index],key=message.viewKey,stamp=signature(message),old=mounted.get(key);
      if(old?.preparedSignature===stamp)continue;
      let job=current.jobs.get(key);
      if(!job){
        if(admitted++>=96){waiting=true;continue;}
        const surface=createPreparationSurface(scroller,host),context=createPreparedRender({previousRoot:old?.row,renderContext:current.options.renderContext,deadlines,member:message.id,reframe:current.reframe,budget:preparationBudget});
        context.connectionContext.changed=invalidate;
        const start=measuredStart(current,index);if(start!==undefined)context.liveTop=()=>scroller.getBoundingClientRect().top+margin+start-scroller.scrollTop;
        job={message,signature:stamp,surface,context};current.jobs.set(key,job);
        job.record=withPreparedRender(context,message.id,()=>render(message,undefined));context.root=job.record.row;job.record.row.preparedContext=context.connectionContext;surface.host.append(job.record.row);
        job.wait=settlePreparedTree(job.record.row,context,{valid:()=>!closed&&flight===current&&layoutEpoch===current.epoch,layout:()=>withPreparedRender(context,message.id,()=>render(message,job.record))});
        job.wait.promise.then(measurement=>{if(flight!==current||closed)return;job.measurement=measurement;current.sizes.set(key,measurement.size);current.measurements.set(key,{signature:stamp,measurement});invalidate();}).catch(reason=>rejectPreparation(reason,current));
      }
      if(!job.measurement)waiting=true;
    }
    if(waiting)return;
    if(!current.indexed){invalidate();return;}
    const regions=refreshRegions(current);if(!regions.valid||regions.changed){invalidate();return;}
    // Replacing a native touch target can prevent its touchend from reaching
    // the scroller. Keep that live tree until contact is released. During the
    // native tail, a shorter canvas must also wait if it would clamp scrollTop
    // before the controller/engine can arbitrate the geometry transaction.
    if(controller?.touching)return;
    if(virtualizer.isTouchScrolling){
      const total=current.rows.reduce((sum,message)=>sum+current.sizes.get(message.viewKey),0);
      const regionsDelta=[...current.regions.values()].reduce((sum,job)=>{const style=getComputedStyle(job.region.host);return sum+job.measurement.size-(style.display==='none'?0:job.region.host.getBoundingClientRect().height+parseFloat(style.marginTop||0)+parseFloat(style.marginBottom||0));},0);
      if(scroller.scrollHeight+total-virtualizer.getTotalSize()+regionsDelta<scroller.scrollTop+scroller.clientHeight-.5)return;
    }
    // All entering/changed rows in this bounded range are ready. Feed their
    // known sizes to the engine before the single flow/geometry transaction.
    committing=new Map();
    current.rows.forEach((message,index)=>{const job=current.jobs.get(message.viewKey);if(job?.measurement)committing.set(message.viewKey,job);});
    current.options.beforeCommit?.();const finalRegions=refreshRegions(current);if(!finalRegions.valid||finalRegions.changed){committing=null;invalidate();return;}owner?.beforeLayout();releaseHold();releaseLayout();
    heights.clear();for(const [key,size]of current.sizes)heights.set(key,size);
    for(const job of current.regions.values()){
      for(const [element,clone]of job.context.frames){const box=clone.getBoundingClientRect();framePreparedElement(element,box.height,element.matches('pot-message-file[data-block="false"]')?box.width:undefined);}
      job.context.phase='commit';commitPreparedResources(job.context);finishPreparedNative(job.context,{commit:true});consumePreparationSurrogates(job.surface.host);
      const attributes=new Set(['hidden','disabled','open',...[...job.region.host.attributes,...job.surface.host.attributes].map(attribute=>attribute.name).filter(name=>name.startsWith('aria-'))]);
      for(const attribute of attributes){if(job.surface.host.hasAttribute(attribute))job.region.host.setAttribute(attribute,job.surface.host.getAttribute(attribute));else job.region.host.removeAttribute(attribute);}
      const preparedNodes=[...job.surface.host.childNodes];for(const node of preparedNodes)moveConversationNode(job.region.host,node);
      for(const node of [...job.region.host.childNodes])if(!preparedNodes.includes(node))node.remove();
      job.region.didCommit?.();job.context.phase='live';
      job.region.host.preparedContext=job.context.connectionContext;
      retirePreparedDeadlines(deadlines,[job.region.key],job.region.host,job.region.key);
      job.region.host.classList.add('prepared-region');regionRevisions.set(job.region.key,job.signature);job.surface.close();
      job.region.host.style.setProperty('--prepared-margin-top',job.measurement.marginTop+'px');job.region.host.style.setProperty('--prepared-margin-bottom',job.measurement.marginBottom+'px');
    }
    configure(current.rows);
    for(const [key,size]of current.sizes){const index=indexesByKey.get(key);if(index!==undefined)virtualizer.resizeItem(index,size);}
    updateMeasured(current.rows,current.options);
    // New records must leave the connected surface before it is retired; doing
    // this inside drawRows would destroy their live leases/diagram instances
    // immediately before the actual same-node flow transfer.
    for(const [key,job]of current.jobs){if(committing.has(key)){job.wait.cancel();disposePreparation(job);}job.surface.close();}
    for(const [key,value]of current.measurements){geometry.delete(key);geometry.set(key,value);}
    const loaded=new Set(current.rows.map(message=>message.viewKey));
    for(const key of geometry.keys()){if(geometry.size<=loaded.size+1280)break;if(!loaded.has(key))geometry.delete(key);}
    committing=null;flight=null;delete host.dataset.preparedPending;layoutHosts.clear();layoutHosts.add(host);for(const region of current.options.regions||[])layoutHosts.add(region.host);for(const record of mounted.values())layoutHosts.add(record.row);for(const element of layoutHosts)rememberPreparedLayout(element);committedRevision=current.revision;layoutReframe=false;host.removeAttribute('aria-busy');current.options.afterCommit?.();
    if(requestedRevision!==current.revision||JSON.stringify(requestedOptions.regions?.map(region=>[region.key,region.revision]))!==JSON.stringify(current.options.regions?.map(region=>[region.key,region.revision])))invalidate();
    }catch(reason){
      preparationFailed(reason);
      throw reason;
    }finally{queueUpdating=false;}
  }
  function updateMeasured(next=rows,options={}) {
    if(closed||updating)return;measuredOptions=options;
    suspended=false;updating=true;
    try{
      owner?.beforeLayout();if(options.parents)owner?.setParents(options.parents);
      if(!next.length)virtualizer.cancelScroll();
      configure(next);virtualizer._willUpdate();
      // Intent changes can pin a different identity without changing the data.
      virtualizer.setOptions({...virtualizer.options,rangeExtractor:range=>pinnedIndexes(range)});
      draw();
      const changed=measure();
      // Materialize the range after the complete prepared size index is installed.
      if(changed||dirty){dirty=false;draw();measure();}
      virtualizer._willUpdate();
      if(owner){
        // A held touch adjustment changes the engine's flow offsets without a
        // native scroll write. Materialize that known range in this commit;
        // the controller still makes exactly one geometry decision.
        if(owner.reconcile())draw();
        owner.publish();
        rememberReadable();
      }
    }finally{updating=false;}
  }
  return Object.create(controller||null,Object.getOwnPropertyDescriptors({setMessages:update,measure,commitPrepared(){
    const environment=currentPreparedRender();if(!environment)return;measuredOptions.beforeCommit?.();
    for(const message of rows){const record=mounted.get(message.viewKey);if(record){withPreparedRender(environment.context,message.id,()=>record.commit?.());record.preparing=false;}}
    measuredOptions.afterCommit?.();
  },reset(){releaseLayout();releaseHold();lastReadable=[];discardPreparation();deadlines.clear();geometry.clear();rowRevisions.clear();revealedRows.clear();requested=[];requestedRevision='[]';committedRevision=null;requestedOptions={};regionRevisions.clear();controller?.reset();},get prepared(){return !layoutReframe&&!flight&&requestedRevision===committedRevision&&(requestedOptions.regions||[]).every(region=>regionRevisions.get(region.key)===layoutEpoch+':'+region.revision);},suspend(){
    releaseLayout();releaseHold();lastReadable=[];discardPreparation();
    cancelAnimationFrame(frame);frame=0;
    owner?.suspend();
    suspended=true;virtualizer.cancelScroll();unmount();
    for(const record of mounted.values())record.childrenWindow?.suspend();
  },close(){
    closed=true;releaseLayout();releaseHold();lastReadable=[];discardPreparation();scroller.removeEventListener('click',interaction,true);scroller.removeEventListener('pot:prepare-resize',interaction);host.ownerDocument.fonts?.removeEventListener('loading',fontChanged);host.ownerDocument.fonts?.removeEventListener('loadingdone',fontChanged);cancelAnimationFrame(frame);geometryObserver?.disconnect();owner?.close();virtualizer.cancelScroll();unmount();
    for(const record of mounted.values())dispose(record);
    mounted.clear();indexesByKey.clear();indexesByID.clear();
    for(const spacer of spacers)spacer.remove();
  }}));
}
