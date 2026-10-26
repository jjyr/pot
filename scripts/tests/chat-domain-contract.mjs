import {createChatDomain} from '../../web/src/state/domains/chat.mjs';
const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {resolve,promise};};
const wait=async test=>{for(let i=0;i<500;i++){if(await test())return;await new Promise(done=>setTimeout(done,10));}throw Error('Chat domain contract timed out');};
export async function chatDomainContract(report=()=>{}){
  const check=(value,label)=>{if(!value)throw Error(label);report('PASS '+label);};
  const name='pot-chat-domain-'+crypto.randomUUID(),scope={space:'local',owner:'a'},owner=JSON.stringify(['local','a']);
  const message=(n,text='message '+n)=>({id:'m'+n,sequence:String(n),version:1,role:'assistant',text,status:'complete'});
  const frame=(cursor,messages=[message(cursor)])=>({runtime_id:'runtime',owner_id:owner,conversation_id:'chat',boot_id:'boot',kind:'reset',cursor:String(cursor),running:false,turn_id:null,messages,replies:[],history:{before:messages[0]?.sequence,after:messages.at(-1)?.sequence}});
  let latest=frame(10),historyCalls=0,hold,historyPage;
  const streams=[],client={production:true,chatCacheName:name,runtime:{runtime_id:'runtime',boot_id:'boot'},spaces:[{id:'local',control_id:'runtime'}],
    subscribe(receive,status,captured){const stream={receive,status,captured,active:true};streams.push(stream);status('connected');return()=>stream.active=false;},
    snapshot:async()=>latest,history:async()=>{historyCalls++;if(hold)await hold.promise;return historyPage;},
  };
  const domain=createChatDomain(client),other=createChatDomain(client),one=domain.open(scope,{surface:'one'}),two=domain.open(scope,{surface:'two'}),remote=other.open(scope,{surface:'remote'});
  const seen=[],shared=[],crossTab=[],errors=[];
  try{
    check(!('cache' in one)&&!('key' in one)&&!('receive' in one),'Chat public session exposes no cache, namespace or transport injection');
    one.watch({snapshot:async snapshot=>{const saved=await one.query();check(saved.snapshot.cursor===snapshot.cursor,'Chat stream observer sees an already committed query');seen.push(snapshot);},failed:error=>errors.push(error)});
    two.watch({snapshot:value=>shared.push(value),failed:error=>errors.push(error)});
    remote.watch({snapshot:value=>crossTab.push(value),failed:error=>errors.push(error)});
    await one.refresh();await wait(()=>shared.length&&crossTab.length);
    check(seen.length===1&&shared.at(-1).cursor==='10'&&crossTab.at(-1).cursor==='10','Same-client and cross-tab watchers consume shared committed projections');
    await one.saveView({position:{mode:'reading',anchors:[{id:'m10',offset:0}]},layout:'test',tools:[['tool',{open:true}]],heights:[['m10',80]]});
    const restored=await one.query();check(restored.view.layout==='test'&&restored.view.tools[0][0]==='tool','Chat domain restores persisted reading/disclosure hints');
    const put=IDBObjectStore.prototype.put;let injected=false;
    IDBObjectStore.prototype.put=function(...args){const result=put.apply(this,args);if(this.name==='messages'&&!injected){injected=true;this.transaction.abort();}return result;};
    latest=frame(11);try{await one.refresh();}finally{IDBObjectStore.prototype.put=put;}
    check(injected&&errors.at(-1)?.storage&&seen.length===1&&(await one.query()).snapshot.cursor==='10','Chat transaction abort publishes only a storage error and retains committed output');
    await one.refresh();await wait(()=>seen.at(-1)?.cursor==='11');check((await one.query()).snapshot.cursor==='11','Chat explicit refresh retries the failed commit');
    hold=deferred();historyPage={boot_id:'boot',conversation_id:'chat',cursor:'11',messages:[message(9)],history:{before:'9',after:'9'}};
    const first=one.history({before:'11'}).then(()=>false,error=>error.name==='AbortError'),second=two.history({before:'11'});
    await wait(()=>historyCalls===1);await one.close();hold.resolve();const result=await second;
    check(await first&&result.messages.some(value=>value.id==='m9')&&historyCalls===1,'Two surfaces share historical IO; closing one cannot cancel another or publish its late result');
    hold=null;const calls=historyCalls;await two.history({around:'m9'},{around:true});check(historyCalls===calls,'Chat history and reset restoration share committed indexed coverage');
    hold=deferred();historyPage={...historyPage,messages:[message(8)],history:{before:'8',after:'8'}};
    const late=two.history({before:'9'}).then(()=>false,error=>error.name==='AbortError');await wait(()=>historyCalls===calls+1);
    const staleReceive=streams.find(stream=>stream.captured.owner==='a').receive;await domain.remove(scope);hold.resolve();check(await late,'Deleting a conversation fences a late historical response');
    staleReceive(frame(12));const removed=domain.open(scope,{surface:'removed'});check(await removed.query()===null,'Closed stream and late history cannot resurrect a deleted cache');await removed.close();
    const different=domain.open({space:'local',owner:'b'});check(await different.query()===null,'Chat queries keep assistant scopes isolated');await different.close();
  }finally{await Promise.allSettled([one.close(),two.close(),remote.close()]);domain.dispose();other.dispose();await new Promise((resolve,reject)=>{const request=indexedDB.deleteDatabase(name);request.onsuccess=resolve;request.onerror=()=>reject(request.error);});}
  report('DONE Chat domain contract');
}
