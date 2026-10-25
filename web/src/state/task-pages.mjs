import {assistantAddress,addressKey} from './task-identity.mjs';
// Each Control supplies an ordered operator catalogue. Read only the prefix
// needed for the global page, independent of the number of assistants/tasks.
export function createTaskPages(client) {
  const cached=new Map();let generation=0;
  const read=async function({scope,q,phase='',owner='',offset=0,limit=20,signal,onProgress=()=>{}}) {
    const ready=[],spaces=[...(client.spaces||[])],epoch=generation;
    const results=await Promise.allSettled(spaces.map(async space=>{
      const assistant=owner?(client.assistants||[]).find(item=>item.space_id===space.id&&addressKey(assistantAddress(item))===owner):null;
      if(owner&&!assistant)return {space,value:{items:[],total:0,counts:{}},stale:false};
      const key=JSON.stringify([space.control_id,space.id,scope,q,phase,owner,offset,limit]);
      let page;
      try{
        let next=0,value,items=[],stale=false;const seen=new Set();
        do{
          if(signal.aborted)throw signal.reason;
          if(seen.has(next))throw Error('Invalid task pagination cursor');seen.add(next);
          const path=`/api/v1/tasks/catalog?${new URLSearchParams({scope,q,...(phase?{phase}:{}),...(assistant?{assistant_id:assistant.id}:{}),offset:next,limit:50})}`;
          value=await (client.read||client.request)(path,{space:space.id,owner:'',signal});
          const freshness=client.resources?.state(value)?.freshness;stale ||= freshness==='retained'||freshness==='stale';
          if(epoch!==generation)throw new DOMException('Task sources reset','AbortError');
          items.push(...(value.items||[]));next=value.next_offset;
        }while(next!=null&&items.length<offset+limit);
        page={space,value:{...value,items},stale};cached.delete(key);cached.set(key,page);
        while(cached.size>64)cached.delete(cached.keys().next().value);
      }catch(error){
        if(signal.aborted||error.name==='AbortError')throw error;
        if(!cached.has(key))throw Object.assign(error,{space});
        page={...cached.get(key),stale:true};
      }
      if(signal.aborted)throw signal.reason;
      ready.push(page);onProgress([...ready]);return page;
    }));
    if(signal.aborted)throw signal.reason;
    if(epoch!==generation)throw new DOMException('Task sources reset','AbortError');
    const pages=results.filter(item=>item.status==='fulfilled').map(item=>item.value),failed=results.filter(item=>item.status==='rejected');
    if(!pages.length&&failed.length)throw failed[0].reason;
    return {pages,partial:failed.length>0||pages.some(page=>page.stale)};
  };
  read.clear=()=>{generation++;cached.clear();};return read;
}
