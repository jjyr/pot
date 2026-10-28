// The initial history is laid out while visibility:hidden keeps its real width.
// Resource completion alone is insufficient: it can change the virtual range,
// which mounts more resources. Recheck readiness after each measured commit.
export function createInitialChatLayout({history, layout, position = ()=>{}, ready, reveal, fontFallback, timeout = 5000}) {
  const document=history.ownerDocument,view=document.defaultView;
  let current;
  const resources='pot-file-image,pot-resource,pot-mermaid,pot-message-file';
  function cancel(){
    if(!current)return;
    current.observer.disconnect();current.resize.disconnect();
    view.cancelAnimationFrame(current.frame);view.clearTimeout(current.timer);
    current=null;
  }
  function start(){
    cancel();
    const run={frame:0,previous:null,expired:false};current=run;
    const active=()=>current===run;
    const schedule=()=>{if(active()&&!run.frame)run.frame=view.requestAnimationFrame(check);};
    const pending=()=>[...history.querySelectorAll(resources)].filter(node=>node.getBoundingClientRect().height>0&&!node.closest('.initial-layout-slot,.prepared-frame')&&node.dataset.state==='loading');
    const geometry=()=>JSON.stringify([history.scrollTop,history.scrollHeight,history.clientWidth,history.clientHeight,
      ...[...history.querySelectorAll('article.message,[data-scroll-anchor]')].map(node=>{const box=node.getBoundingClientRect();return [node.dataset.scrollAnchor,box.top,box.height];})]);
    function check(){
      run.frame=0;if(!active()||!ready())return;
      layout();
      run.observer.takeRecords();
      if(!active())return;
      if(!ready()){run.previous=null;return;}
      const waiting=pending();
      if(run.expired){
        // A stalled/offline preview must not hide the conversation forever or
        // resize it later. Its contents remain usable inside a reserved slot.
        for(const node of waiting){
          const box=node.getBoundingClientRect(),inline=node.matches('pot-message-file[data-block="false"]');
          // Inline references stay links, even after binding. Reserve their
          // measured link box, not the minimum height used for block previews.
          if(inline)node.style.setProperty('--initial-layout-width',`${box.width}px`);
          node.style.setProperty('--initial-layout-height',`${inline?box.height:Math.max(240,box.height)}px`);
          node.classList.add('initial-layout-slot');
        }
        if(document.fonts?.status==='loading')fontFallback();
      }else if(waiting.length||document.fonts?.status==='loading'){
        run.previous=null;return;
      }
      // Measure again after readiness/fallback changed geometry, then require
      // the measured range and final native offset to survive another layout.
      position();
      layout();
      run.observer.takeRecords();
      if(!ready()){run.previous=null;return;}
      if(pending().length){run.previous=null;schedule();return;}
      const next=geometry();
      // Live output or an interactive document can keep changing indefinitely.
      // After the resource deadline, allow one final measured commit and expose
      // the current viewport; subsequent live changes use normal scroll intent.
      if(next!==run.previous&&(!run.expired||!run.deadlineMeasured)){
        run.previous=next;run.deadlineMeasured=run.expired;schedule();return;
      }
      cancel();reveal();
    }
    run.observer=new MutationObserver(schedule);
    run.observer.observe(history,{subtree:true,childList:true,attributes:true,attributeFilter:['data-state','hidden','aria-busy']});
    run.resize=new ResizeObserver(schedule);run.resize.observe(history);
    run.timer=view.setTimeout(()=>{run.expired=true;schedule();},timeout);
    document.fonts?.ready.then(schedule);
    schedule();
  }
  return {start,cancel,get pending(){return !!current;}};
}
