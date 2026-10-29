// Keep native and browser reads on the same scoped, authorized JSON transport.
// A shared budget bounds outstanding Base64 strings across preview types.
const chunkSize=192*1024,limit=6,queue=[];let active=0;
function pump(){
  while(active<limit&&queue.length){
    const entry=queue.shift();entry.signal.removeEventListener('abort',entry.cancel);
    if(entry.signal.aborted){entry.reject(entry.signal.reason);continue;}
    active++;let released=false;entry.resolve(()=>{if(released)return;released=true;active--;pump();});
  }
}
function acquire(signal){
  signal.throwIfAborted();
  return new Promise((resolve,reject)=>{
    const entry={signal,resolve,reject,cancel(){const index=queue.indexOf(entry);if(index>=0)queue.splice(index,1);reject(signal.reason);}};
    signal.addEventListener('abort',entry.cancel,{once:true});queue.push(entry);pump();
  });
}

export async function readResourceChunks({size,revision,type,read,signal,check=()=>{}}){
  if(!Number.isSafeInteger(size)||size<0)throw Error('Invalid resource size');
  const abort=new AbortController(),combined=signal?AbortSignal.any([signal,abort.signal]):abort.signal;
  const parts=new Array(Math.max(1,Math.ceil(size/chunkSize)));let next=0,failure;
  async function worker(){
    try{
      while(next<parts.length){
        combined.throwIfAborted();check();const index=next++,offset=index*chunkSize;
        const release=await acquire(combined);
        try{
          combined.throwIfAborted();check();
          const chunk=await read(offset,combined);combined.throwIfAborted();check();
          const length=Math.min(chunkSize,size-offset);
          if(chunk.size!==size||chunk.offset!==offset||chunk.revision!==revision||typeof chunk.data!=='string'||chunk.data.length>4*Math.ceil(length/3))throw Error('Invalid resource chunk');
          const decoded=atob(chunk.data);if(decoded.length!==length)throw Error('Invalid resource bytes');
          const bytes=new Uint8Array(length);for(let i=0;i<length;i++)bytes[i]=decoded.charCodeAt(i);
          // Retain only opaque Blob parts, not the full image's JS byte arrays.
          parts[index]=new Blob([bytes]);
        }finally{release();}
      }
    }catch(error){failure||=error;abort.abort(error);}
  }
  try{
    await Promise.all(Array.from({length:Math.min(4,parts.length)},worker));
    if(failure)throw failure;combined.throwIfAborted();check();
    return new Blob(parts,{type});
  }finally{parts.length=0;}
}
