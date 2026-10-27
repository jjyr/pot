import assert from 'node:assert/strict';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import {createChatOutbox} from '../../web/src/state/chat-outbox.mjs';
import {outboxTarget,outboxIdentityKey} from '../../web/src/state/chat-outbox-store.mjs';
import {projectOutboxRows} from '../../web/src/state/chat-outbox-projection.mjs';

const clone=structuredClone,scope={space:'space',owner:'a'},caps={version:1,operations:['chat.send','chat.attachments']};
const snapshot=(owner='a',extra={})=>({runtime_id:'host',boot_id:'boot',owner_id:JSON.stringify(['space',owner]),conversation_id:'chat-'+owner,cursor:'0',running:false,turn_id:null,replies:[],messages:[],...extra});
function fixture(){
  const rows=[],accepted=new Map(),trace=[];let mode='normal',next=1,hold;
  const client={runtime:{runtime_id:'host'},spaces:[{id:'space',control_id:'control'}],assistants:['a','b'].map(id=>({id,space_id:'space',capabilities:clone(caps)})),onlineReady:false,operationsReady:true,connectionGeneration:1,directoryGeneration:1,
    operationScopeReady:()=>true,snapshot:async scope=>snapshot(scope.owner),confirmSession(){},
    async send(request,scope){trace.push({kind:'send',request:clone(request),scope:{space:scope.space,owner:scope.owner}});if(hold)await hold;
      if(mode==='before'){mode='normal';throw Error('Disconnected before acceptance');}
      const key=JSON.stringify([scope.space,scope.owner,request.request_id]),duplicate=accepted.has(key);accepted.set(key,clone(request));
      if(mode==='after'){mode='normal';throw Error('Accepted but response lost');}
      if(mode==='forbidden')throw Object.assign(Error('Permission revoked'),{status:403});
      return {message_id:request.request_id,duplicate};},
    async sendReceipt(request,scope){trace.push({kind:'receipt',request:clone(request)});return {...request,runtime_id:'control',owner_id:scope.owner,state:accepted.has(JSON.stringify([scope.space,scope.owner,request.request_id]))?'accepted':'unconfirmed',message_id:request.request_id};},
  };
  const storage={
    async list(){return clone(rows);},async files(){return [];},async releaseFiles(expected){const value=rows.find(row=>row.sequence===expected.sequence);if(value?.state==='accepted'&&value.target.key===expected.target.key&&value.request.request_id===expected.request.request_id){value.projection_pending=false;value.revision++;return clone(value);}return null;},
    async claim(sequence,token,now,lease){const value=rows.find(row=>row.sequence===sequence);if(!value||['failed','accepted','cancelled'].includes(value.state)||value.claim?.until>now||rows.some(row=>row.target.key===value.target.key&&row.sequence<sequence&&!['accepted','cancelled'].includes(row.state)))return null;value.claim={token,until:now+lease};value.state=value.started?'uncertain':'delivering';value.revision++;return clone(value);},
    async update(sequence,token,patch){const value=rows.find(row=>row.sequence===sequence);if(value?.claim?.token!==token)return null;Object.assign(value,clone(patch));value.revision++;return clone(value);},close(){},
  };
  const clock={setTimeout,clearTimeout,setInterval:()=>1,clearInterval(){}};
  const worker=(options={})=>createChatOutbox({client,storage,clock,...options});
  const add=(id,owner='a',extra={})=>{const captured=snapshot(owner);const row={sequence:next++,revision:1,state:'queued',started:false,claim:null,target:outboxTarget(client,{space:'space',owner},captured),request:{request_id:id,conversation_id:captured.conversation_id,text:'Message '+id,...extra}};rows.push(row);return row;};
  return {client,storage,rows,trace,accepted,add,worker,setMode(value){mode=value;},setHold(value){hold=value;}};
}
let checks=0;const check=(value,label)=>{assert(value,label);checks++;console.log('PASS '+label);};
{
 const f=fixture();f.add('a1');f.add('a2');f.add('b1','b');let w=f.worker();try{
  await w.wake();check(f.trace.length===0,'Offline explicit queue does not send');
  f.client.onlineReady=true;await w.wake();check(f.rows[0].state==='accepted'&&f.rows[1].state==='queued'&&f.rows[2].state==='accepted','One FIFO head per target, independent assistants progress');
  await w.wake();check(f.trace.filter(row=>row.kind==='send'&&row.scope.owner==='a').map(row=>row.request.request_id).join(',')==='a1,a2','Recovery sends one assistant FIFO');
 }finally{w.close();}
}
for(const field of ['runtime_id','owner_id','conversation_id','request_id','user_message_id','retry_user_message_id']){
 const f=fixture(),row=f.add('receipt-fence');row.started=true;row.state='uncertain';f.client.onlineReady=true;
 f.client.sendReceipt=async()=>({...row.request,runtime_id:'control',owner_id:'a',state:'accepted',message_id:'receipt-message',[field]:'different-identity'});
 const w=f.worker();try{await w.wake();check(row.state==='failed'&&row.problem==='identityChanged'&&!f.trace.some(entry=>entry.kind==='send'),'Changed receipt '+field+' cannot accept, release originals or POST');}finally{w.close();}
}
for(const mode of ['after','before']){
 const f=fixture();f.add('stable');f.setMode(mode);f.client.onlineReady=true;let w=f.worker();try{
  await w.wake();check(f.rows[0].state==='uncertain','Lost response remains uncertain: '+mode);w.close();w=f.worker();await w.wake();
  check(f.rows[0].state==='accepted'&&f.accepted.size===1,'Reopening reconciles durable identity: '+mode);
  const sends=f.trace.filter(row=>row.kind==='send');check(sends.length===(mode==='after'?1:2)&&sends.every(row=>row.request.request_id==='stable')&&f.trace[1].kind==='receipt','Receipt precedes an identical-ID retry: '+mode);
 }finally{w.close();}
}
{
 const f=fixture();f.add('shared');f.client.onlineReady=true;let release;f.setHold(new Promise(resolve=>release=resolve));const one=f.worker(),two=f.worker();try{const first=one.wake();await new Promise(setImmediate);await two.wake();release();await first;check(f.trace.filter(row=>row.kind==='send').length===1,'Concurrent dispatcher claim prevents duplicate POST');}finally{one.close();two.close();}
}
for(const [name,change,problem]of [
 ['Control change',f=>f.client.spaces[0].control_id='replacement','identityChanged'],
 ['Conversation retired',f=>f.client.snapshot=async()=>snapshot('a',{conversation_id:'new'}),'conversationRetired'],
 ['Permission revoked',f=>f.client.assistants[0].capabilities.operations=[],'permission'],
 ['Old running turn',()=>{},'turnRetired'],
]){
 const f=fixture();f.add('fenced','a',name==='Old running turn'?{user_message_id:'retired'}:{});f.client.onlineReady=true;change(f);const w=f.worker();try{await w.wake();await w.wake();check(f.trace.length===0&&f.rows[0].state==='failed'&&f.rows[0].problem===problem,name+' preserves message and never retries automatically');}finally{w.close();}
}
{
 const f=fixture();f.add('guidance');f.client.onlineReady=true;f.client.snapshot=async()=>snapshot('a',{running:true,turn_id:'original'});f.setMode('before');const w=f.worker();try{
  await w.wake();f.client.snapshot=async()=>snapshot('a',{running:true,turn_id:'successor'});await w.wake();check(f.trace.filter(row=>row.kind==='send').length===1&&f.trace[0].request.user_message_id==='original'&&f.rows[0].problem==='turnRetired','First authoritative snapshot selects existing steer; retry never steers a successor');
 }finally{w.close();}
}
{
 const f=fixture();f.add('late-response');f.client.onlineReady=true;let release;f.setHold(new Promise(resolve=>release=resolve));const w=f.worker({timeout:5});try{
  await w.wake();check(f.rows[0].state==='uncertain','Bounded timeout persists unknown acceptance while old network work is still pending');
  f.setHold(null);await w.wake();await new Promise(setImmediate);const accepted=clone(f.rows[0]);release();await new Promise(setImmediate);
  check(f.trace.filter(row=>row.kind==='send').length===2&&f.trace[1].kind==='receipt'&&f.accepted.size===1,'New claim reconciles before same-ID retry while the old request still exists');
  check(f.rows[0].state==='accepted'&&f.rows[0].revision===accepted.revision&&f.rows[0].claim===null,'Late old response cannot overwrite the newer claimant or resurrect its claim');
 }finally{release();w.close();}
}
{
 const f=fixture();f.add('projection-pending');f.client.onlineReady=true;let fail=true,repair;
 f.client.resources={async applyCommand(){if(fail)throw Error('Synthetic projection quota');}};
 const w=f.worker({cacheFailed:(_row,_reason,retry)=>repair=retry});try{
  await w.wake();await new Promise(setImmediate);check(f.rows[0].state==='accepted'&&f.rows[0].projection_pending&&repair,'Business acceptance preserves originals when its local projection fails');
  fail=false;await repair();check(!f.rows[0].projection_pending&&f.trace.filter(row=>row.kind==='send').length===1,'Local projection retry releases originals without another POST');
 }finally{w.close();}
}
for(const [name,change,problem]of [
 ['Control changes during dispatch commit',f=>f.client.spaces[0].control_id='changed','identityChanged'],
 ['Permission revoked during dispatch commit',f=>f.client.assistants[0].capabilities.operations=[],'permission'],
]){
 const f=fixture();f.add('late-fence');f.client.onlineReady=true;
 const snapshot=f.client.snapshot;f.client.snapshot=async scope=>{const result=await snapshot(scope);queueMicrotask(()=>change(f));return result;};
 const w=f.worker();try{await w.wake();check(f.trace.length===0&&f.rows[0].state==='failed'&&f.rows[0].problem===problem,name+' cannot POST');}finally{w.close();}
}
{
 const f=fixture();let created=0,revoked=0;const nativeCreate=URL.createObjectURL,nativeRevoke=URL.revokeObjectURL;URL.createObjectURL=blob=>{created++;return nativeCreate(blob);};URL.revokeObjectURL=url=>{revoked++;nativeRevoke(url);};f.storage.files=async()=>[{id:'synthetic',blob:new Blob(['synthetic original'])}];const w=f.worker();try{
  const target=outboxTarget(f.client,scope,snapshot()).key,attachment={id:'synthetic'},one=w.acquireOriginal(target,'request',attachment,scope),two=w.acquireOriginal(target,'request',attachment,scope);
  const [first,second]=await Promise.all([one.url(),two.url()]);check(first===second&&created===1,'Mounted original preview leases share one local URL');one.release();check(revoked===0,'A remaining preview lease keeps its original URL valid');two.release();two.release();check(revoked===1,'Last preview release revokes its URL exactly once');
 }finally{w.close();URL.createObjectURL=nativeCreate;URL.revokeObjectURL=nativeRevoke;}
}
{
 const fetch=globalThis.fetch,commands=[],base=createMemoryProjectionStore();let release,held;
 globalThis.fetch=async(path,options)=>{
  if(path==='/api/v1/runtime')return new Response(JSON.stringify({runtime_id:'host',boot_id:'boot'}));
  if(path.startsWith('/api/v1/messages')){if(held)await held;return new Response(JSON.stringify({message_id:'accepted',duplicate:false}));}
  throw Error('Unexpected synthetic request '+path);
 };
 const client=await createProductionClient({resourceStorage:{...base,command(key,meta,value){commands.push(meta);return base.command(key,meta,value);}}});
 try{
  client.spaces=[{id:'space',control_id:'control'}];client.assistants=[{id:'a',space_id:'space',capabilities:caps}];client.setScope(scope);
  await assert.rejects(client.send({request_id:'wrong-control',conversation_id:'chat-a',text:'synthetic'},{...scope,expectedControl:'replacement'}),error=>error.status===409&&error.code==='control_changed');check(commands.length===0,'Production adapter rejects a stale Control before HTTP or local projection');
  held=new Promise(resolve=>release=resolve);const pending=client.send({request_id:'captured',conversation_id:'chat-a',text:'synthetic'},{...scope,expectedControl:'control'});client.spaces[0].control_id='replacement';release();const accepted=await pending;await accepted.committed;check(commands[0].control==='control','A response arriving after Control change keeps its acceptance projection in the captured Control namespace');
 }finally{client.resources.clear();globalThis.fetch=fetch;}
}
{
 const f=fixture(),row=f.add('optimistic');const rows=projectOutboxRows([],f.rows);check(rows.length===1&&rows[0].queue===row&&!rows[0].activity,'Queue projection creates only a user message');check(projectOutboxRows([{request_id:'optimistic'}],f.rows).length===1,'Authoritative transcript replaces the optimistic identity');
 const key=outboxIdentityKey(row),editing=new Map([[key,'Keep this editor']]),successor={...row,request:{...row.request,request_id:'new-request-same-sequence'}};
 check(projectOutboxRows([],f.rows,{editing})[0].queueEditing&&!projectOutboxRows([],[successor],{editing})[0].queueEditing&&outboxIdentityKey({...row,revision:row.revision+1})===key&&['key','host','control','space','owner','conversation'].every(part=>outboxIdentityKey({...row,target:{...row.target,[part]:'changed'}})!==key),'Editor intent binds complete target/request across sequence reuse and survives its own revisions');
 let failed=false;try{outboxTarget({...f.client,spaces:[]},scope,snapshot());}catch{failed=true;}check(failed,'Unknown initial Control cannot be guessed');
}
console.log('DONE '+checks+' outbox dispatcher checks');
