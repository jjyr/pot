import {messageBytes} from './projection-bytes.mjs';
const identity=value=>JSON.stringify([value.runtime_id,value.owner_id,value.conversation_id,value.boot_id]);

// Only uncommitted transport work is buffered. Adjacent deltas coalesce while a
// transaction is in flight; there is no retained owner/snapshot cache here.
function coalesce(previous,next) {
  if(next.kind!=='delta'||identity(previous)!==identity(next)||String(next.base_cursor)!==previous.cursor)return null;
  const messages=new Map(previous.messages.map(message=>[message.id,message]));
  for(const message of next.messages)messages.set(message.id,message);
  const replies=new Map(previous.replies.map(reply=>[reply.input_id,reply]));
  for(const reply of next.replies)replies.set(reply.input_id,reply);
  return {...next,kind:previous.kind,base_cursor:previous.base_cursor,messages:[...messages.values()],replies:[...replies.values()],
    history:previous.kind==='reset'?{...next.history,before:previous.history?.before}:next.history};
}

export function createChatSync({cache,key,context=()=>({}),committed,failed,delay=16,host=globalThis}) {
  let pending=[],flight=null,timer,closed=false,terminalError=null;
  function terminate(reason){
    terminalError=reason;closed=true;pending=[];host.clearTimeout(timer);timer=undefined;
  }
  function fail(reason){
    pending=[];
    if(closed)return;
    let handled;
    try{handled=failed(reason);}catch(error){terminate(error);return Promise.reject(error);}
    return Promise.resolve(handled).catch(error=>{terminate(error);throw error;});
  }
  function drain(){
    if(terminalError)return Promise.reject(terminalError);
    host.clearTimeout(timer);timer=undefined;
    if(flight)return flight;
    if(!pending.length)return Promise.resolve();
    const frames=pending;pending=[];
    flight=Promise.resolve().then(()=>cache.writeFrames(key,frames,context())).then(snapshot=>{
      if(snapshot&&!closed)return committed(snapshot,frames.flatMap(frame=>frame.messages));
    }).catch(reason=>{
      if(terminalError)throw terminalError;
      return fail(reason);
    }).finally(()=>{flight=null;if(pending.length&&!closed)schedule();});
    flight.catch(()=>{});
    return flight;
  }
  function schedule(){if(timer===undefined&&!flight)timer=host.setTimeout(drain,delay);}
  async function flush(){
    if(terminalError)throw terminalError;
    host.clearTimeout(timer);timer=undefined;
    while(flight||pending.length){await drain();if(terminalError)throw terminalError;}
  }
  return {
    push(frame){
      if(closed||terminalError)return;
      const previous=pending.at(-1),merged=previous&&coalesce(previous,frame);
      if(merged)pending[pending.length-1]=merged;else pending.push(frame);
      // A reset supersedes pending older projections and invalidates their
      // history ranges in the durable cache. The current write still finishes.
      if(frame.kind==='reset')pending=[frame];
      const messages=pending.flatMap(value=>value.messages);
      if(messages.length>512||messages.reduce((bytes,message)=>bytes+messageBytes(message),0)>32*1024*1024){
        closed=true;pending=[];host.clearTimeout(timer);
        let handled;
        try{handled=failed(Error('chat_cache_backpressure'));}catch(error){terminate(error);return;}
        Promise.resolve(handled).catch(error=>terminate(error));
        return;
      }
      if(frame.replies.some(reply=>['completed','failed','cancelled','unknown'].includes(reply.state))||frame.messages.some(message=>message.status==='failed'))drain();else schedule();
    },
    flush,
    retire(){closed=true;return flush();},
    close(){
      if(terminalError)return Promise.reject(terminalError);
      closed=true;host.clearTimeout(timer);pending=[];
      if(flight)return flight.then(()=>{if(terminalError)throw terminalError;});
      return Promise.resolve();
    },
  };
}
