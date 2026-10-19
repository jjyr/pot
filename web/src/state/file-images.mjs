const limit=16*1024*1024,chunkLimit=192*1024;

// Only raster originals become browser image sources; never execute artifact HTML/SVG.
function imageType(bytes) {
  const starts=values=>values.every((value,index)=>bytes[index]===value);
  if(starts([137,80,78,71,13,10,26,10]))return 'image/png';
  if(starts([255,216,255]))return 'image/jpeg';
  const text=new TextDecoder('ascii').decode(bytes.subarray(0,12));
  if(/^GIF8[79]a/.test(text))return 'image/gif';
  if(text.startsWith('RIFF')&&text.slice(8)==='WEBP')return 'image/webp';
  throw Error('Unsupported artifact image');
}

export function createFileImages(request,getScope) {
  const entries=new Map();
  return function acquire(task,path,options={}) {
    const scope={...getScope(),...options},key=JSON.stringify([scope.space,scope.owner,task,path]);
    let entry=entries.get(key);
    if(!entry){
      entry={count:0,abort:new AbortController()};entries.set(key,entry);
      entry.url=(async()=>{
        const parts=[];let offset=0,size,revision,type;
        do {
          const query=new URLSearchParams({path,offset:String(offset)});if(revision)query.set('revision',revision);
          const chunk=await request(`/api/v1/tasks/${encodeURIComponent(task)}/file?${query}`,{...scope,signal:entry.abort.signal});
          if(!Number.isSafeInteger(chunk.size)||chunk.size<=0||chunk.size>limit||chunk.offset!==offset||typeof chunk.revision!=='string'||!chunk.revision||chunk.revision.length>128
            ||(revision&&(chunk.revision!==revision||chunk.size!==size))||typeof chunk.data!=='string'||chunk.data.length>4*Math.ceil(chunkLimit/3))throw Error('Invalid artifact image chunk');
          size=chunk.size;revision=chunk.revision;
          const bytes=Uint8Array.from(atob(chunk.data),c=>c.charCodeAt(0));
          if(!bytes.length||bytes.length>chunkLimit||offset+bytes.length>size)throw Error('Invalid artifact image data');
          if(!offset)type=imageType(bytes);
          parts.push(bytes);offset+=bytes.length;
        }while(offset<size);
        if(entry.abort.signal.aborted)throw entry.abort.signal.reason;
        return URL.createObjectURL(new Blob(parts,{type}));
      })();
      entry.url.catch(()=>{if(entries.get(key)===entry)entries.delete(key);});
    }
    entry.count++;let released=false;
    return {url:entry.url,release(){
      if(released)return;released=true;entry.count--;
      // Markdown replacement in one render keeps the same in-flight original.
      queueMicrotask(()=>{if(entry.count)return;if(entries.get(key)===entry)entries.delete(key);entry.abort.abort();entry.url.then(url=>URL.revokeObjectURL(url)).catch(()=>{});});
    }};
  };
}
