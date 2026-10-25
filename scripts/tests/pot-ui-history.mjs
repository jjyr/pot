import assert from 'node:assert/strict';
import {mergeUIFrame,mergeHistoryPage,HISTORY_CACHE_LIMIT} from '../../web/src/state/chat-history.mjs';
import {createConversationState,beginSubscription,acceptSnapshot} from '../../web/src/state/conversation.mjs';
import {normalizeSnapshot} from '../../web/src/adapters/production.mjs';
import {messageBytes,truncateUTF8,utf8Bytes} from '../../web/src/state/projection-bytes.mjs';
const message=(n,text=`message ${n}`,version=10)=>({id:`m${n}`,conversation_id:'chat',sequence:String(n),version,role:n%2?'user':'assistant',reply_to:n%2?undefined:`m${n-1}`,text,status:'complete'});
const frame=(cursor,kind,messages,base=0)=>({runtime_id:'r',owner_id:'a',conversation_id:'chat',boot_id:'boot',cursor:String(cursor),kind,base_cursor:base,replies:[],running:false,turn_id:null,messages,history:{before:messages[0]?.sequence||'',after:messages.at(-1)?.sequence||''}});
let current=mergeUIFrame(null,frame(10,'reset',[message(9),message(10)]));
current=mergeUIFrame(current,frame(12,'delta',[message(10,'new tokens',12),message(11,'new input',12)],10));
const stale={boot_id:'boot',conversation_id:'chat',cursor:10,messages:[message(8),message(9),message(10,'old tokens')],history:{before:'8',after:'10'}};
current=mergeHistoryPage(current,stale,'9');
assert.equal(current.messages.find(m=>m.id==='m10').text,'new tokens','late history cannot rewind streaming content');
assert.deepEqual(current.messages.map(m=>m.id),['m8','m9','m10','m11']);
assert.throws(()=>mergeUIFrame(current,frame(14,'delta',[message(12)],13)),/conversation_gap/);
assert.equal(mergeHistoryPage(current,{...stale,boot_id:'other'}),null);
const state=createConversationState(),epoch=beginSubscription(state);
assert(acceptSnapshot(state,{...current,kind:'reset'},epoch));
assert.equal(acceptSnapshot(state,frame(11,'delta',[],10),epoch),false,'old and out-of-order events are rejected');
assert.equal(acceptSnapshot(state,frame(12,'delta',[],10),epoch),false,'duplicate frames are rejected');
assert.equal(acceptSnapshot(state,{...frame(13,'reset',[]),boot_id:'new'},epoch),false,'boot change requires fresh subscription');
const nextEpoch=beginSubscription(state,{retainPending:true});
assert(acceptSnapshot(state,{...frame(1,'reset',[]),boot_id:'new'},nextEpoch));
current=mergeUIFrame(null,frame(10,'reset',Array.from({length:80},(_,i)=>message(i+1921))));
for(let before=1920;before>0;before-=80){current=mergeHistoryPage(current,{boot_id:'boot',conversation_id:'chat',cursor:10,messages:Array.from({length:80},(_,i)=>message(before-79+i)),history:{before:String(before-79),after:String(before)}},String(before));assert(current.messages.length<=HISTORY_CACHE_LIMIT);}
assert(current.history.has_newer,'evicted newer history can be loaded again');
assert.equal(current.messages[0].id,'m1');
const oldLength=current.messages.length;
current=mergeUIFrame(current,frame(11,'delta',[message(2001,'unloaded latest',11)],10),'10');
assert.equal(current.messages.length,oldLength,'unloaded changes do not grow the reading window');
assert.equal(current._latest,'2001');
const wire={kind:'reset',base_cursor:0,view:{replies:[],state:{boot_id:'boot',cursor:10,conversation_id:'chat',turn:null},messages:[{message:{id:'m',role:'user',text:'hello',status:'complete'},sequence:'1',version:10}],history:{before:'1',after:'1'}}};
assert.equal(normalizeSnapshot(wire,{runtime_id:'r'},'a').messages[0].text,'hello');
console.log('Chat history: bounded cache, concurrent pages/deltas, stale pages, cursor gaps, duplicates, boot reset and wire projection passed');
// A send receipt must settle even when the reader's bounded window excludes it.
const scoped=createConversationState(),scopeEpoch=beginSubscription(scoped);
assert(acceptSnapshot(scoped,frame(1,'reset',[message(1,'oldest',1)]),scopeEpoch));
scoped.snapshot.history.has_newer=true;scoped.snapshot._latest='100';
const incoming=frame(2,'delta',[{...message(101,'sent input',2),role:'user',request_id:'sent-request'}],1);
const key=JSON.stringify(['r','a','chat']);scoped.sends.set(key,{request:{request_id:'sent-request'},phase:'sending'});
assert(acceptSnapshot(scoped,incoming,scopeEpoch));assert.equal(scoped.sends.has(key),false);assert(!scoped.snapshot.messages.some(m=>m.id==='m101'));

const pendingFrame={...frame(1,'reset',[message(1)],0),replies:[{input_id:'m1',state:'running'}]};
let lifecycle=mergeUIFrame(null,pendingFrame);
lifecycle=mergeUIFrame(lifecycle,frame(2,'delta',[message(2)],1));
assert.equal(lifecycle.replies[0].state,'running','An unrelated delta retains a visible reply lifecycle');
lifecycle=mergeUIFrame(lifecycle,{...frame(3,'delta',[],2),replies:[{input_id:'m1',state:'completed'}]});
assert.equal(lifecycle.replies[0].state,'completed','An empty cleanup delta settles its input');
lifecycle=mergeUIFrame(lifecycle,frame(4,'reset',[message(3)]));
assert.deepEqual(lifecycle.replies,[],'Resets remove lifecycles from the previous window');

const diskState=createConversationState(),diskEpoch=beginSubscription(diskState);
acceptSnapshot(diskState,frame(1,'reset',[message(1,'unchanged',1),message(2,'initial',1)]),diskEpoch,{committed:true});
const unchanged=diskState.snapshot.messages[0];
acceptSnapshot(diskState,frame(2,'reset',[message(1,'unchanged',1),message(2,'streamed',2)]),diskEpoch,{committed:true});
assert.equal(diskState.snapshot.messages[0],unchanged,'Committed disk reads retain the active identity of unchanged rows');
assert.equal(diskState.snapshot.messages[1].text,'streamed');

const large=Array.from({length:12},(_,i)=>message(i+1,'中'.repeat(900000)));
const multibyte=mergeUIFrame(null,frame(12,'reset',large));
assert(multibyte.messages.reduce((sum,row)=>sum+messageBytes(row),0)<=16*1024*1024,'CJK windows obey the same UTF-8 JSON byte accounting as IndexedDB');
assert(multibyte.history.has_more,'Byte eviction leaves earlier history loadable');
assert.throws(()=>mergeUIFrame(null,frame(1,'reset',[message(1,'中'.repeat(6*1024*1024))])),/chat_cache_capacity/,'One oversized row cannot bypass the window budget');
const active={...message(13,'😀'.repeat(1000)),status:'streaming'},dependency={...message(0,'input'),context_only:true};
const anchored=mergeUIFrame(null,frame(13,'reset',[dependency,...large,active]),'1');
assert(anchored.messages.some(row=>row.id===active.id)&&anchored.messages.some(row=>row.id===dependency.id),'Byte eviction retains active output and context dependencies');
for(const text of ['中'.repeat(50),'😀'.repeat(50),'a中😀'.repeat(50)])for(const tail of [false,true])for(const limit of [0,1,2,3,4,7,17,128]){
  const bounded=truncateUTF8(text,limit,{tail});assert(utf8Bytes(bounded)<=limit);assert(bounded.isWellFormed());assert(!bounded.includes('\ufffd'));assert(tail?text.endsWith(bounded):text.startsWith(bounded));
}
console.log('UTF-8 bounds: CJK/emoji windows, active dependencies, oversized-row rejection and legal prefix/tail truncation passed');
const mutable=message(1,'initial');delete mutable.version;
mergeUIFrame(null,frame(1,'reset',[mutable]));mutable.text='中'.repeat(6*1024*1024);
assert.throws(()=>mergeUIFrame(null,frame(2,'reset',[mutable])),/chat_cache_capacity/,'Mutated raw Task backfill rows must not reuse an earlier smaller byte weight');
