import {copyKey,digestBlob,resourceScope} from './resource-bytes.mjs';
// A lease captures immutable manifest + complete source identity. Shared URLs
// belong to the last connected reader; persistence never owns browser URLs.
export function createDisplayResources(request,getScope,contentURL,persistence={}){
  const entries=new Map(),{copies,identity=scope=>scope,current=()=>true}=persistence;
  copies?.subscribe(event=>{if(event.kind==='clear'||event.kind==='compact'||event.kind==='retire')for(const entry of entries.values())if(event.kind==='clear'||event.kind==='compact'||entry.scope===event.scope){entry.abort.abort();for(const url of entry.urls.values())url.then(value=>URL.revokeObjectURL(value)).catch(()=>{});entry.retire?.();}});
  return function acquire(resource,options={}){
    const scope={...getScope(),...options},source=identity(scope),revision=JSON.stringify([resource.id,resource.entry,resource.files]),key=copyKey(source,'resource',revision),files=new Map(resource.files.map(file=>[file.path,file]));
    let entry=entries.get(key);
    if(!entry||entry.abort.signal.aborted){
      entry={count:0,scope:resourceScope(source),abort:new AbortController(),reads:new Map(),urls:new Map(),retireListeners:new Set()};entries.set(key,entry);
      entry.retire=()=>{for(const listener of entry.retireListeners)listener();};
      const check=()=>{if(entry.abort.signal.aborted)throw new DOMException('Resource retired','AbortError');if(!current(source))throw new DOMException('Resource source changed','AbortError');};
      entry.read=path=>{
        try{check();}catch(error){return Promise.reject(error);}
        if(entry.reads.has(path))return entry.reads.get(path);
        const pending=(async()=>{
          check();const file=files.get(path);if(!file||!Number.isSafeInteger(file.size)||file.size>16*1024*1024)throw Error('Resource preview limit');
          const fileRevision=JSON.stringify([revision,path,file.digest,file.size,file.mime_type]);
          const saved=await copies?.read(source,'bytes',fileRevision);check();
          if(saved){if(saved.value instanceof Blob&&saved.value.size===file.size&&await digestBlob(saved.value)===file.digest){check();return saved.value;}throw Error('Invalid saved resource bytes');}
          const expected=await copies?.token(source);check();
          const parts=[];let offset=0;
          do{
            const chunk=await request(`/api/v1/resources/${encodeURIComponent(resource.id)}/chunk?${new URLSearchParams({path,offset:String(offset)})}`,{...scope,expectedControl:source.control,signal:entry.abort.signal});
            check();if(chunk.size!==file.size||chunk.offset!==offset||chunk.revision!==file.digest||typeof chunk.data!=='string'||chunk.data.length>4*Math.ceil(192*1024/3))throw Error('Invalid resource chunk');
            const data=Uint8Array.from(atob(chunk.data),value=>value.charCodeAt(0));
            if(data.length>192*1024||offset+data.length>file.size||(!data.length&&offset<file.size))throw Error('Invalid resource bytes');
            parts.push(data);offset+=data.length;
          }while(offset<file.size);
          const blob=new Blob(parts,{type:file.mime_type});check();
          if(copies){if(await digestBlob(blob)!==file.digest)throw Error('Resource digest mismatch');check();await copies.commit(source,'bytes',fileRevision,blob,expected.generation);check();}
          return blob;
        })();
        entry.reads.set(path,pending);pending.catch(()=>entry.reads.delete(path));return pending;
      };
      entry.blobURL=path=>{
        try{check();}catch(error){return Promise.reject(error);}
        if(!entry.urls.has(path))entry.urls.set(path,entry.read(path).then(blob=>{check();return URL.createObjectURL(blob);}));
        return entry.urls.get(path);
      };
      entry.renderDocument=async token=>{
        if(!copies)return null;
        check();const saved=await copies.read(source,'html',revision);check();let document=saved?.value;
        if(!document){
          const expected=await copies.token(source);check();
          document=await request(`/api/v1/resources/${encodeURIComponent(resource.id)}/render-document`,{...scope,expectedControl:source.control,signal:entry.abort.signal});check();
          if(document?.version!==1||document.resource!==resource.id||document.entry!==resource.entry||JSON.stringify(document.files)!==JSON.stringify(resource.files)||typeof document.html!=='string'||document.html.length>64*1024*1024||document.token!=='pot-resource-mount-token-v1'||typeof document.digest!=='string'||await digestBlob(new Blob([document.html]))!==document.digest)throw Error('Invalid HTML display document');
          await copies.commit(source,'html',revision,document,expected.generation);check();
        }
        if(await digestBlob(new Blob([document.html]))!==document.digest)throw Error('Invalid saved HTML document');
        check();return document.html.replaceAll(document.token,token);
      };

    }
    entry.count++;let released=false;
    return {
      read:entry.read,
      // Audio/video retain streaming metadata-only behavior. Images and PDF use
      // immutable verified bytes, never an HTTP cache or a live URL fallback.
      url:path=>copies&&!/^(audio|video)\//.test(files.get(path)?.mime_type||'')?entry.blobURL(path):contentURL(resource,path,scope),
      renderURL:token=>contentURL(resource,resource.entry,scope,'render',token),renderDocument:copies?entry.renderDocument:undefined,trackDocumentURL(url){if(entry.abort.signal.aborted){URL.revokeObjectURL(url);return;}entry.urls.set(url,Promise.resolve(url));},blobURL:entry.blobURL,
      onRetire(listener){entry.retireListeners.add(listener);return()=>entry.retireListeners.delete(listener);},
      release(){if(released)return;released=true;entry.count--;queueMicrotask(()=>{if(entry.count)return;if(entries.get(key)===entry)entries.delete(key);entry.abort.abort();for(const pending of entry.urls.values())pending.then(url=>URL.revokeObjectURL(url)).catch(()=>{});entry.urls.clear();entry.retireListeners.clear();});},
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
