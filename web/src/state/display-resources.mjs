// Display bytes never enter the model request. Each lease captures its owner and
// Space and releases pending reads/blob URLs when the last view disconnects.
export function createDisplayResources(request,getScope,contentURL){
  const entries=new Map();
  return function acquire(resource,options={}){
    const scope={...getScope(),...options},key=JSON.stringify([scope.space,scope.owner,resource.id]),files=new Map(resource.files.map(file=>[file.path,file]));
    let entry=entries.get(key);
    if(!entry){
      entry={count:0,abort:new AbortController(),reads:new Map(),urls:new Set()};entries.set(key,entry);
      entry.read=path=>{
        if(entry.reads.has(path))return entry.reads.get(path);
        const pending=(async()=>{
          const file=files.get(path);if(!file||!Number.isSafeInteger(file.size)||file.size>16*1024*1024)throw Error('Resource preview limit');
          const parts=[];let offset=0;
          do{
            const chunk=await request(`/api/v1/resources/${encodeURIComponent(resource.id)}/chunk?${new URLSearchParams({path,offset:String(offset)})}`,{...scope,signal:entry.abort.signal});
            if(chunk.size!==file.size||chunk.offset!==offset||chunk.revision!==file.digest||typeof chunk.data!=='string'||chunk.data.length>4*Math.ceil(192*1024/3))throw Error('Invalid resource chunk');
            const data=Uint8Array.from(atob(chunk.data),value=>value.charCodeAt(0));
            if(data.length>192*1024||offset+data.length>file.size||(!data.length&&offset<file.size))throw Error('Invalid resource bytes');
            parts.push(data);offset+=data.length;
          }while(offset<file.size);
          if(entry.abort.signal.aborted)throw entry.abort.signal.reason;
          return new Blob(parts,{type:file.mime_type});
        })();
        entry.reads.set(path,pending);pending.catch(()=>entry.reads.delete(path));return pending;
      };
    }
    entry.count++;let released=false;
    return {
      read:entry.read,
      url:path=>contentURL(resource,path,scope),
      renderURL:token=>contentURL(resource,resource.entry,scope,'render',token),
      async blobURL(path){const blob=await entry.read(path);if(entry.abort.signal.aborted)throw entry.abort.signal.reason;const url=URL.createObjectURL(blob);entry.urls.add(url);return url;},
      release(){if(released)return;released=true;entry.count--;queueMicrotask(()=>{if(entry.count)return;if(entries.get(key)===entry)entries.delete(key);entry.abort.abort();for(const url of entry.urls)URL.revokeObjectURL(url);entry.urls.clear();});},
    };
  };
}

// Historical successful presentation receipts retain their saved resource view.
// New message-file projections are bound by the completion host, not tool text.
export function resourcePresentation(message){
  if(message?.tool?.name!=='present_resource'||message.tool.phase!=='completed')return null;
  let result=message.tool.result;if(typeof result==='string'){try{result=JSON.parse(result);}catch{return null;}}
  const value=result?.presentation,resource=value?.resource;
  return resource&&typeof resource.id==='string'&&typeof resource.name==='string'&&Array.isArray(resource.files)&&['preview','download'].includes(value.mode)?value:null;
}
export function projectResourceMessages(messages){
  return messages.flatMap(message=>{
    const presentation=resourcePresentation(message);
    return presentation?[message,{id:`${message.id}:resource`,viewKey:`resource:${message.id}`,sequence:message.sequence,role:'assistant',text:'',status:'complete',phase:'complete',presentation}]:[message];
  });
}
