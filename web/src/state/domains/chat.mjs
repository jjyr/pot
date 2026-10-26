import {createChatCache} from '../chat-cache.mjs';
import {createChatSync} from '../chat-sync.mjs';
import {mergeUIFrame,mergeHistoryPage} from '../chat-history.mjs';
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
    let snapshot=null,observer={},sync,receive=()=>{},stopTransport=()=>{},stopCommits=()=>{},generation=0,retired=false,ended=false,writes=Promise.resolve(),closing,ownWrites=0;
    const current=()=>{if(closed||retired||ended||abort.signal.aborted)throw new DOMException('Chat session retired','AbortError');};
    const commitContext=()=>{const {anchor,position}=context();return {anchor,position,surface,window:snapshot?.messages.map(message=>({id:message.id,context_only:!!message.context_only}))};};
    const commit=work=>{ownWrites++;return stored(work()).finally(()=>ownWrites--);};
    const deliver=async(value,received=value.messages,options={live:true})=>{current();snapshot=value;await observer.snapshot?.(value,received,options);};
    function stop(){generation++;stopTransport();stopTransport=()=>{};stopCommits();stopCommits=()=>{};}
    const session={
      hasHistory:typeof client.history==='function',
      async query(){current();let saved=cache?await stored(cache.read(key,surface)):null;if(cache&&!saved?.view&&initialSurface)saved=await stored(cache.read(key,initialSurface));current();if(saved)snapshot=saved.snapshot;return saved;},
      async history(query,{around=false,signal,valid=()=>true}={}){
        const version=generation;
        const check=()=>{current();if(signal?.aborted)throw signal.reason;if(version!==generation||!valid())throw new DOMException('Chat history retired','AbortError');};
        check();if(!snapshot||!session.hasHistory)return null;
        let page=cache?await stored(cache.readPage(key,query)):null;check();
        if(!page){
          const requestKey=JSON.stringify([key,query]);let flight=historyReads.get(requestKey);
          if(!flight){flight=client.history(query,scope).finally(()=>historyReads.delete(requestKey));historyReads.set(requestKey,flight);}
          page=await flight;
        }check();
        const value=cache?await commit(()=>cache.writePage(key,page,{...commitContext(),around})):mergeHistoryPage(snapshot,page,context().anchor,{around});
        check();if(value)snapshot=value;return value;
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
