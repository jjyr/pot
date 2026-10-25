import {createChatCache} from '../../web/src/state/chat-cache.mjs';

// Count records returned by real IndexedDB requests, not elapsed fixture time.
export async function chatCacheReadVolume(report=()=>{}) {
  const counts={rows:0,scans:0},original=[];
  for(const prototype of [IDBObjectStore.prototype,IDBIndex.prototype])for(const method of ['get','getAll','openCursor']){
    const call=prototype[method];original.push(()=>prototype[method]=call);
    prototype[method]=function(...args){
      const result=call.apply(this,args);
      if((this.objectStore?.name||this.name)==='messages'){
        if(method!=='get')counts.scans++;
        result.addEventListener('success',()=>{counts.rows+=method==='getAll'?result.result.length:result.result?1:0;});
      }
      return result;
    };
  }
  const results=[];
  try{
    for(const size of [2048,8192]){
      const name='pot-read-volume-'+crypto.randomUUID(),cache=createChatCache({name,maxMessages:16384,maxBytes:64*1024*1024});
      const message=(i,version=1)=>({id:'m'+i,sequence:String(i),version,role:'user',text:'x'.repeat(1024),status:'complete'});
      const frame=(cursor,messages,kind='reset')=>({kind,cursor:String(cursor),base_cursor:String(cursor-1),runtime_id:'volume',owner_id:'owner',conversation_id:'chat',boot_id:'boot',running:false,turn_id:null,replies:[],messages,history:{before:'1',after:String(size),has_more:false,has_newer:false}});
      try{
        await cache.writeFrames('scope',[frame(1,Array.from({length:size},(_,i)=>message(i+1)))]);
        counts.rows=counts.scans=0;
        await cache.writeFrames('scope',[frame(2,[message(size,2)],'delta')]);
        results.push({size,delta:{...counts}});
        counts.rows=counts.scans=0;
        const page=await cache.readPage('scope',{before:String(size)});
        if(!page?.messages.length)throw Error('Historical page must remain readable');
        results.at(-1).page={...counts};
        const cold=createChatCache({name});try{counts.rows=counts.scans=0;await cold.read('scope');results.at(-1).cold={...counts};}finally{cold.close();}
        report(JSON.stringify(results.at(-1)));
      }finally{cache.close();await new Promise((resolve,reject)=>{const request=indexedDB.deleteDatabase(name);request.onsuccess=resolve;request.onerror=()=>reject(request.error);});}
    }
  }finally{for(const restore of original)restore();}
  for(const value of results){
    if(value.delta.rows!==1||value.delta.scans!==0||value.page.rows!==80||value.page.scans!==1||value.cold.rows>896)throw Error('History-sized reads: '+JSON.stringify(results));
  }
  if(results[0].delta.rows!==results[1].delta.rows)throw Error('Delta reads grow with retained history: '+JSON.stringify(results));
  report('PASS bounded chat reads');return results;
}
