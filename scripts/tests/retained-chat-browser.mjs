import {createChatDomain} from '../../web/src/state/domains/chat.mjs';
import {createChatCache} from '../../web/src/state/chat-cache.mjs';

function assert(condition,label){if(!condition)throw Error(label);}

const dbName='pot-retained-'+crypto.randomUUID();
const key=JSON.stringify(['r','s','owner']);
const row=n=>({id:'m'+n,sequence:String(n),version:1,role:'user',text:'row '+n,status:'complete'});
const frame=(messages,total,over={})=>({
  kind:'reset',base_cursor:'0',runtime_id:'r',boot_id:'b',owner_id:JSON.stringify(['s','owner']),
  conversation_id:'c',cursor:'1',running:false,turn_id:null,replies:[],messages,
  history:{before:messages.length?String(messages[0].sequence):'',after:messages.length?String(messages.at(-1).sequence):'',has_more:false,has_newer:false},
  _latest:String(total),...over,
});
const normals=v=>v.messages.filter(m=>!m.context_only);
const seqs=v=>normals(v).map(m=>BigInt(m.sequence));
function contiguous(a){for(let i=1;i<a.length;i++)if(a[i]!==a[i-1]+1n)return false;return true;}

export async function runRetainedChatBrowser(){
  const cache=createChatCache({name:dbName});
    const subs=[];
  const client={
    production:true,runtime:{runtime_id:'r'},spaces:[{id:'s',control_id:'r'}],uiLocale:()=>'en',
    connectionGeneration:0,chatCacheName:dbName,
    async history(q){
      const total=1200;
      const mk=(lo,hi)=>{const m=[];for(let n=lo;n<=hi;n++)m.push(row(n));
        return frame(m,total,{history:{before:String(lo),after:String(hi),has_more:lo>1,has_newer:hi<total}});};
      const ps=80;
      if(q.around){const mm=/^m(\d+)$/.exec(String(q.around));const c=mm?Number(mm[1]):600;
        let lo=Math.max(1,Math.min(total,c-Math.floor(ps/2)));let hi=Math.min(total,lo+ps-1);
        if(hi-lo+1<ps)lo=Math.max(1,hi-ps+1);return mk(lo,hi);}
      if(q.after!=null){const lo=Number(q.after)+1;return mk(lo,Math.min(total,lo+ps-1));}
      if(q.before!=null){const hi=Number(q.before)-1;return mk(Math.max(1,hi-ps+1),hi);}
      return mk(Math.max(1,total-ps+1),total);
    },
    subscribe(cb){subs.push(cb);return()=>{const i=subs.indexOf(cb);if(i>=0)subs.splice(i,1);};},
    snapshot:async()=>{const m=[];for(let n=1200-79;n<=1200;n++)m.push(row(n));
      return frame(m,1200,{history:{before:'1121',after:'1200',has_more:true,has_newer:false}});},
    confirmSession(){},
  };

  const domain=createChatDomain(client);
  const session=domain.open({space:'s',owner:'owner'},{surface:'main',context:()=>({anchor:'50'})});
  try{
    let observed=null;
    const waiters=[];
    const waitForSnapshot=cursor=>new Promise((res,rej)=>{
      const tl=setTimeout(()=>{const i=waiters.findIndex(w=>w.cursor===cursor);if(i>=0)waiters.splice(i,1);rej(new Error('timeout '+cursor));},3000);
      const w={cursor,res:v=>{clearTimeout(tl);res(v);},rej:e=>{clearTimeout(tl);rej(e);}};
      waiters.push(w);
      if(observed&&BigInt(observed.cursor)>=BigInt(cursor)){waiters.splice(waiters.indexOf(w),1);w.res(observed);}
    });
    const failWaiters=r=>{for(const w of waiters.slice()){waiters.splice(waiters.indexOf(w),1);w.rej(r);}};
    session.watch({
      snapshot(v){observed=v;for(const w of waiters.slice()){if(BigInt(v.cursor)>=BigInt(w.cursor)){waiters.splice(waiters.indexOf(w),1);w.res(v);}}},
      failed(r){failWaiters(r);},
      connection(){},removed(){},
    });
    const receive=async v=>{for(const cb of subs.slice())await cb(v);};
    const initial=(()=>{const m=[];for(let n=401;n<=480;n++)m.push(row(n));
      return frame(m,1200,{history:{before:'401',after:'480',has_more:true,has_newer:true}});})();
    const initP=waitForSnapshot('1');
    await receive(initial);
    await initP;
    session.expose();

    const around=await session.history({around:'m1000'},{around:true});
    assert(seqs(around).includes(1000n),'around incl 1000');
    const latest=await session.history({},{around:true});
    assert(seqs(latest).includes(1200n),'latest incl 1200');
    const older=await session.history({around:'m50'},{around:true});
    assert(seqs(older).includes(50n),'older incl 50');
    const os=seqs(older);
    assert(os.length===1200,'older normals '+os.length);
    assert(contiguous(os),'older non-contiguous');
    assert(os[0]===1n,'older first');
    assert(os.at(-1)===1200n,'older last');

    const delta={...frame([],1201),kind:'delta',base_cursor:'1',cursor:'2',
      messages:[{...row(1000),version:2,text:'updated retained'},row(1201)],
      history:{before:'1000',after:'1201',has_more:true,has_newer:false}};
    const dP=waitForSnapshot('2');
    await receive(delta);
    await dP;
    assert(observed,'observed exists');
    const obs=normals(observed);
    assert(obs.length===1201,'observed normal count '+obs.length);
    assert(obs.find(m=>m.id==='m1000').text==='updated retained','row1000 updated');
    assert(String(obs.at(-1).sequence)==='1201','last 1201');

    const bounded=(await cache.read(key)).snapshot;
    assert(bounded,'bounded snapshot');
    const boundedMsgs=bounded.messages||[];
    assert(boundedMsgs.length<=640,'bounded count '+boundedMsgs.length);
    const readInitial=await cache.read(key);
    assert(readInitial&&(readInitial.snapshot.messages||[]).length<=640,'disk bounded');

    let aborted=null;
    try{await cache.readMessages(key,['m1000'],{identity:{...observed,boot_id:'wrong'}});}catch(e){aborted=e;}
    assert(aborted,'mismatch rejects');
    assert(aborted.name==='AbortError','mismatch AbortError');
    const good=await cache.readMessages(key,['m1000'],{identity:observed});
    const found=good.find(m=>m.id==='m1000');
    assert(found,'good read returns m1000');
    assert(found.version===2,'good read version 2');

    const originalGet=IDBObjectStore.prototype.get;
    try{
      IDBObjectStore.prototype.get=function(query){if(this.name==='messages' && this.transaction.mode==='readonly' && this.transaction.db.name===dbName) throw new DOMException('Synthetic committed read failed','UnknownError');return originalGet.call(this,query);};
      const failure=waitForSnapshot('3').then(()=>null,error=>error);
      await receive({...frame([{...row(1000),version:3,text:'not published'}],1201),kind:'delta',base_cursor:'2',cursor:'3'});
      const error=await failure;
      assert(error?.storage===true && error.diagnostic.includes('Synthetic committed read failed'),'committed row read failure reaches domain observer');
      assert(observed.cursor==='2','failed postcommit read does not publish unverified display');
    } finally {
      IDBObjectStore.prototype.get=originalGet;
    }

    return {passed:true,retained:obs.length,bounded:boundedMsgs.length,deliveryReadFailure:true};
  } finally {
    await session.close();
    await domain.dispose();
    await Promise.resolve();
    await cache.close();
    await new Promise((res,rej)=>{const r=indexedDB.deleteDatabase(dbName);r.onsuccess=()=>res();r.onerror=()=>rej(r.error);r.onblocked=()=>rej(new Error('blocked'));});
  }
}
