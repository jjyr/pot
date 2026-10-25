import {bindCacheLifecycle} from './cache-lifecycle.mjs';
import {createIndexedStore,idbRequest as request,indexedRows} from './indexed-store.mjs';
import {mergeUIFrame,mergeHistoryPage} from './chat-history.mjs';

const sequence=message=>BigInt(message.sequence||0);
const order=message=>String(message.sequence||0).padStart(24,'0');
const bodyBytes=message=>new TextEncoder().encode(JSON.stringify(message)).length+256;
const latest=(a='0',b='0')=>BigInt(a)>BigInt(b)?a:b;
const sameOwner=(a,b)=>a.runtime_id===b.runtime_id&&a.owner_id===b.owner_id&&a.conversation_id===b.conversation_id;

function hydrate(meta, records, window=meta?.window) {
  if(!meta)return null;
  const byID=new Map(records.map(row=>[row.id,row]));
  const messages=(window||[]).flatMap(item=>{const row=byID.get(item.id);return row?[{...row.message,context_only:!!item.context_only}]:[];});
  const normal=messages.filter(message=>!message.context_only),ids=new Set(messages.map(message=>message.id));
  return {...meta.projection,kind:'reset',base_cursor:0,messages,
    replies:meta.replies.filter(reply=>ids.has(reply.input_id)),
    history:{before:normal[0]?.sequence||'',after:normal.at(-1)?.sequence||'',has_more:sequence(normal[0]||{})>1n,has_newer:sequence(normal.at(-1)||{})<BigInt(meta.latest)},
    _latest:meta.latest,_updates:new Map((meta.updates||[]).flatMap(id=>byID.has(id)?[[id,byID.get(id).message]]:[]))};
}
function capture(meta, snapshot) {
  const {messages,history,_updates,_latest,raw,...projection}=snapshot;
  return {...meta,schema_version:2,projection,window:messages.map(message=>({id:message.id,context_only:!!message.context_only})),
    latest:latest(meta?.latest,_latest||history?.after||'0'),updates:[...(_updates?.keys()||[])],used:Date.now()};
}
// Event coverage can develop holes when message bodies are evicted. Keep its
// metadata bounded too; dropping coverage only requires a conservative backfill.
function evictedRanges(values,at){
  const kept=values.flatMap(range=>{const first=BigInt(range.before),last=BigInt(range.after);if(at<first||at>last)return [range];return [...(at>first?[{before:String(first),after:String(at-1n)}]:[]),...(at<last?[{before:String(at+1n),after:String(last)}]:[])];});
  return kept.length>128?[...kept.slice(0,64),...kept.slice(-64)]:kept;
}
function ranges(values) {
  const result=[];
  for(const value of values.filter(value=>value.before&&value.after).sort((a,b)=>BigInt(a.before)<BigInt(b.before)?-1:1)){
    const previous=result.at(-1);
    if(previous&&BigInt(value.before)<=BigInt(previous.after)+1n)previous.after=latest(previous.after,value.after);
    else result.push({...value});
  }
  return result;
}
const chatStores=['conversations','messages','views','cacheStats'];
const bounds=(key,before='0',after='999999999999999999999999')=>IDBKeyRange.bound([key,String(before).padStart(24,'0')],[key,String(after).padStart(24,'0')]);
const windowOf=snapshot=>snapshot.messages.map(message=>({id:message.id,context_only:!!message.context_only}));
const sameWindow=(a,b)=>a?.length===b?.length&&a.every((item,index)=>item.id===b[index].id&&!!item.context_only===!!b[index].context_only);

// A single active, committed projection accelerates reads; IndexedDB remains the
// baseline for every transaction and cold window. No inactive transcript cache.
export function createChatCache({indexedDB=globalThis.indexedDB,name='pot-chat-cache',maxMessages=2048,maxBytes=32*1024*1024,totalBytes=256*1024*1024,viewTTL=30*86400000,maxViews=32,checkpoints=false}={}){
  const db=createIndexedStore({indexedDB,name}),listeners=new Set();let hot=null,closed=false;
  const stopLifecycle=bindCacheLifecycle(()=>{closed=true;hot=null;db.close();});
  const channel=globalThis.BroadcastChannel?new BroadcastChannel('pot-chat-commits:'+name):null;
  const notify=(key,remote=false)=>{if(closed)return;for(const listener of listeners)listener(key,{remote});if(!remote)channel?.postMessage(key);};
  if(channel)channel.onmessage=event=>{hot=null;notify(event.data,true);};
  const transaction=(mode,work,stores=checkpoints?[...chatStores,'taskCheckpoints']:chatStores)=>db.transaction(stores,mode,work);
  async function records(tx,key,ids){
    return (await Promise.all([...new Set(ids)].map(id=>request(tx.objectStore('messages').get([key,id]))))).filter(Boolean);
  }
  async function projection(tx,key,meta,window=meta?.window){
    if(!meta?.projection)return null;
    if(hot?.key===key&&hot.revision===meta.revision&&sameWindow(window,windowOf(hot.snapshot)))return hot.snapshot;
    return hydrate(meta,await records(tx,key,[...(window||[]).map(item=>item.id),...(meta.updates||[])]),window);
  }
  async function dependencies(tx,key,selected){
    const ids=new Set(selected.map(row=>row.id)),extra=new Map(),loaded=new Map(selected.map(row=>[row.id,row])),queue=[...selected],visited=new Set();
    const store=tx.objectStore('messages');
    while(queue.length){
      const row=queue.shift();if(visited.has(row.id))continue;visited.add(row.id);
      const message=row.message,parent=message.tool?.parent_id;
      const input=message.reply_to?(loaded.get(message.reply_to)||await request(store.get([key,message.reply_to]))):null;
      const ancestor=parent?await request(store.index('tool').get([key,message.tool.turn_id,parent])):null;
      if(message.reply_to&&!input||parent&&!ancestor)return null;
      for(const entry of [input,ancestor]){if(!entry||visited.has(entry.id))continue;loaded.set(entry.id,entry);if(!ids.has(entry.id)&&!extra.has(entry.id)){if(extra.size===32)return null;extra.set(entry.id,entry);}queue.push(entry);}
    }
    return [...selected.map(row=>({...row.message,context_only:false})),...[...extra.values()].map(row=>({...row.message,context_only:true}))];
  }
  async function erase(tx,key,removeViews=true){
    const catalogue=tx.objectStore('conversations'),meta=await request(catalogue.get(key)),stats=await request(tx.objectStore('cacheStats').get('chat'))||{key:'chat',bytes:0};
    tx.objectStore('messages').delete(IDBKeyRange.bound([key,''],[key,[]]));
    if(removeViews)tx.objectStore('views').delete(IDBKeyRange.bound([key,''],[key,[]]));
    catalogue.delete(key);if(checkpoints)tx.objectStore('taskCheckpoints').delete(key);tx.objectStore('cacheStats').put({...stats,bytes:Math.max(0,stats.bytes-(meta?.bytes||0))});
  }
  async function upsert(tx,key,meta,messages){
    const store=tx.objectStore('messages'),oldRows=await records(tx,key,messages.map(message=>message.id)),byID=new Map(oldRows.map(row=>[row.id,row])),accepted=[];
    meta.count||=0;meta.bytes||=0;
    for(const message of messages){
      const old=byID.get(message.id),current=old&&(old.message.version||0)>(message.version||0)?{...old.message,context_only:message.context_only}:message;
      const row={key,id:current.id,order:order(current),message:current,normal:old?.normal||!current.context_only,bytes:bodyBytes(current),used:Date.now()};
      store.put(row);meta.count+=old?0:1;meta.bytes+=row.bytes-(old?.bytes||0);byID.set(row.id,row);accepted.push(current);
    }
    return accepted;
  }
  async function save(tx,key,meta,snapshot){
    const store=tx.objectStore('messages'),pinned=new Set(snapshot.messages.map(message=>message.id));
    if(meta.count>maxMessages||meta.bytes>maxBytes){
      await new Promise((resolve,reject)=>{const op=store.index('used').openCursor(IDBKeyRange.bound([key,0],[key,Infinity]));op.onerror=()=>reject(op.error);op.onsuccess=()=>{const row=op.result;if(!row||meta.count<=maxMessages&&meta.bytes<=maxBytes){resolve();return;}if(!pinned.has(row.value.id)){meta.count--;meta.bytes-=row.value.bytes;meta.ranges=evictedRanges(meta.ranges||[],BigInt(row.value.message.sequence));meta.replies=meta.replies.filter(reply=>reply.input_id!==row.value.id);row.delete();}row.continue();};});
    }
    if(meta.count>maxMessages||meta.bytes>maxBytes||meta.bytes>totalBytes)throw Error('chat_cache_capacity');
    const catalogue=tx.objectStore('conversations'),old=await request(catalogue.get(key)),stats=await request(tx.objectStore('cacheStats').get('chat'))||{key:'chat',bytes:0};
    meta={...capture({...meta,key},snapshot),revision:(old?.revision||0)+1};catalogue.put(meta);
    stats.bytes+=meta.bytes-(old?.bytes||0);tx.objectStore('cacheStats').put(stats);
    if(stats.bytes>totalBytes){
      // The index walks only eviction victims; no per-frame catalogue/body scan.
      while(stats.bytes>totalBytes){const victims=(await indexedRows(catalogue.index('used'),null,{limit:64})).filter(victim=>victim.key!==key);if(!victims.length)break;for(const victim of victims){if(stats.bytes<=totalBytes)break;await erase(tx,victim.key);stats.bytes-=victim.bytes;}}
    }
    return meta;
  }
  async function saveWindow(tx,key,surface,snapshot,position){
    if(!surface)return;
    const store=tx.objectStore('views'),previous=await request(store.get([key,surface]));
    store.put({schema_version:1,...previous,key,surface,conversation_id:snapshot.conversation_id,boot_id:snapshot.boot_id,cursor:snapshot.cursor,position:position||previous?.position||{mode:'latest'},window:windowOf(snapshot),used:Date.now()});
  }
  function committed(key,result){
    if(!result)return null;
    hot={key,revision:result.meta.revision,snapshot:result.snapshot};notify(key);return result.snapshot;
  }
  async function read(key,surface){
    return transaction('readonly',async tx=>{
      const meta=await request(tx.objectStore('conversations').get(key));if(!meta)return null;
      const saved=surface?await request(tx.objectStore('views').get([key,surface])):null;
      const view=saved?.conversation_id===meta.projection.conversation_id&&saved.used>Date.now()-viewTTL?saved:null;
      let window=view?.boot_id===meta.projection.boot_id?view.window:meta.window;
      let snapshot=await projection(tx,key,meta,window);
      if(snapshot.messages.length!==(window?.length||0)){window=meta.window;snapshot=await projection(tx,key,meta,window);}
      if(view?.position?.mode==='latest'&&snapshot.history.has_newer){
        const selected=await indexedRows(tx.objectStore('messages').index('sequence'),bounds(key),{direction:'prev',accept:row=>row.normal});
        const messages=await dependencies(tx,key,selected.reverse());
        if(messages)snapshot=hydrate(meta,messages.map(message=>({id:message.id,message})),windowOf({messages}));
      }
      return {snapshot,view,checkpoint:checkpoints?await request(tx.objectStore('taskCheckpoints').get(key)):null};
    });
  }
  async function writeFrames(key,frames,{anchor,surface,window,position,checkpoint,historyRange}={}){
    const result=await transaction('readwrite',async tx=>{
      if(await request(tx.objectStore('cacheStats').get('retired:'+key)))return null;
      let meta=await request(tx.objectStore('conversations').get(key)),snapshot=await projection(tx,key,meta,window||meta?.window),changed=false;
      for(let frame of frames){
        if(meta?.projection&&(meta.projection.runtime_id!==frame.runtime_id||meta.projection.owner_id!==frame.owner_id))throw Error('chat_cache_identity');
        if(meta?.projection&&!sameOwner(meta.projection,frame)){
          if(meta.retired_conversations?.includes(frame.conversation_id))continue;
          if(frame.kind!=='reset')throw Error('chat_cache_identity');const retired=[...(meta.retired_conversations||[]),meta.projection.conversation_id].slice(-32),boots=meta.retired_boots;await erase(tx,key);meta={retired_conversations:retired,retired_boots:boots};snapshot=null;
        }
        if(meta?.projection&&frame.boot_id!==meta.projection.boot_id){
          if(meta.retired_boots?.includes(frame.boot_id))continue;
          if(frame.kind!=='reset')throw Error('conversation_gap');
          const retired=[...(meta.retired_boots||[]),meta.projection.boot_id].slice(-8);await erase(tx,key,false);meta={retired_boots:retired,retired_conversations:meta.retired_conversations};snapshot=null;
        }
        if(snapshot&&BigInt(frame.cursor)<BigInt(snapshot.cursor)||snapshot&&frame.kind==='delta'&&frame.cursor===snapshot.cursor)continue;
        meta||={};frame={...frame,messages:await upsert(tx,key,meta,frame.messages)};
        const previousCursor=snapshot?.cursor;snapshot=mergeUIFrame(snapshot,frame,anchor);
        const replies=new Map((meta.replies||[]).map(reply=>[reply.input_id,reply]));for(const reply of frame.replies)replies.set(reply.input_id,reply);
        meta={...meta,history_floor:frame.kind==='reset'&&previousCursor!==frame.cursor?frame.cursor:meta.history_floor||'0',replies:[...replies.values()].slice(-maxMessages),ranges:ranges([...(frame.kind==='delta'||previousCursor===frame.cursor?meta.ranges||[]:[]),...(frame.kind==='reset'&&frame.history?[{before:frame.history.before,after:frame.history.after}]:frame.messages.filter(message=>!message.context_only).map(message=>({before:message.sequence,after:message.sequence})))])};
        if(historyRange)meta.ranges=ranges([...(meta.ranges||[]),historyRange]);
        meta=capture(meta,snapshot);changed=true;
      }
      if(!snapshot)return null;if(changed){meta=await save(tx,key,meta,snapshot);if(checkpoints&&checkpoint)tx.objectStore('taskCheckpoints').put({schema_version:1,...checkpoint,key,cursor:snapshot.cursor,conversation_id:snapshot.conversation_id});}await saveWindow(tx,key,surface,snapshot,position);return {meta,snapshot};
    });
    // The transaction's projection becomes query-visible only after complete.
    return committed(key,result);
  }
  async function writePage(key,page,{anchor,around=false,surface,window,position}={}){
    const result=await transaction('readwrite',async tx=>{
      let meta=await request(tx.objectStore('conversations').get(key));if(!meta||meta.projection.boot_id!==page.boot_id||meta.projection.conversation_id!==page.conversation_id)return null;
      if(BigInt(meta.projection.cursor)-BigInt(page.cursor)>256n||BigInt(page.cursor)<BigInt(meta.history_floor||0))throw Error('history_gap');
      const previous=await projection(tx,key,meta,window||meta.window),messages=await upsert(tx,key,meta,page.messages);
      const snapshot=mergeHistoryPage(previous,{...page,messages},anchor,{around});meta.ranges=ranges([...(meta.ranges||[]),{before:page.history?.before,after:page.history?.after}]);
      meta=await save(tx,key,meta,snapshot);await saveWindow(tx,key,surface,snapshot,position);return {meta,snapshot};
    });return committed(key,result);
  }
  async function writeRecords(key,messages,history,{conversation,signal}={}){
    const result=await transaction('readwrite',async tx=>{
      if(signal?.aborted)throw signal.reason;
      let meta=await request(tx.objectStore('conversations').get(key));if(!meta)return null;
      if(conversation&&meta.projection.conversation_id!==conversation)throw new DOMException('Task run changed','AbortError');
      const snapshot=await projection(tx,key,meta);await upsert(tx,key,meta,messages);if(signal?.aborted)throw signal.reason;
      meta.ranges=ranges([...(meta.ranges||[]),history]);meta=await save(tx,key,meta,snapshot);if(signal?.aborted)throw signal.reason;return {meta,snapshot};
    });return committed(key,result);
  }
  async function readPage(key,query){
    return transaction('readonly',async tx=>{
      const meta=await request(tx.objectStore('conversations').get(key));if(!meta)return null;
      const store=tx.objectStore('messages'),index=store.index('sequence'),target=query.around?await request(store.get([key,query.around])):null;if(query.around&&!target)return null;
      const pivot=query.before?BigInt(query.before)-1n:query.after?BigInt(query.after)+1n:target?sequence(target.message):BigInt(meta.latest);
      const range=meta.ranges?.find(value=>pivot>=BigInt(value.before)&&pivot<=BigInt(value.after));if(!range)return null;
      const limit=Math.max(1,Math.min(640,query.limit||80)),options={accept:row=>row.normal,limit};let selected;
      if(query.before)selected=(await indexedRows(index,bounds(key,range.before,pivot),{...options,direction:'prev'})).reverse();
      else if(query.after)selected=await indexedRows(index,bounds(key,pivot,range.after),options);
      else if(target){const earlier=(await indexedRows(index,bounds(key,range.before,BigInt(target.message.sequence)-1n),{...options,direction:'prev',limit:Math.floor(limit/2)})).reverse();const newer=await indexedRows(index,bounds(key,target.message.sequence,range.after),{...options,limit:limit-earlier.length});selected=[...earlier,...newer];}
      else selected=(await indexedRows(index,bounds(key,range.before,range.after),{...options,direction:'prev'})).reverse();
      if(!selected.length)return null;const messages=await dependencies(tx,key,selected);if(!messages)return null;
      return {boot_id:meta.projection.boot_id,conversation_id:meta.projection.conversation_id,cursor:meta.projection.cursor,messages,...(checkpoints?{checkpoint:await request(tx.objectStore('taskCheckpoints').get(key))}:{}),history:{before:selected[0].message.sequence,after:selected.at(-1).message.sequence,has_more:sequence(selected[0].message)>1n,has_newer:sequence(selected.at(-1).message)<BigInt(meta.latest)}};
    });
  }
  return {read,writeFrames,writePage,writeRecords,readPage,readMessages:(key,ids)=>transaction('readonly',async tx=>(await records(tx,key,ids)).map(row=>row.message)),subscribe(listener){listeners.add(listener);return ()=>listeners.delete(listener);},
    async saveView(key,surface,value){await transaction('readwrite',async tx=>{
      const meta=await request(tx.objectStore('conversations').get(key));if(!meta||meta.projection.conversation_id!==value.conversation_id)return;
      const store=tx.objectStore('views'),previous=await request(store.get([key,surface]));
      const window=previous?.boot_id===value.boot_id&&BigInt(previous.cursor||0)>BigInt(value.cursor||0)?previous.window:value.window;
      const cursor=previous?.boot_id===value.boot_id?latest(previous.cursor,value.cursor):value.cursor;
      store.put({schema_version:1,...value,cursor,window,key,surface,used:Date.now()});
      // Surface cleanup belongs to position saves, never the streaming hot path.
      const count=await request(store.index('scope').count(key));if(count>maxViews){const rows=await indexedRows(store.index('scopeUsed'),IDBKeyRange.bound([key,0],[key,Infinity]),{limit:count-maxViews+1});let remaining=count-maxViews;for(const item of rows){if(!remaining)break;if(item.surface!==surface){store.delete([key,item.surface]);remaining--;}}}
      const expired=await indexedRows(store.index('used'),IDBKeyRange.upperBound(Date.now()-viewTTL),{limit:64});for(const item of expired)store.delete([item.key,item.surface]);
    },['conversations','views']);},
    async remove(key){await transaction('readwrite',async tx=>{await erase(tx,key);tx.objectStore('cacheStats').put({key:'retired:'+key,used:Date.now()});});hot=null;notify(key);},
    close(){stopLifecycle();closed=true;hot=null;listeners.clear();channel?.close();db.close();},
  };
}
