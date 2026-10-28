import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {createChatDomain} from '../../web/src/state/domains/chat.mjs';

// Exercise the actual UI request functions without a browser or timers. The
// outer DOM component is replaced only at its rendering boundary; transport
// coalescing and history merging use the real Chat domain.
const source=await readFile(process.env.POT_HISTORY_INTENT_SOURCE||new URL('../../web/src/components/ui.js',import.meta.url),'utf8');
const start=source.indexOf('  function readHistory(query,options={}){');
const end=source.indexOf('  async function jumpToMessage(',start);
assert(start>=0&&end>start,'the UI history request boundary must be present');
const createUI=new Function('environment',`
  const {state,chatSession,client,currentChatPosition,onPublish,onRestore}=environment;
  let disposed=false,epoch=1,historyGeneration=0,historyRequests=new Map(),
    historyDemand=null,historyLoading=false,historyFailure='',historyLatency=200,
    chatRestore=null,historyAbort=new AbortController();
  const renderMessages=()=>{},scheduleViewSave=()=>{};
  const renderMessagesNow=()=>onPublish(state.snapshot);
  const positionChat=onRestore;
  const applySnapshot=async snapshot=>{state.snapshot=snapshot;onPublish(snapshot);};
  const cacheFailure=error=>{throw error;};
  ${source.slice(start,end)}
  return {readHistory,retire(){disposed=true;historyGeneration++;historyAbort.abort();}};
`);
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done;});return {promise,resolve};};
const message=n=>({id:'m'+n,sequence:String(n),version:1,role:'assistant',text:'committed '+n,status:'complete'});
const seed={runtime_id:'intent-runtime',owner_id:'a',conversation_id:'intent-chat',boot_id:'intent-boot',kind:'reset',base_cursor:'0',cursor:'1',running:false,turn_id:null,replies:[],messages:[message(100)],history:{before:'100',after:'100',has_more:true,has_newer:false}};
const page=n=>({boot_id:seed.boot_id,conversation_id:seed.conversation_id,cursor:'1',messages:[message(n)],history:{before:String(n),after:String(n),has_more:true,has_newer:n<100}});
async function until(test){for(let i=0;i<100;i++){if(test())return;await Promise.resolve();}throw Error('Deterministic history request did not reach its transport boundary');}
async function scenario({restore=false,latest=false,retire=false,boundary=false}={}){
  const gate=deferred(),published=[],restored=[];let calls=0,navigation=1;
  const client={production:false,runtime:{runtime_id:seed.runtime_id},onlineReady:true,subscribe:()=>()=>{},snapshot:async()=>seed,history:async()=>{calls++;return gate.promise;}};
  const state={snapshot:seed},domain=createChatDomain(client),session=domain.open({owner:'a'},{context:()=>({anchor:'100'})});
  session.watch({});await session.refresh();
  const ui=createUI({state,chatSession:session,client,currentChatPosition:ticket=>ticket.epoch===1&&ticket.generation===navigation,onPublish:snapshot=>published.push(snapshot.messages.map(message=>message.id)),onRestore:intent=>restored.push(intent)});
  const query=latest?{}:boundary?{before:'100'}:{around:'m60'},around=!boundary;
  try{
    const first=ui.readHistory(query,{around,...(!boundary?{positionTicket:{epoch:1,generation:1}}:{}),...(restore?{restoreIntent:{kind:'restore',position:{anchor:{id:'m60'}}}}:{})});
    await until(()=>calls===1);
    if(!boundary)navigation++;
    const options={around,...(!boundary?{positionTicket:{epoch:1,generation:navigation}}:{})};
    const second=ui.readHistory(query,options),duplicate=ui.readHistory(query,options);
    assert.strictEqual(second,duplicate,'the same current consumer still deduplicates');
    // Let the second consumer join the real domain's shared HTTP flight.
    for(let i=0;i<20;i++)await Promise.resolve();
    assert.equal(calls,1,'one page still uses exactly one shared network request');
    if(retire){ui.retire();await session.retire();}
    gate.resolve(page(latest?160:60));
    const [oldResult,newResult]=await Promise.all([first,second]);
    if(retire){assert.equal(newResult,false);assert.deepEqual(published,[]);}
    else if(boundary){assert.equal(oldResult,true);assert.equal(newResult,true);assert.equal(published.length,1);}
    else{
      assert.equal(oldResult,false,'the superseded position cannot publish');
      assert.equal(newResult,true,'the newer same-query positioning intention must publish');
      assert.deepEqual(published,[[latest?'m160':'m60']],'only the current around intention replaces the local window');
    }
    assert.deepEqual(restored,[],'a superseded initial restore cannot reclaim the reader');
  }finally{gate.resolve(page(60));await session.close();domain.dispose();}
}
await scenario();
await scenario({restore:true});
await scenario({latest:true});
await scenario({retire:true});
await scenario({boundary:true});
console.log('History intents: same-query re-navigation, initial-restore supersession, Latest, same-consumer dedup, shared network flight, retired session and boundary reads passed');
