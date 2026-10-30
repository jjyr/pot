import {createSchedulesDomain} from '../../web/src/state/domains/schedules.mjs';
import {createQueryRegistry,createCommands} from '../../web/src/state/domains/query.mjs';

export async function schedulesDomainContract(report=()=>{}) {
  const check=(c,l)=>{if(!c)throw Error(l);report('PASS '+l);};
  const flush=async()=>{for(let i=0;i<10;i++)await new Promise(r=>setImmediate(r));};
  const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return{promise,resolve};};

  let reads=0,listReads=0,ready=true,listeners=new Set(),gate;
  const record={id:'t',name:'one'};
  const resources={
    subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{},
    state:()=>({freshness:'fresh'}),describe:()=>({control:'c'}),
    async read(){reads++;const value=structuredClone(record),g=gate;gate=null;if(g)await g.promise;return {value};},
    async query(){return {value:{items:[],total:0}};},
    async collection(_path,_scope,fn){return fn();},
  };
  const client={
    uiLocale:()=>'en',
    spaces:[{id:'s',control_id:'c',boot_id:'b'}],
    assistants:[{id:'a',space_id:'s',retained:false,offline:false,capabilities:{version:1,operations:['schedules.manage']}}],
    operationScopeReady:()=>ready,
    connectionGeneration:0,
    runtime:{runtime_id:'r',boot_id:'b'},
    request:async()=>({items:[],next_offset:null}),
    transport:async()=>{listReads++;return{items:[],next_offset:null};},
  };
  const directory={watch(listener){listeners.add(listener);listener({});return()=>listeners.delete(listener);}};
  const publish=()=>{for(const l of [...listeners])l({});};
  const registry=createQueryRegistry(resources),commands=createCommands(client);
  const domain=createSchedulesDomain(client,{resources,queries:registry,commands,directory});
  let stop;
  try{
    ready=false;const updates=[];stop=domain.watch({space:'s',owner:'a'},{},s=>updates.push(s));
    await flush();
    check(updates.at(-1)?.freshness==='retained','detail initially not ready is retained');
    const before=reads;ready=true;publish();await flush();
    check(reads===before+1&&updates.at(-1).fresh,'readiness transition rereads once');
    const after=reads;ready=true;publish();publish();await flush();
    check(reads===after,'unchanged publications no rereads');
    stop();stop=undefined;
    check(listeners.size===0,'stop detaches watch');

    ready=true;const up2=[];stop=domain.watch({space:'s',owner:'a'},{},s=>up2.push(s));
    await flush();
    const b2=reads;client.connectionGeneration++;publish();await flush();
    check(reads>b2,'authority change refreshes');
const g = deferred();
gate=g;
record.id = 'old';
const p = domain.refresh({space: 's', owner: 'a'}, {});
await flush();
ready = false;
publish();
check(!up2.at(-1).fresh, 'revocation marks stale');
ready = true;
publish();
record.id = 'new';
g.resolve();
await p;
await flush();
check(!up2.some(s => s.fresh && s.data?.value?.id === 'old'), 'stale response never fresh');
check(up2.at(-1).fresh && up2.at(-1).data.value.id === 'new', 'recovery fresh');
    stop();stop=undefined;

    ready=false;record.id='saved';
    const listUpdates=[];const stopList=domain.watchList(s=>listUpdates.push(s));
    await flush();
    check(listUpdates.at(-1)?.data?.[0]?.stale===true,'collection initially unavailable uses saved');
    const b3=listReads;ready=true;publish();await flush();
    check(listReads===b3+1,'collection ready transition reads');
    const a3=listReads;publish();await flush();
    check(listReads===a3,'collection unchanged no reads');
    client.assistants=[];publish();await flush();
    check((listUpdates.at(-1)?.data||[]).length===0,'collection membership removal clears');
    stopList();
    check(listeners.size===0,'collection stop detaches');
  }finally{stop?.();registry.dispose();commands.dispose();}
}

await schedulesDomainContract(console.log);
