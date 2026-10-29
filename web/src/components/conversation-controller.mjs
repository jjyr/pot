import {captureReadingPosition,resolveReadingPosition,readingAnchor} from './reading-position.mjs';
import {bindConversationInput} from './conversation-input.mjs';

// One intent and one geometry decision for a scroller. The virtualizer supplies
// measured offsets and executes corrections, including deferred touch writes;
// it never independently chooses an anchor, append-follow or resize correction.
export function createConversationController({scroller,virtualizer,requestLayout,syncOffset=()=>{},inputStarted=()=>{},intentChanged=()=>{},active=()=>true,interact=()=>{},upward=()=>{},downward=()=>{},scrolled=()=>{},canReadEarlier=()=>false,earlierBoundary,onEarlierVisible}) {
  let intent={mode:'latest'},published=intent,settledPosition,parents=new Map(),closed=false,restoring=false;
  let appliedTop=scroller.scrollTop,echo=null,inputPending=false,pointer=false,touchMoved=false,motion=false,up=false,commandTop=null,deferred=0;
  let inputDelta=0,boundaryDirection=null,touching=false,landing=null,latestInputPosition=null;
  const subscribers=new Set();
  function earlierVisible(){if(closed||!active()||!scroller.clientHeight||!earlierBoundary)return;const box=earlierBoundary.getBoundingClientRect(),viewport=scroller.getBoundingClientRect();if(box.bottom>=viewport.top&&box.top<=viewport.bottom)onEarlierVisible?.();}
  const boundaryObserver=earlierBoundary?new IntersectionObserver(earlierVisible,{root:scroller}):null;
  if(earlierBoundary)boundaryObserver.observe(earlierBoundary);
  virtualizer.shouldAdjustScrollPositionOnItemSizeChange=()=>false;
  function capture(options){
    const position=captureReadingPosition(scroller,options),anchor=readingAnchor(scroller);
    if(anchor){const row=[...scroller.querySelectorAll('article.message')].find(node=>node.dataset.messageId===anchor.id||node.dataset.anchorMessageId===anchor.id);const index=position.anchors.findIndex(item=>item.key===row?.dataset.scrollAnchor);if(index>0)position.anchors.unshift(...position.anchors.splice(index,1));}
    return {...position,anchor};
  }
  function cancel(){landing=null;virtualizer.cancelScroll();commandTop=null;restoring=false;}
  function reading(options){
    const position=structuredClone(intent.position||latestInputPosition||settledPosition||capture(options));
    if(options?.ignoreToggle)position.anchors=position.anchors.filter(item=>item.key!=='background-tasks-toggle');
    intent={mode:'reading',position};
  }
  function acceptInput({force=false}={}){
    if(closed||!active()||!scroller.clientHeight)return false;
    const top=scroller.scrollTop;
    const owned=inputPending||pointer||touchMoved&&virtualizer.isTouchScrolling||motion&&virtualizer.isScrolling;
    if(!force&&(!owned||echo!==null&&Math.abs(top-echo)<.5)||Math.abs(top-appliedTop)<.5)return false;
    landing=null;const delta=top-appliedTop;appliedTop=top;echo=null;inputPending=false;
    reading();
    // Translate the committed anchor; recapturing live DOM here would adopt
    // unmeasured height changes as user input.
    // Keep the accumulated input anchor even when this frame reaches Latest.
    // A fast reversal may arrive before publish captures the bottom position.
    const position=intent.position;position.top=top;
    for(const anchor of position.anchors)anchor.offset-=delta;
    if(position.anchor)position.anchor.offset-=delta;
    latestInputPosition=null;
    if(delta>0&&!up&&scroller.scrollHeight-scroller.clientHeight-top<=1){latestInputPosition=position;intent={mode:'latest'};}
    inputDelta+=delta;return true;
  }
  function engage({event,kind,up:direction}){
    if(closed||!active())return false;
    // Focus/reveal may move before pointerdown. Adopt that movement as input.
    acceptInput({force:kind==='pointer'});cancel();interact(event);
    if(kind==='touchstart'){touchMoved=false;touching=true;}
    if(kind==='touchmove')touchMoved=true;
    inputStarted();
    pointer=kind==='pointer';motion=kind==='key'?'key':kind==='wheel'?'wheel':false;up=direction;inputPending=true;echo=null;
    if(intent.mode==='target'||direction)reading();
    if(scroller.scrollHeight>scroller.clientHeight+1||canReadEarlier())boundaryDirection=direction?'up':'down';
    intentChanged();
    requestLayout();return true;
  }
  const input=bindConversationInput(scroller,{engage,endPointer:()=>{pointer=false;},endTouch:()=>{touching=false;requestLayout();},endMotion:()=>{if(!inputPending&&(motion!=='key'||!virtualizer.isScrolling))motion=false;}});
  function set(next){cancel();latestInputPosition=null;inputPending=false;pointer=false;touchMoved=false;motion=false;echo=null;inputDelta=0;boundaryDirection=null;restoring=true;intent=structuredClone(next||{mode:'latest'});intentChanged();requestLayout();}
  function desired(){
    if(intent.mode==='latest')return scroller.scrollHeight-scroller.clientHeight;
    if(intent.mode==='target'){
      const target=[...scroller.querySelectorAll(intent.id?'article.message':'[data-scroll-anchor]')].find(row=>!row.closest('[data-conversation-hold]')&&(intent.id?row.dataset.messageId===intent.id:row.dataset.scrollAnchor===intent.key));
      if(!target||!target.getBoundingClientRect().height)return;
      const box=target.getBoundingClientRect(),top=box.top-scroller.getBoundingClientRect().top;
      const offset=intent.align==='nearest'?Math.min(Math.max(top,0),Math.max(0,scroller.clientHeight-box.height)):(scroller.clientHeight-box.height)/2;
      return scroller.scrollTop+top-offset;
    }
    if(intent.position){const result=resolveReadingPosition(scroller,intent.position);if(result.anchor||!intent.position.anchors.length)return result.top;}
  }
  function reconcile(){
    if(closed||!active()||!scroller.clientHeight)return false;
    // Native keyboard/compositor motion can advance during the measured DOM
    // commit, after beforeLayout adopted its previous frame. Base this one
    // correction on the current input offset in both model and engine.
    if(acceptInput())syncOffset(scroller.scrollTop);
    const target=desired();if(target===undefined)return false;
    const top=Math.max(0,Math.min(target,Math.max(0,scroller.scrollHeight-scroller.clientHeight)));
    let delta=top-scroller.scrollTop;
    // The engine announces a held-offset release before its native write. That
    // displacement was already allocated by this model; a synchronous range
    // commit in that announcement must not allocate the same correction again.
    if(deferred&&!virtualizer.isTouchScrolling){const pending=virtualizer.getScrollOffset()-scroller.scrollTop;if(pending*deferred>0)delta-=Math.sign(deferred)*Math.min(Math.abs(deferred),Math.abs(pending));}
    if(Math.abs(delta)<1e-7){restoring=false;return false;}
    if(landing&&Math.abs(landing.requested-top)<1e-7&&landing.actual===scroller.scrollTop){restoring=false;return false;}
    if(restoring){
      const command=top+(virtualizer.isTouchScrolling?deferred:0);
      if(commandTop===command)return false;
      commandTop=command;virtualizer.scrollToOffset(command);return true;
    }
    // All size/data/reflow changes use the same current intent. A later RO
    // batch for an already accounted height yields zero, not a second command.
    const before=scroller.scrollTop;virtualizer.applyScrollAdjustment(delta);
    if(scroller.scrollTop===before&&virtualizer.isTouchScrolling)deferred+=delta;
    return true;
  }
  function publish(){
    if(closed||!active()||!scroller.clientHeight)return;
    const top=desired();
    if(top===undefined||Math.abs(Math.max(0,Math.min(top,Math.max(0,scroller.scrollHeight-scroller.clientHeight)))-scroller.scrollTop)>.5)return;
    restoring=false;commandTop=null;
    const residual=Math.max(0,Math.min(top,Math.max(0,scroller.scrollHeight-scroller.clientHeight)))-scroller.scrollTop;
    if(Math.abs(residual)>1e-7&&(!landing||Math.abs(landing.requested-scroller.scrollTop-residual)>1e-7||landing.actual!==scroller.scrollTop))return;
    settledPosition=capture();latestInputPosition=null;
    // A native integer landing cannot satisfy every CSS-pixel destination. Keep
    // that residual in the sole controller's intended offsets when adopting a
    // newly visible native anchor; publication must not erase it.
    settledPosition.top+=residual;for(const anchor of settledPosition.anchors)anchor.offset-=residual;
    if(settledPosition.anchor)settledPosition.anchor.offset-=residual;
    if(intent.mode!=='latest')intent={...intent,position:settledPosition};
    appliedTop=scroller.scrollTop;
    if(JSON.stringify(intent)!==JSON.stringify(published)){published=structuredClone(intent);for(const listener of subscribers)listener(structuredClone(published));}
    // Boundary consumers read the publication, not the preceding frame's
    // persisted anchor. Fetch/commit/paging remain in their business domains.
    earlierVisible();
    const delta=inputDelta,direction=boundaryDirection;inputDelta=0;boundaryDirection=null;
    if(inputPending&&!virtualizer.isScrolling&&(direction==='up'&&scroller.scrollTop===0||direction==='down'&&scroller.scrollHeight-scroller.clientHeight-scroller.scrollTop<=1)){inputPending=false;motion=false;}
    if(delta)scrolled(delta);
    if(direction==='up')upward();else if(direction==='down')downward();
  }
  const api={
    subscribeReading(listener){subscribers.add(listener);listener(structuredClone(published));return()=>subscribers.delete(listener);},
    // Context/cache consumers need the newest reader command even while a
    // candidate body is waiting. Subscribers still receive measured commits.
    snapshot:()=>structuredClone(intent),
    reset(){cancel();virtualizer.cleanup();intent={mode:'latest'};published=structuredClone(intent);settledPosition=undefined;latestInputPosition=null;parents=new Map();inputPending=false;pointer=false;touchMoved=false;touching=false;motion=false;echo=null;deferred=0;inputDelta=0;boundaryDirection=null;appliedTop=scroller.scrollTop;for(const listener of subscribers)listener(structuredClone(published));requestLayout();},
    restore:set,
    scrollToLatest:()=>set({mode:'latest'}),
    locate:target=>set({mode:'target',...target}),
    pause(options){cancel();acceptInput();reading(options);intentChanged();requestLayout();},
    engage:input.engage,
    get landing(){return landing&&{...landing,residual:landing.requested-landing.actual};},
    get following(){return intent.mode==='latest';},
    get touching(){return touching;},
    get destination(){return intent.mode==='target'?{id:intent.id,key:intent.key,align:intent.align,expand:intent.expand}:null;},
    get position(){return intent.position&&structuredClone(intent.position);},
    get anchor(){return intent.position?.anchor&&{...intent.position.anchor};},
    get revealing(){return restoring||intent.mode==='target'&&intent.expand;},
    get visibilityPath(){const path=new Map(),seen=new Set();let id=intent.id||intent.position?.anchor?.id;while(parents.has(id)&&!seen.has(id)){seen.add(id);const parent=parents.get(id);path.set(parent,id);id=parent;}return path;},
  };
  return {api,beforeLayout(){if(acceptInput())syncOffset(scroller.scrollTop);},reconcile,publish,setParents:next=>{parents=next||new Map();},
    // TanStack need not notify when an offset stays inside the same item range.
    // Reading publication still needs a commit for every adopted input frame.
    nativeScroll(){if(acceptInput())requestLayout();},
    idle(){if(!inputPending)motion=false;},
    wrote(top,requested=top){landing={requested,actual:top};appliedTop=top;echo=top;deferred=0;if(Math.abs(requested-top)>1e-7)syncOffset(top);},
    suspend(){latestInputPosition=null;landing=null;deferred=0;inputPending=false;pointer=false;touchMoved=false;touching=false;motion=false;echo=null;inputDelta=0;boundaryDirection=null;},
    close(){closed=true;boundaryObserver?.disconnect();input.close();subscribers.clear();},
  };
}
