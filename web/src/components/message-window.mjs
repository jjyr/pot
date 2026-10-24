import {moveConversationNode} from './conversation-dom.mjs';
import {Virtualizer, observeElementRect, observeElementOffset, defaultRangeExtractor} from '../vendor/tanstack-virtual-core.mjs';

// TanStack owns item offsets, end anchoring and dynamic size corrections. Pot
// owns the rendered nodes, disclosures and the logical reading destination.
export function createMessageWindow({scroller, host, render, dispose, requestRender, reading, heights = new Map(), nested = false}) {
  let rows = [], mounted = new Map(), closed = false, suspended = false, updating = false, dirty = false;
  let width = 0, margin = 0, target = null, restore, reportRect;
  const pending = new Set(), spacers = [];
  const indexesByKey = new Map(), indexesByID = new Map();
  const margins = new WeakMap(), sources = new WeakMap();
  function navigationIndex() {
    return !target ? -1 : target.end ? rows.length - 1 : (indexesByID.get(target.id) ?? indexesByKey.get(target.id) ?? -1);
  }
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
      return observeElementOffset(instance,(offset,scrolling)=>{
        // Capture reader movement before TanStack commits a different DOM range.
        if(!nested)reading?.observeScroll();
        callback(offset,scrolling);
      });
    },
    indexAttribute:'data-virtual-index', measureElement:sizeOf,
    useAnimationFrameWithResizeObserver:true,
    rangeExtractor:pinnedIndexes, overscan:12,
    anchorTo:nested?'start':'end', followOnAppend:!nested, scrollEndThreshold:1,
    initialOffset:()=>scroller.scrollTop,
    scrollToFn(offset,{adjustments=0}) {
      // The only writer for a conversation's native scroll position. Every
      // command and resize correction is arbitrated by TanStack first.
      if(nested||closed||suspended)return;
      const top=Math.max(0,Math.min(offset+adjustments,scroller.scrollHeight-scroller.clientHeight));
      if(Math.abs(top-scroller.scrollTop)>.5){scroller.scrollTop=top;reading?.acceptScroll();}
    },
    onChange(_instance, sync) {
      if (closed || suspended) return;
      dirty = true;
      if (!updating) {
        // Native scrolling and resize corrections must commit their ranges in
        // the same frame as the offset change, retaining existing nodes.
        if (sync && rows.length && host.isConnected && host.getClientRects().length) update();
        else requestRender();
      }
    },
  });
  if (nested) virtualizer.shouldAdjustScrollPositionOnItemSizeChange=()=>false;
  const unmount = virtualizer._didMount();
  const cancel = ()=>{virtualizer.cancelScroll();target=null;};
  const unbindScroll = nested?undefined:reading?.bindScroll({
    get scrolling(){return virtualizer.isTouchScrolling;},
    get moving(){return virtualizer.isScrolling;},
    cancel,
    project({mode,top,explicit}){
      if(closed||suspended||!explicit&&virtualizer.isTouchScrolling)return;
      if(mode==='latest'){
        if(scroller.scrollHeight-scroller.clientHeight-scroller.scrollTop>.5)virtualizer.scrollToEnd();
      }else if(Math.abs(top-scroller.scrollTop)>.5)virtualizer.scrollToOffset(top);
    },
  });
  const gestures=nested?[]:['wheel','touchstart','pointerdown','keydown'];
  for(const name of gestures)scroller.addEventListener(name,cancel,{passive:true});

  function draw() {
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
    if(width&&nextWidth&&width!==nextWidth){if(!nested)reading?.resize();marginsForMounted();virtualizer.measure();for(const record of mounted.values())pending.add(record);}
    if(nextWidth)width=nextWidth;
    if(rows!==next){
      indexesByKey.clear();indexesByID.clear();
      next.forEach((item,index)=>{indexesByKey.set(item.viewKey,index);indexesByID.set(item.id,index);for(const member of item.members||[])indexesByID.set(member.id,index);});
    }
    const followOnAppend=!nested&&(!reading||reading.following&&!virtualizer.isTouchScrolling);
    const anchorTo=!nested&&(!reading||reading.following)?'end':'start';
    if(rows!==next||Math.abs(nextMargin-margin)>.5||virtualizer.options.followOnAppend!==followOnAppend||virtualizer.options.anchorTo!==anchorTo){
      const list=next; margin=nextMargin;
      virtualizer.setOptions({...virtualizer.options,count:list.length,getItemKey:index=>list[index].viewKey,estimateSize:index=>heights.get(list[index].viewKey)??(list[index].role==='user'?120:list[index].role==='tool'?70:180),rangeExtractor:range=>pinnedIndexes(range),scrollMargin:margin,followOnAppend,anchorTo});
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
    if(options.restore)restore=options.restore;
    try{
      if(!next.length)virtualizer.cancelScroll();
      configure(next);virtualizer._willUpdate();
      if(options.target&&!virtualizer.isTouchScrolling){
        target=options.target;
        virtualizer.setOptions({...virtualizer.options,rangeExtractor:range=>pinnedIndexes(range)});
        if(target.end&&!nested&&!reading)virtualizer.scrollToEnd();
        else if(target.id){const index=navigationIndex();if(index>=0){const item=virtualizer.getMeasurements()[index];if(nested){virtualizer.scrollOffset=item.start-(target.offset??scroller.clientHeight/3);}else if(!reading){virtualizer.setOptions({...virtualizer.options,scrollPaddingStart:target.offset??scroller.clientHeight/3});virtualizer.scrollToIndex(index,{align:'start'});}}}
      }
      draw();
      const changed=measure();
      // One commit after the first measurements; later resizes are incremental.
      if(changed||dirty){dirty=false;draw();measure();}
      virtualizer._willUpdate();
      restore?.();
      if(!nested)reading?.commit();
    }finally{updating=false;}
  }
  return {set:update,measure,layout:update,cancelScroll:cancel,suspend(){
    suspended=true;virtualizer.cancelScroll();unmount();
    for(const record of mounted.values())record.childrenWindow?.suspend();
  },close(){
    closed=true;unbindScroll?.();virtualizer.cancelScroll();unmount();
    for(const name of gestures)scroller.removeEventListener(name,cancel);
    for(const record of mounted.values())dispose(record);
    mounted.clear();pending.clear();indexesByKey.clear();indexesByID.clear();
    for(const spacer of spacers)spacer.remove();
  }};
}
