import {CHAT_CACHE_VERSION, migrateChatCache} from '../../../pkg/storage/migrations/ui-cache/0001_chat_cache.mjs';
import {mergeUIFrame, mergeHistoryPage} from './chat-history.mjs';

const stores=['conversations','messages','views'];
const sequence=message=>BigInt(message.sequence||0);
const request=value=>new Promise((resolve,reject)=>{value.onsuccess=()=>resolve(value.result);value.onerror=()=>reject(value.error);});
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
  return {...meta,schema_version:1,projection,window:messages.map(message=>({id:message.id,context_only:!!message.context_only})),
    latest:latest(meta?.latest,_latest||history?.after||'0'),updates:[...(_updates?.keys()||[])],used:Date.now()};
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
function withDependencies(selected,retained){
  const ids=new Set(selected.map(row=>row.id)),dependencies=new Map(),byID=new Map(retained.map(row=>[row.id,row]));
  const tools=new Map(retained.filter(row=>row.message.tool?.id).map(row=>[JSON.stringify([row.message.tool.turn_id,row.message.tool.id]),row]));
  const queue=[...selected],visited=new Set();
  while(queue.length){
    const row=queue.shift();if(visited.has(row.id))continue;visited.add(row.id);
    const message=row.message,parent=message.tool?.parent_id;
    const input=byID.get(message.reply_to),ancestor=parent&&tools.get(JSON.stringify([message.tool.turn_id,parent]));
    if(message.reply_to&&!input||parent&&!ancestor)return null;
    for(const entry of [input,ancestor]){
      if(!entry||visited.has(entry.id))continue;
      if(!ids.has(entry.id)&&!dependencies.has(entry.id)){if(dependencies.size===32)return null;dependencies.set(entry.id,entry);}
      queue.push(entry);
    }
  }
  return [...selected.map(row=>({...row.message,context_only:false})),...[...dependencies.values()].map(row=>({...row.message,context_only:true}))];
}

// IndexedDB is the only retained chat cache. Transactions read their own durable
// baseline; promises resolve at transaction completion, never at put success.
export function createChatCache({indexedDB=globalThis.indexedDB,name='pot-chat-cache',maxMessages=2048,maxBytes=32*1024*1024,totalBytes=256*1024*1024}={}) {
  let opening,closed=false;
  function open(){
    if(closed)return Promise.reject(Error('chat_cache_closed'));
    if(!opening)opening=new Promise((resolve,reject)=>{
      if(!indexedDB){reject(Error('chat_cache_unavailable'));return;}
      const value=indexedDB.open(name,CHAT_CACHE_VERSION);let abandoned=false;
      value.onupgradeneeded=event=>migrateChatCache(value.result,value.transaction,event.oldVersion);
      value.onsuccess=()=>{const db=value.result;if(abandoned||closed){db.close();return;}db.onversionchange=()=>{db.close();opening=null;};resolve(db);};
      value.onerror=()=>{opening=null;reject(value.error);};
      value.onblocked=()=>{abandoned=true;opening=null;reject(Error('chat_cache_blocked'));};
    }).catch(reason=>{opening=null;throw reason;});
    return opening;
  }
  async function transaction(mode,work){
    const db=await open();
    return new Promise((resolve,reject)=>{
      const tx=db.transaction(stores,mode);let result,failure;
      tx.oncomplete=()=>resolve(result);
      tx.onabort=tx.onerror=()=>reject(failure||tx.error||Error('chat_cache_transaction'));
      Promise.resolve().then(()=>work(tx)).then(value=>{result=value;},reason=>{failure=reason;try{tx.abort();}catch{reject(reason);}});
    });
  }
  const all=(tx,key)=>request(tx.objectStore('messages').index('scope').getAll(key));
  async function erase(tx,key,removeViews=true){
    const messages=tx.objectStore('messages'),views=tx.objectStore('views');
    for(const value of await request(messages.index('scope').getAllKeys(key)))messages.delete(value);
    if(removeViews)for(const value of await request(views.index('scope').getAllKeys(key)))views.delete(value);
    tx.objectStore('conversations').delete(key);
  }
  async function save(tx,key,meta,snapshot,records){
    const pinned=new Set(snapshot.messages.map(message=>message.id));
    let bytes=records.reduce((sum,row)=>sum+row.bytes,0);
    const retained=[...records];
    for(const row of [...records].sort((a,b)=>a.used-b.used)){
      if(retained.length<=maxMessages&&bytes<=maxBytes)break;
      if(pinned.has(row.id))continue;
      tx.objectStore('messages').delete([key,row.id]);retained.splice(retained.indexOf(row),1);bytes-=row.bytes;
    }
    // An evicted range must be fetched again; never claim a partially cached page.
    if(retained.length!==records.length)meta.ranges=[];
    meta=capture({...meta,key,bytes},snapshot);
    tx.objectStore('conversations').put(meta);
    const catalogue=await request(tx.objectStore('conversations').getAll());
    let total=catalogue.reduce((sum,item)=>sum+(item.bytes||0),0);
    for(const item of catalogue.sort((a,b)=>a.used-b.used)){
      if(total<=totalBytes)break;
      if(item.key===key)continue;
      await erase(tx,item.key);total-=item.bytes||0;
    }
    return meta;
  }
  function upsert(tx,key,records,messages){
    const byID=new Map(records.map(row=>[row.id,row]));
    const accepted=[];
    for(const message of messages){
      const old=byID.get(message.id);
      const current=old&&(old.message.version||0)>(message.version||0)?{...old.message,context_only:message.context_only}:message;
      const row={key,id:current.id,order:order(current),message:{...current},normal:old?.normal||!current.context_only,bytes:bodyBytes(current),used:Date.now()};
      byID.set(row.id,row);tx.objectStore('messages').put(row);accepted.push(current);
    }
    return {records:[...byID.values()],messages:accepted};
  }
  async function read(key,surface){
    return transaction('readonly',async tx=>{
      const meta=await request(tx.objectStore('conversations').get(key));
      if(!meta)return null;
      const view=surface?await request(tx.objectStore('views').get([key,surface])):null;
      const compatible=view?.conversation_id===meta.projection.conversation_id;
      const records=await all(tx,key);
      const ids=new Set(records.map(row=>row.id));
      let window=compatible&&view.boot_id===meta.projection.boot_id&&view.window?.every(item=>ids.has(item.id))?view.window:undefined;
      let snapshot=hydrate(meta,records,window);
      if(view?.position?.mode==='latest'&&snapshot.history.has_newer){
        const messages=withDependencies(records.filter(row=>row.normal).sort((a,b)=>a.order<b.order?-1:1).slice(-80),records);
        if(messages)snapshot=hydrate(meta,records,messages.map(message=>({id:message.id,context_only:!!message.context_only})));
      }
      return {snapshot,view:compatible?view:null};
    });
  }
  async function saveWindow(tx,key,surface,snapshot,position){
    if(!surface)return;
    const store=tx.objectStore('views'),previous=await request(store.get([key,surface]));
    store.put({schema_version:1,...previous,key,surface,conversation_id:snapshot.conversation_id,boot_id:snapshot.boot_id,
      cursor:snapshot.cursor,position:position||previous?.position||{mode:'latest'},window:snapshot.messages.map(message=>({id:message.id,context_only:!!message.context_only})),used:Date.now()});
  }
  async function writeFrames(key,frames,{anchor,surface,window,position}={}){
    const committed=await transaction('readwrite',async tx=>{
      let meta=await request(tx.objectStore('conversations').get(key)),records=await all(tx,key),snapshot=hydrate(meta,records,window),changed=false;
      for(const incoming of frames){
        let frame=incoming;
        if(meta?.projection&&(meta.projection.runtime_id!==frame.runtime_id||meta.projection.owner_id!==frame.owner_id))throw Error('chat_cache_identity');
        if(meta&&!sameOwner(meta.projection,frame)){
          if(frame.kind!=='reset')throw Error('chat_cache_identity');
          await erase(tx,key);meta=null;records=[];snapshot=null;
        }
        if(meta&&frame.boot_id!==meta.projection.boot_id){
          if(meta.retired_boots?.includes(frame.boot_id))continue;
          if(frame.kind!=='reset')throw Error('conversation_gap');
          const retired=[...(meta.retired_boots||[]),meta.projection.boot_id].slice(-8);
          await erase(tx,key,false);meta={retired_boots:retired};records=[];snapshot=null;
        }
        if(snapshot&&BigInt(frame.cursor)<BigInt(snapshot.cursor)||snapshot&&frame.kind==='delta'&&frame.cursor===snapshot.cursor)continue;
        const result=upsert(tx,key,records,frame.messages);records=result.records;frame={...frame,messages:result.messages};
        const previousCursor=snapshot?.cursor;
        snapshot=mergeUIFrame(snapshot,frame,anchor);
        const replies=new Map((meta?.replies||[]).map(reply=>[reply.input_id,reply]));
        for(const reply of frame.replies)replies.set(reply.input_id,reply);
        const ids=new Set(records.map(row=>row.id));
        meta={...meta,history_floor:frame.kind==='reset'&&previousCursor!==frame.cursor?frame.cursor:meta?.history_floor||'0',replies:[...replies.values()].filter(reply=>ids.has(reply.input_id)),
          ranges:ranges([...(frame.kind==='delta'||previousCursor===frame.cursor?meta?.ranges||[]:[]),
            ...(frame.kind==='reset'&&frame.history?[{before:frame.history.before,after:frame.history.after}]:frame.messages.filter(message=>!message.context_only).map(message=>({before:message.sequence,after:message.sequence})))])};
        meta=capture(meta,snapshot);changed=true;
      }
      if(!snapshot)return false;
      if(changed)await save(tx,key,meta,snapshot,records);
      await saveWindow(tx,key,surface,snapshot,position);return true;
    });
    // The renderer only receives a projection read after the write committed.
    return committed?(await read(key,surface))?.snapshot:null;
  }
  async function writePage(key,page,{anchor,around=false,surface,window,position}={}){
    const committed=await transaction('readwrite',async tx=>{
      let meta=await request(tx.objectStore('conversations').get(key));
      if(!meta||meta.projection.boot_id!==page.boot_id||meta.projection.conversation_id!==page.conversation_id)return false;
      if(BigInt(meta.projection.cursor)-BigInt(page.cursor)>256n||BigInt(page.cursor)<BigInt(meta.history_floor||0))throw Error('history_gap');
      let records=await all(tx,key);const previous=hydrate(meta,records,window);
      const result=upsert(tx,key,records,page.messages);records=result.records;
      const snapshot=mergeHistoryPage(previous,{...page,messages:result.messages},anchor,{around});
      meta.ranges=ranges([...(meta.ranges||[]),{before:page.history?.before,after:page.history?.after}]);
      await save(tx,key,meta,snapshot,records);await saveWindow(tx,key,surface,snapshot,position);return true;
    });
    return committed?(await read(key,surface))?.snapshot:null;
  }
  async function readPage(key,query){
    return transaction('readonly',async tx=>{
      const meta=await request(tx.objectStore('conversations').get(key));if(!meta)return null;
      const retained=await all(tx,key),records=retained.filter(row=>row.normal).sort((a,b)=>a.order<b.order?-1:1);
      const target=query.around?records.find(row=>row.id===query.around):null;
      if(query.around&&!target)return null;
      const pivot=query.before?BigInt(query.before)-1n:query.after?BigInt(query.after)+1n:target?sequence(target.message):BigInt(meta.latest);
      const range=meta.ranges?.find(value=>pivot>=BigInt(value.before)&&pivot<=BigInt(value.after));
      if(!range)return null;
      const available=records.filter(row=>sequence(row.message)>=BigInt(range.before)&&sequence(row.message)<=BigInt(range.after));
      let selected;
      if(query.before)selected=available.filter(row=>sequence(row.message)<BigInt(query.before)).slice(-80);
      else if(query.after)selected=available.filter(row=>sequence(row.message)>BigInt(query.after)).slice(0,80);
      else if(target){const index=available.findIndex(row=>row.id===target.id),start=Math.max(0,index-40);selected=available.slice(start,start+80);}
      else selected=available.slice(-80);
      if(!selected.length)return null;
      // Preserve the owning input and tool ancestors needed for reply projection.
      const messages=withDependencies(selected,retained);if(!messages)return null;
      return {boot_id:meta.projection.boot_id,conversation_id:meta.projection.conversation_id,cursor:meta.projection.cursor,messages,
        history:{before:selected[0].message.sequence,after:selected.at(-1).message.sequence,has_more:sequence(selected[0].message)>1n,has_newer:sequence(selected.at(-1).message)<BigInt(meta.latest)}};
    });
  }
  return {read,writeFrames,writePage,readPage,
    saveView(key,surface,value){return transaction('readwrite',async tx=>{
      const meta=await request(tx.objectStore('conversations').get(key));if(!meta||meta.projection.conversation_id!==value.conversation_id)return;
      const previous=await request(tx.objectStore('views').get([key,surface]));
      const window=previous?.boot_id===value.boot_id&&BigInt(previous.cursor||0)>BigInt(value.cursor||0)?previous.window:value.window;
      const cursor=previous?.boot_id===value.boot_id?latest(previous.cursor,value.cursor):value.cursor;
      tx.objectStore('views').put({schema_version:1,...value,cursor,window,key,surface,used:Date.now()});
    });},
    remove:key=>transaction('readwrite',tx=>erase(tx,key)),
    close(){closed=true;opening?.then(db=>db.close()).catch(()=>{});},
  };
}
