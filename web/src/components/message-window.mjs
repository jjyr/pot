import {moveConversationNode} from './conversation-dom.mjs';
import {Virtualizer, observeElementRect, observeElementOffset, defaultRangeExtractor} from '../vendor/tanstack-virtual-core.mjs';
import {createConversationController} from './conversation-controller.mjs';

// The controller owns intent and compensation; this adapter commits its measured
// flow ranges. Child adapters share that controller and never write the scroller.
export function createMessageWindow({scroller, host, render, dispose, requestRender, controller:parentController, active, interact, upward, downward, scrolled, canReadEarlier, earlierBoundary, onEarlierVisible, heights = new Map(), nested = false}) {
  let rows = [], mounted = new Map(), closed = false, suspended = false, updating = false, dirty = false;
  let width = 0, margin = 0, reportRect, reportOffset, owner, controller=parentController, frame=0;
  const pending = new Set(), spacers = [];
  const indexesByKey = new Map(), indexesByID = new Map();
  const margins = new WeakMap(), sources = new WeakMap();
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
  function sizeOf(row, entry) {
    const height = entry?.borderBoxSize?.[0]?.blockSize ?? row.getBoundingClientRect().height;
    // Hidden conversations and collapsed ancestors have no usable measurement.
    const key = row.dataset.windowKey;
    if (!height) return heights.get(key) ?? 70;
    let extra = margins.get(row);
    if (extra === undefined) {
      const style = getComputedStyle(row);
      extra = parseFloat(style.marginTop || 0) + parseFloat(style.marginBottom || 0);
      margins.set(row, extra);
    }
    const size = height + extra;
    heights.delete(key); heights.set(key, size);
    while (heights.size > 1280) heights.delete(heights.keys().next().value);
    return size;
  }
  const virtualizer = new Virtualizer({
    count: 0, getScrollElement:()=>scroller,
    getItemKey:index=>rows[index].viewKey,
    estimateSize:index=>heights.get(rows[index].viewKey)??(rows[index].role==='user'?120:rows[index].role==='tool'?70:180),
    observeElementRect(instance,callback){reportRect=callback;return observeElementRect(instance,callback);},
    observeElementOffset(instance,callback){
      reportOffset=callback;
      return observeElementOffset(instance,(offset,scrolling)=>{
        // Capture reader movement before TanStack commits a different DOM range.
        if(!nested)owner?.nativeScroll();
        callback(offset,scrolling);
        if(!nested&&!scrolling)owner?.idle();
      });
    },
    indexAttribute:'data-virtual-index', measureElement:sizeOf,
    useAnimationFrameWithResizeObserver:true,
    rangeExtractor:pinnedIndexes, overscan:12,
    anchorTo:'start', followOnAppend:false, scrollEndThreshold:1,
    initialOffset:()=>scroller.scrollTop,
    scrollToFn(offset,{adjustments=0}) {
      // The only writer for a conversation's native scroll position. Every
      // command and resize correction is arbitrated by TanStack first.
      if(nested||closed||suspended)return;
      const top=Math.max(0,Math.min(offset+adjustments,scroller.scrollHeight-scroller.clientHeight));
      if(Math.abs(top-scroller.scrollTop)>.5){scroller.scrollTop=top;owner?.wrote(scroller.scrollTop);}
    },
    onChange(_instance, sync) {
      if (closed || suspended) return;
      dirty = true;
      if (!updating) {
        // Native scrolling and resize corrections must commit their ranges in
        // the same frame as the offset change, retaining existing nodes.
        if (sync && rows.length && host.isConnected && host.getClientRects().length) update();
        else if(nested)requestRender();else invalidate();
      }
    },
  });
  if(nested)virtualizer.shouldAdjustScrollPositionOnItemSizeChange=()=>false;
  const unmount = virtualizer._didMount();
  if(!nested){owner=createConversationController({scroller,virtualizer,requestLayout:invalidate,syncOffset:offset=>reportOffset?.(offset,true),active,interact,upward,downward,scrolled,canReadEarlier,earlierBoundary,onEarlierVisible});controller=owner.api;}
  const geometryObserver=nested?null:new ResizeObserver(invalidate);
  geometryObserver?.observe(scroller);if(host.parentElement)geometryObserver?.observe(host.parentElement);

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
    const layout = []; let offset=0, spacerIndex=0;
    for(const item of items){
      const start=item.start-margin;
      if(start>offset+.5||!layout.length&&start<-.5)layout.push({gap:start-offset});
      const message=rows[item.index], old=mounted.get(item.key);
      const changed=!old||sources.get(old)!==message;
      const record=render(message,old);
      sources.set(record,message);
      record.row.dataset.windowKey=item.key;record.row.dataset.virtualIndex=String(item.index);
      // Stable reading identity belongs to the list contract, including callers
      // whose renderer only supplies content and a row element.
      record.row.dataset.messageId=message.id;record.row.dataset.scrollAnchor=`message:${item.key}`;
      layout.push({record});if(changed||!record.row.isConnected)pending.add(record);
      offset=item.end-margin;
    }
    const total=virtualizer.getTotalSize();
    if(total>offset+.5)layout.push({gap:total-offset});
    for(const [key,record]of mounted)if(!selected.has(key)){
      virtualizer.observer.unobserve(record.row);virtualizer.elementsCache.delete(key);
      mounted.delete(key);pending.delete(record);dispose(record);
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
  function measure() {
    let changed=false;
    for(const record of mounted.values())changed=record.childrenWindow?.measure()||changed;
    // Only new/updated rows need a synchronous first measurement. Subsequent
    // size changes arrive through TanStack's batched ResizeObserver entries.
    for(const record of pending){
      if(!record.row.isConnected)continue;
      const index=Number(record.row.dataset.virtualIndex),before=virtualizer.itemSizeCache.get(record.row.dataset.windowKey);
      virtualizer.measureElement(record.row);
      // Measure new flow rows in this commit, including while scrolling. The
      // controller alone decides compensation; late duplicate RO sizes do not.
      if(virtualizer.isScrolling)virtualizer.resizeItem(index,sizeOf(record.row));
      changed=before!==virtualizer.itemSizeCache.get(record.row.dataset.windowKey)||changed;
    }
    pending.clear();return changed;
  }
  function configure(next) {
    const viewport=scroller.getBoundingClientRect(),box=host.getBoundingClientRect();
    const nextMargin=scroller.scrollTop+box.top-viewport.top;
    const nextWidth=host.clientWidth;
    const rect={width:scroller.clientWidth,height:scroller.clientHeight};
    if(virtualizer.scrollRect?.width!==rect.width||virtualizer.scrollRect?.height!==rect.height)reportRect?.(rect);
    if(width&&nextWidth&&width!==nextWidth){marginsForMounted();virtualizer.measure();for(const record of mounted.values())pending.add(record);}
    if(nextWidth)width=nextWidth;
    if(rows!==next){
      indexesByKey.clear();indexesByID.clear();
      next.forEach((item,index)=>{indexesByKey.set(item.viewKey,index);indexesByID.set(item.id,index);for(const member of item.members||[])indexesByID.set(member.id,index);});
    }
    if(rows!==next||Math.abs(nextMargin-margin)>.5){
      const list=next; margin=nextMargin;
      virtualizer.setOptions({...virtualizer.options,count:list.length,getItemKey:index=>list[index].viewKey,estimateSize:index=>heights.get(list[index].viewKey)??(list[index].role==='user'?120:list[index].role==='tool'?70:180),rangeExtractor:range=>pinnedIndexes(range),scrollMargin:margin});
      rows=next;
      const available=new Set(rows.map(item=>item.viewKey));
      for(const [key,record]of mounted)if(!available.has(key)){virtualizer.observer.unobserve(record.row);virtualizer.elementsCache.delete(key);mounted.delete(key);pending.delete(record);dispose(record);}
      for(const key of virtualizer.itemSizeCache.keys())if(!available.has(key))virtualizer.itemSizeCache.delete(key);
    }
  }
  function marginsForMounted(){for(const record of mounted.values())margins.delete(record.row);}
  function update(next=rows,options={}) {
    if(closed||updating)return;
    suspended=false;updating=true;
    try{
      owner?.beforeLayout();if(options.parents)owner?.setParents(options.parents);
      if(!next.length)virtualizer.cancelScroll();
      configure(next);virtualizer._willUpdate();
      // Intent changes can pin a different identity without changing the data.
      virtualizer.setOptions({...virtualizer.options,rangeExtractor:range=>pinnedIndexes(range)});
      draw();
      const changed=measure();
      // One commit after the first measurements; later resizes are incremental.
      if(changed||dirty){dirty=false;draw();measure();}
      virtualizer._willUpdate();
      if(owner){
        for(let pass=0;pass<3&&owner.reconcile();pass++){draw();if(measure())draw();}
        owner.publish();
      }
    }finally{updating=false;}
  }
  return Object.assign(Object.create(controller||null),{setMessages:update,measure,suspend(){
    cancelAnimationFrame(frame);frame=0;
    owner?.suspend();
    suspended=true;virtualizer.cancelScroll();unmount();
    for(const record of mounted.values())record.childrenWindow?.suspend();
  },close(){
    closed=true;cancelAnimationFrame(frame);geometryObserver?.disconnect();owner?.close();virtualizer.cancelScroll();unmount();
    for(const record of mounted.values())dispose(record);
    mounted.clear();pending.clear();indexesByKey.clear();indexesByID.clear();
    for(const spacer of spacers)spacer.remove();
  }});
}
