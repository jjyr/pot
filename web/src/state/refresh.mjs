// One request at a time. Changes arriving during it require one later read.
// A view owns cancellation; an invalidation must never cancel its current read.
export function createRefreshQueue(work,{active=()=>true,onError=()=>{}}={}) {
  let pending=null,again=false,closed=false;
  const current=()=>!closed&&active();
  const queue={
    run(){
      if(!current())return Promise.resolve();
      if(pending){again=true;return pending;}
      pending=(async()=>{try{await work();}catch(error){if(current()&&error.name!=='AbortError')onError(error);}})().finally(()=>{
        pending=null;if(again&&current()){again=false;queueMicrotask(()=>queue.run());}
      });
      return pending;
    },
    close(){closed=true;again=false;},
  };return queue;
}

export function relevantChange(topics,dependencies,space='',source={}) {
  return (!space||!source.space||space===source.space)&&(!topics.length||topics.some(topic=>dependencies.includes(topic)));
}
