import {captureReadingPosition,resolveReadingPosition,readingAnchor} from './reading-position.mjs';

// This cursor is the reading intent, not a mirror of scrollTop. Only reader
// gestures and explicit navigation may replace it. Layout writes are outputs.
export function createReadingCursor(history,{active=()=>true,requestRender=()=>{},changed=()=>{},interact=()=>{},upward=()=>{},downward=()=>{},scrolled=()=>{},canReadEarlier=()=>false}={}) {
  let cursor={mode:'latest'},appliedTop=history.scrollTop,width=0,height=0,depth=0,closed=false;
  let touchY=null,scrollController,positionPending=false,layoutPending=false;
  const scrolling=()=>scrollController?.scrolling||false;
  const listeners=[];
  const listen=(name,fn,target=history)=>{target.addEventListener(name,fn,{passive:true});listeners.push([target,name,fn]);};
  const atBottom=()=>history.scrollHeight-history.clientHeight-history.scrollTop<=1;
  function capture(options){
    const position=captureReadingPosition(history,options),anchor=readingAnchor(history);
    // Nested tool parents may span the viewport. Prefer the deepest visible
    // message, while task-only positions keep their own stable row keys.
    if(anchor){
      const row=[...history.querySelectorAll('article.message')].find(node=>node.dataset.messageId===anchor.id);
      const index=position.anchors.findIndex(item=>item.key===row?.dataset.scrollAnchor);
      if(index>0)position.anchors.unshift(...position.anchors.splice(index,1));
    }
    return {...position,anchor};
  }
  const notify=()=>changed(cursor);
  function project(top){scrollController?.project({mode:cursor.mode,top,explicit:positionPending});}
  function apply({layout=false}={}){
    if(closed||depth||!active()||!history.clientHeight)return;
    // TanStack owns native movement and deferred size corrections. Rendering a
    // range must never turn the previous reading anchor into a new command.
    if(scrolling()&&!positionPending){
      flush({native:true});
      width=history.clientWidth;height=history.clientHeight;
      return;
    }
    if(!positionPending&&!layoutPending&&!layout&&cursor.mode==='reading')return;
    depth++;
    try{
      if(cursor.mode==='latest'){project(history.scrollHeight);positionPending=false;}
      else if(cursor.mode==='target'){
        const target=[...history.querySelectorAll(cursor.id?'article.message':'[data-scroll-anchor]')].find(node=>cursor.id?node.dataset.messageId===cursor.id:node.dataset.scrollAnchor===cursor.key);
        if(target){
          const box=target.getBoundingClientRect(),viewport=history.getBoundingClientRect();
          const offset=cursor.align==='nearest'?Math.min(Math.max(box.top-viewport.top,0),Math.max(0,history.clientHeight-box.height)):(history.clientHeight-box.height)/2;
          project(history.scrollTop+box.top-viewport.top-offset);
          positionPending=false;
          cursor={...cursor,position:capture()};
        }
      }else if(cursor.position){
        const {anchor:restored,top}=resolveReadingPosition(history,cursor.position);
        if(top!==undefined)project(top);
        // A gesture can enter a virtual spacer before its rows are mounted.
        // Resolve that provisional pixel position once the window is measured.
        if(restored||!cursor.position.anchors.length){
          positionPending=false;
          if(!layoutPending||!cursor.position.anchors.length||restored?.key!==cursor.position.anchors[0]?.key)cursor={...cursor,position:capture()};
        }
      }
      width=history.clientWidth;height=history.clientHeight;
    }finally{depth--;}
  }
  function set(next){scrollController?.cancel();layoutPending=false;positionPending=true;cursor=structuredClone(next||{mode:'latest'});notify();}
  function pause(options){layoutPending=false;positionPending=false;cursor={mode:'reading',position:capture(options)};notify();}
  function read(){layoutPending=false;cursor=atBottom()?{mode:'latest'}:{mode:'reading',position:capture()};notify();}
  // Width reflow can deliver several measurement batches. Keep its destination
  // anchor through those batches; the next reader gesture releases it.
  function resize(){if(!scrolling()&&cursor.mode==='reading')layoutPending=true;}
  function commit(){
    // TanStack may have compensated changed row estimates without reader input.
    // Save its resulting DOM anchor without turning that output into a command.
    if(!closed&&!depth&&!positionPending&&!layoutPending&&cursor.mode==='reading')cursor={...cursor,position:capture()};
  }
  function flush({native=false}={}){
    if(!active()||depth||positionPending||Math.abs(history.scrollTop-appliedTop)<.5)return false;
    if(!scrolling()&&width&&(history.clientWidth!==width||history.clientHeight!==height)){apply({layout:true});return false;}
    if(!native&&!scrolling()&&!scrollController?.moving)return false;
    const delta=history.scrollTop-appliedTop;
    read();appliedTop=history.scrollTop;scrolled(delta);return true;
  }
  function gesture(event,{up=false}={}){
    if(event.defaultPrevented||!active())return false;
    for(let node=event.target instanceof Element?event.target:null;node&&node!==history;node=node.parentElement){
      if(node.scrollHeight>node.clientHeight&&/^(auto|scroll)$/.test(getComputedStyle(node).overflowY)){
        if(up?node.scrollTop>0:node.scrollTop+node.clientHeight<node.scrollHeight-1)return false;
      }
    }
    // Native focus/reveal can scroll before pointerdown. Adopt that position
    // as part of this reader gesture before projecting anything, or the click
    // target would move between pointerdown and pointerup.
    layoutPending=false;positionPending=false;flush({native:true});interact();
    if(cursor.mode==='target')pause();
    if(history.scrollHeight<=history.clientHeight+1&&!canReadEarlier())return false;
    if(up)pause();
    if(up)upward();else downward();return true;
  }
  listen('wheel',event=>{if(!event.ctrlKey&&!event.metaKey&&Math.abs(event.deltaY)>Math.abs(event.deltaX))gesture(event,{up:event.deltaY<0});});
  listen('pointerdown',event=>{gesture(event);});
  listen('touchstart',event=>{touchY=event.touches.length===1?event.touches[0].clientY:null;if(touchY!==null)gesture(event);});
  listen('touchmove',event=>{const next=event.touches.length===1?event.touches[0].clientY:null;if(touchY!==null&&next!==null&&next!==touchY)gesture(event,{up:next>touchY});touchY=next;});
  const endTouch=()=>{touchY=null;};
  listen('touchend',endTouch);listen('touchcancel',endTouch);
  listen('keydown',event=>{
    if(event.altKey||event.target.closest('input,textarea,select,[contenteditable]'))return;
    if((event.ctrlKey||event.metaKey)&&!['ArrowUp','ArrowDown','Home','End'].includes(event.key))return;
    if(event.key!=='Tab'&&event.target.closest('button,a,summary')&&[' ','Enter'].includes(event.key))return;
    if(['ArrowUp','ArrowDown','PageUp','PageDown','Home','End','Tab',' '].includes(event.key))gesture(event,{up:['ArrowUp','PageUp','Home'].includes(event.key)||event.key===' '&&event.shiftKey});
  });
  return {
    bindScroll(controller){scrollController=controller;return()=>{if(scrollController===controller)scrollController=null;};},
    observeScroll(){flush({native:true});requestRender();},
    acceptScroll(){appliedTop=history.scrollTop;commit();},
    commit,
    resize,
    get following(){return cursor.mode==='latest';},
    get destination(){return cursor.mode==='target'?cursor:null;},
    get position(){return cursor.position;},
    get anchor(){return cursor.position?.anchor;},
    snapshot(){flush();return structuredClone(cursor);},
    restore:set,
    latest(){set({mode:'latest'});},
    locate(target){set({mode:'target',...target});},
    pause,
    engage:gesture,
    flush,
    apply,
    reflow(){if(width&&history.clientWidth!==width)resize();apply({layout:history.clientWidth!==width||history.clientHeight!==height});requestRender();},
    // Any producer can publish a synchronous layout transaction. The composer
    // does not know which cursor or container is being preserved.
    layout(work){
      if(!depth)flush();depth++;
      try{return work();}finally{if(!--depth){if(width&&history.clientWidth!==width)resize();apply({layout:true});}}
    },
    dispose(){closed=true;scrollController=null;for(const [target,name,fn]of listeners)target.removeEventListener(name,fn);},
  };
}
