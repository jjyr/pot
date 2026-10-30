import {createChatCache} from '../chat-cache.mjs';
import {createChatSync} from '../chat-sync.mjs';
import {mergeUIFrame,mergeHistoryPage} from '../chat-history.mjs';
import {retainCommittedChat} from '../retained-chat.mjs';
import {validSnapshot} from '../conversation.mjs';
import {translate} from '../../locales/messages.mjs';

// A surface consumes committed conversation queries. Storage identity, history
// coverage and transport ownership never escape this domain.
export function createChatDomain(client){
  let cache,closed=false;const sessions=new Set(),sessionKeys=new WeakMap(),historyReads=new Map();
  const storage=()=>client.production?(cache ||= createChatCache({name:client.chatCacheName||'pot-chat-cache'})):null;
  const keyFor=scope=>JSON.stringify([(client.spaces||[]).find(item=>item.id===scope.space)?.control_id||client.runtime?.runtime_id||'',scope.space||'',scope.owner||'']);
  const stored=promise=>promise.catch(error=>{if(error.name==='AbortError'||['conversation_gap','history_gap'].includes(error.message))throw error;throw Object.assign(new Error(translate(client.uiLocale?.()||'en','storage.failed'),{cause:error}),{storage:true,uiKey:'storage.failed',diagnostic:error.message});});
  function open(scope={}, {surface='main',initialSurface,context=()=>({})}={}){
    if(closed)throw new DOMException('Chat domain disposed','AbortError');
    scope={space:scope.space||'',owner:scope.owner||''};
    const key=keyFor(scope),cache=storage(),abort=new AbortController();
    let snapshot=null,displayed=null,exposed=false,observer={},sync,receive=()=>{},stopTransport=()=>{},stopCommits=()=>{},generation=0,retired=false,ended=false,writes=Promise.resolve(),closing,ownWrites=0;
    const current=()=>{if(closed||retired||ended||abort.signal.aborted)throw new DOMException('Chat session retired','AbortError');};
    const commitContext=()=>{const {anchor,position}=context();return {anchor,position,surface,window:snapshot?.messages.map(message=>({id:message.id,context_only:!!message.context_only}))};};
    const commit=work=>{ownWrites++;return stored(work()).finally(()=>ownWrites--);};
    const sameIdentity=(a,b)=>a&&b&&a.runtime_id===b.runtime_id&&a.owner_id===b.owner_id&&a.conversation_id===b.conversation_id;
    const retain=(value,{replace=false,direction='stream',presentation=value}={})=>{
      if(!sameIdentity(snapshot,value))exposed=false;
      snapshot=value;
      displayed=retainCommittedChat(displayed,presentation,{replace:replace&&!exposed,direction});
      return displayed;
    };
const deliver=async(value,received=value.messages,options={live:true})=>{
  const version=generation;
  const check=()=>{current();if(version!==generation)throw new DOMException('Delivery superseded','AbortError');};
  check();
  let presentation=value;
  if(received.length){
    let rows;
    try{
      rows=cache?await stored(cache.readMessages(key,received.map(row=>row.id),{identity:value,validate:check})):received;
      check();
    }catch(reason){
      if(reason.name==='AbortError')return;
      await observer.failed?.(reason);return;
    }
    const entries=new Map(value.messages.map(message=>[message.id,message]));
    for(const row of rows){
      if(!row)continue;
      const old=entries.get(row.id);
      if(!old||row.version>=old.version)entries.set(row.id,old&&!old.context_only?{...row,context_only:false}:row);
    }
    presentation={...value,messages:[...entries.values()].sort((a,b)=>{const x=BigInt(a.sequence||0),y=BigInt(b.sequence||0);return x<y?-1:x>y?1:0;})};
  }
  const view=retain(value,{presentation});
  check();
  await observer.snapshot?.(view,received,options);
};
    const expose=()=>{
      if(!displayed)return;
      if(displayed.messages.some(message=>!message.context_only))exposed=true;
    };
    function stop(){generation++;stopTransport();stopTransport=()=>{};stopCommits();stopCommits=()=>{};}
    const readRemote=query=>{
      const requestKey=JSON.stringify([key,query]);let flight=historyReads.get(requestKey);
      if(!flight){flight=client.history(query,scope).finally(()=>historyReads.delete(requestKey));historyReads.set(requestKey,flight);}
      return flight;
    };
    const session={
      hasHistory:typeof client.history==='function',
      async query(){current();let saved=cache?await stored(cache.read(key,surface)):null;if(cache&&!saved?.view&&initialSurface)saved=await stored(cache.read(key,initialSurface));current();if(saved)retain(saved.snapshot);return saved?{...saved,snapshot:displayed}:saved;},
      expose,
async history(query,{around=false,cachedOnly=false,signal,valid=()=>true,currentPosition=()=>true}={}){
  const version=generation;
  const check=()=>{current();if(signal?.aborted)throw signal.reason;if(version!==generation||!valid())throw new DOMException('Chat history retired','AbortError');};
  const page=async(q,replace)=>{
    check();if(!currentPosition())return null;
    // Always project pages in isolated (around) mode so bounded disk windows cannot evict freshly fetched end rows and block growth; caller replace only controls retain() positioning.
    let value=cache?await stored(cache.projectPage(key,q,{identity:{boot_id:snapshot.boot_id,conversation_id:snapshot.conversation_id},context:commitContext,around:true,currentPosition,validate:check})):null;
    check();
    if(!value&&cachedOnly)return null;
    if(!value){const remote=await readRemote(q);check();if(!currentPosition())return null;value=cache?await commit(()=>cache.writePage(key,remote,{context:commitContext,around:true,currentPosition,validate:check})):mergeHistoryPage(snapshot,remote,context().anchor,{around:true});}
    check();if(!value||!currentPosition())return null;
// pages cannot skip unknown intervals; require contiguous bridge between isolated page history and requested q range
if(q.before&&!(BigInt(value.history.before||0)<BigInt(q.before)&&BigInt(value.history.after||0)>=BigInt(q.before)-1n))throw Error('history_gap');
if(q.after&&!(BigInt(value.history.after||0)>BigInt(q.after)&&BigInt(value.history.before||0)<=BigInt(q.after)+1n))throw Error('history_gap');
    return retain(value,{replace,direction:q.before?'before':q.after?'after':'stream'});
  };
  check();
  if(!snapshot||!session.hasHistory||!currentPosition())return null;
  if(!(around&&exposed))return page(query,around);
  const targetId=query.around;
  if(targetId&&displayed?.messages.some(message=>message.id===targetId&&!message.context_only))return displayed;
  if(!targetId&&!displayed?.history?.has_newer)return displayed;
  let probe=cache?await stored(cache.readPage(key,query,{validate:check})):null;
  check();
  if(!probe){if(cachedOnly)return null;probe=await readRemote(query);check();}
  if(!currentPosition())return null;
  if(!probe||probe.boot_id!==snapshot.boot_id||probe.conversation_id!==snapshot.conversation_id)throw new DOMException('Chat history retired','AbortError');
  const match=targetId?probe.messages?.find(message=>message.id===targetId&&!message.context_only):null;
  const target=targetId?(match?.sequence==null?null:BigInt(match.sequence)):(probe.history?.after==null?null:BigInt(probe.history.after));
  if(target===null)return null;
  while(currentPosition()){
    check();
    if(targetId&&displayed?.messages.some(message=>message.id===targetId&&!message.context_only))return displayed;
    const first=displayed?.history?.before==null?null:BigInt(displayed.history.before);
    const last=displayed?.history?.after==null?null:BigInt(displayed.history.after);
    if(!targetId&&last!==null&&last>=target)return displayed;
    if(first!==null&&last!==null&&target>=first&&target<=last)throw Error('history_gap');
    const direction=first===null||target<first?'before':'after';
    const boundary=displayed?.history?.[direction];
    const merged=await page({[direction]:boundary},false);
    if(!merged)return null;
    const nextBoundary=displayed?.history?.[direction];
    if(nextBoundary==null||nextBoundary==='')throw Error('history_gap');
    if(direction==='before'){if(BigInt(nextBoundary)>=BigInt(boundary))throw Error('history_gap');}
    else {if(BigInt(nextBoundary)<=BigInt(boundary))throw Error('history_gap');}
  }
  return null;
},
      saveView(value){
        if(!snapshot||!cache||ended)return writes;
        const {position,layout,heights,tools}=value;
        const view=structuredClone({position,layout,heights,tools,conversation_id:snapshot.conversation_id,boot_id:snapshot.boot_id,cursor:snapshot.cursor,window:snapshot.messages.map(message=>({id:message.id,context_only:!!message.context_only}))});
        writes=writes.catch(()=>{}).then(()=>stored(cache.saveView(key,surface,view)));return writes;
      },
      watch(next){
        current();stop();sync?.close();observer=next;
        const connectionGeneration=client.connectionGeneration;
        const version=generation,active=()=>!closed&&!retired&&!ended&&version===generation;
        const failed=reason=>{if(active())observer.failed?.(reason);};
        if(cache)sync=createChatSync({cache:{writeFrames:(...args)=>commit(()=>cache.writeFrames(...args))},key,context:()=>!ended?commitContext():{surface},committed:(value,received)=>{if(!active())return;client.confirmSession?.(scope,connectionGeneration);return deliver(value,received);},failed});
        receive=value=>{
          if(!active())return;
          if(!validSnapshot(value)){failed(Error('Invalid conversation snapshot'));return;}
          const expected=scope.space?JSON.stringify([scope.space,scope.owner]):scope.owner;
          if(client.production&&expected&&value.owner_id!==expected){failed(Object.assign(Error('Chat identity changed'),{uiKey:'identityChanged'}));return;}
          if(cache)sync.push(value);else {try{const merged=mergeUIFrame(snapshot,value,context().anchor);return deliver(merged,value.messages).catch(failed);}catch(reason){failed(reason);}}
        };
        stopTransport=client.subscribe(receive,(status,reason)=>{if(active())observer.connection?.(status,reason);},scope);
        stopCommits=cache?.subscribe(async(changed,{remote})=>{
          if(changed!==key||!active()||!remote&&ownWrites)return;
          try{const saved=await stored(cache.read(key,surface));if(!active())return;if(saved)await deliver(saved.snapshot,saved.snapshot.messages,{live:false});else observer.removed?.();}catch(reason){failed(reason);}
        })||(()=>{});
        return ()=>{if(version===generation)stop();};
      },
      async refresh(){current();const version=generation,value=await client.snapshot({...scope,signal:abort.signal});current();if(version!==generation)return;await receive(value);await sync?.flush();},
      async retire(){if(ended)return;retired=true;stop();abort.abort();await sync?.retire();},
      close(){if(closing)return closing;ended=true;stop();abort.abort();const pending=sync?.close();closing=Promise.all([writes,pending]).finally(()=>sessions.delete(session));return closing;},
    };
    // Receiving transport frames is private to watch/refresh.
    sessions.add(session);sessionKeys.set(session,key);return session;
  }
  return {open,
    async remove(scope){const key=keyFor(scope);await Promise.all([...sessions].filter(session=>sessionKeys.get(session)===key).map(session=>session.close()));if(cache)await stored(cache.remove(key));},
    dispose(){closed=true;Promise.allSettled([...sessions].map(session=>session.close())).finally(()=>cache?.close());},
  };
}
