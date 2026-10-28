import {createResourceCopies,digestBlob,messageRevision,resourceScope} from './resource-bytes.mjs';
// This adapter only saves successful authorized display reads. It does not
// consume draft/outbox originals or provide request/download authorization.
const displayFile=file=>({path:file.path,mime_type:file.mime_type,size:file.size,digest:file.digest});
const displayBinding=row=>({id:row.id,message:row.message,revision:row.revision,start:row.start,end:row.end,href:row.href,label:row.label,block:row.block,state:'ready',resource:{id:row.resource.id,name:row.resource.name,mime_type:row.resource.mime_type,size:row.resource.size,entry:row.resource.entry,files:row.resource.files.map(displayFile),download:displayFile(row.resource.download)}});
export function createResourceReader(client,request,{base='',desktop=false,copies=createResourceCopies({name:client.chatCacheName||'pot-chat-cache'})}={}){
  const known=new Map(),retired=new Set(),bindingReads=new Map();
  const host=()=>JSON.stringify([desktop?'native':globalThis.location?.origin||'',base,client.runtime.runtime_id||client.runtime.id]);
  const identity=options=>{
    const scope={...client.scope(),...options},control=scope.expectedControl||(client.spaces||[]).find(space=>space.id===scope.space)?.control_id||client.runtime.runtime_id||client.runtime.id;
    const value={host:host(),control,space:scope.space,owner:scope.owner,linked:!!client.assistants?.find(item=>item.id===scope.owner&&item.space_id===scope.space)?.origin};const previous=known.get(resourceScope(value));value.linked||=previous?.linked||false;known.delete(resourceScope(value));known.set(resourceScope(value),value);if(known.size>1024)known.delete(known.keys().next().value);return value;
  };
  const sameSource=source=>identity(source).host===source.host&&((client.spaces||[]).find(space=>space.id===source.space)?.control_id||client.runtime.runtime_id||client.runtime.id)===source.control;
  const current=source=>!retired.has(resourceScope(source))&&sameSource(source);
  copies.subscribe(event=>{if(event.kind==='compact')retired.clear();if(event.kind==='retire')retired.add(event.scope);if(event.kind==='authorized')retired.delete(event.scope);if(event.kind==='clear')for(const key of known.keys())retired.add(key);});
  const check=source=>{if(!current(source))throw new DOMException('Resource source changed','NotAllowedError');};
  const retire=async(source,authority)=>{if(!authority)retired.add(resourceScope(source));await copies.retire(source,authority);};
  async function authorized(path,options,source,reconfirm=false){if(reconfirm?!sameSource(source):!current(source))throw new DOMException('Resource source changed','NotAllowedError');try{const value=await request(path,{...options,expectedControl:source.control});if(reconfirm?!sameSource(source):!current(source))throw new DOMException('Resource source changed','NotAllowedError');return value;}catch(error){if([401,403].includes(error.status)){await retire(source);}throw error;}}
  return {copies,identity,current,retire,read:(path,options)=>authorized(path,options,identity(options)),
    // Called only for transport-confirmed directory responses, never retained
    // display projections. An absent owner or replaced Control retires copies.
    async observe(path,value,scope,{isCurrent=()=>true,signal}={}){
      if(!['/api/v1/spaces','/api/v1/assistants'].includes(path))return;
      const rows=Array.isArray(value)?value:value?.items;
      if(!Array.isArray(rows))return;
      const sources=new Map((await copies.sources()).map(source=>[resourceScope(source),source]));for(const [key,source]of known)sources.set(key,source);
      for(const source of sources.values()){
        if(!isCurrent())throw new DOMException('Directory read retired','AbortError');
        if(source.host!==host())continue;
        if(path==='/api/v1/spaces'&&!rows.some(row=>row.id===source.space&&row.control_id===source.control))await retire(source,{isCurrent,signal});
        if(path==='/api/v1/assistants'&&scope.space===source.space&&!source.linked&&!rows.some(row=>row.id===source.owner))await retire(source,{isCurrent,signal});
      }
    },
    async bindings(message,options={}){
      const source=identity(options);
      // Text identity is separate from the server's immutable reference revision.
      // Without the caller's message text there is no offline binding lookup.
      const revision=options.sourceText===undefined?null:await messageRevision(options.sourceText),key=JSON.stringify([options.task||'',message,revision]);
      const expected=await copies.token(source);
      const load=async()=>{
        const rows=await authorized(`/api/v1/message-files/${encodeURIComponent(message)}?${new URLSearchParams({task_id:options.task||''})}`,options,source,expected.retired);
        const ready=rows.filter(row=>row.state==='ready'&&row.resource&&row.message===message&&row.revision===revision).map(displayBinding);
        if(revision&&ready.length){if(expected.retired){await copies.authorize(source,expected.generation);retired.delete(resourceScope(source));}await copies.commit(source,'bindings',key,ready,expected.generation);check(source);}
        return revision?rows.filter(row=>row.revision===revision):rows;
      };
      const saved=revision&&!expected.retired?await copies.read(source,'bindings',key):null;if(!expected.retired)check(source);
      const pending=()=>{const shared=JSON.stringify([resourceScope(source),key]);if(!bindingReads.has(shared)){const promise=load().finally(()=>bindingReads.delete(shared));bindingReads.set(shared,promise);}return bindingReads.get(shared);};
      if(saved){pending().catch(()=>{});return saved.value;}
      return pending();
    },
    async attachment(attachment,options={}){
      const source=identity(options),revision=JSON.stringify([attachment.id,attachment.size,attachment.mime_type]),expected=await copies.token(source);
      const saved=expected.retired?null:await copies.read(source,'attachment',revision);if(!expected.retired)check(source);
      if(saved){if(!(saved.value instanceof Blob)||saved.value.size!==attachment.size||await digestBlob(saved.value)!==saved.digest)throw Error('Invalid saved attachment');check(source);return saved.value;}
      if(!Number.isSafeInteger(attachment.size)||attachment.size<=0||attachment.size>8*1024*1024)throw Error('Attachment preview limit');
      const parts=[];let offset=0;
      while(offset<attachment.size){
        const chunk=await authorized(`/api/v1/attachments/${encodeURIComponent(attachment.id)}?offset=${offset}`,options,source,expected.retired);
        if(chunk.offset!==offset||chunk.size!==attachment.size||chunk.revision!==attachment.id||typeof chunk.data!=='string'||chunk.data.length>4*Math.ceil(192*1024/3))throw Error('Attachment read changed');
        const bytes=Uint8Array.from(atob(chunk.data),c=>c.charCodeAt(0));
        if(!bytes.length||bytes.length>192*1024||offset+bytes.length>attachment.size)throw Error('Invalid attachment chunk');parts.push(bytes);offset+=bytes.length;
      }
      const blob=new Blob(parts,{type:attachment.mime_type});if(expected.retired){await copies.authorize(source,expected.generation);retired.delete(resourceScope(source));}check(source);await copies.commit(source,'attachment',revision,blob,expected.generation);check(source);return blob;
    },
  };
}
