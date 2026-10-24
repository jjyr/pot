import assert from 'node:assert/strict';
import {createChatSync} from '../../web/src/state/chat-sync.mjs';
const writes=[],displayed=[],errors=[];let release;
const cache={writeFrames:async(key,frames)=>{writes.push({key,frames});await new Promise(resolve=>release=resolve);return frames.at(-1);}};
const frame=(cursor,base)=>({runtime_id:'r',owner_id:'a',conversation_id:'c',boot_id:'b',kind:cursor===1?'reset':'delta',base_cursor:String(base),cursor:String(cursor),messages:[{id:'m',text:String(cursor)}],replies:[],history:{before:'1',after:'1'}});
const sync=createChatSync({cache,key:'scope',committed:value=>displayed.push(value),failed:reason=>errors.push(reason),delay:10000});
sync.push(frame(1,0));const flushing=sync.flush();await Promise.resolve();
assert.equal(displayed.length,0,'An in-flight transaction cannot update the view');
sync.push(frame(2,1));sync.push(frame(3,2));release();
for(let i=0;i<10&&!release;i++)await Promise.resolve();
await new Promise(resolve=>setTimeout(resolve,0));
assert.equal(displayed[0].cursor,'1');assert.equal(writes[1].frames.length,1,'Queued adjacent deltas coalesce');
assert.equal(writes[1].frames[0].base_cursor,'1');assert.equal(writes[1].frames[0].cursor,'3');assert.equal(writes[1].frames[0].messages[0].text,'3');
release();await flushing;assert.equal(displayed.at(-1).cursor,'3');assert.deepEqual(errors,[]);await sync.close();
let failure=false;
const broken=createChatSync({cache:{writeFrames:async()=>{throw Error('disk full');}},key:'scope',committed:()=>assert.fail('Failed writes cannot display'),failed:()=>failure=true,delay:10000});
broken.push(frame(1,0));await broken.flush();assert.equal(failure,true);await broken.close();

let bounded=false;
const limited=createChatSync({cache,key:'scope',committed:()=>assert.fail('Oversized transport buffers cannot display'),failed:()=>bounded=true,delay:10000});
limited.push({...frame(1,0),messages:Array.from({length:513},(_,index)=>({id:String(index),text:'bounded'}))});
await limited.flush();assert.equal(bounded,true);await limited.close();

const retiredWrites=[];
const retired=createChatSync({cache:{writeFrames:async(key,frames)=>{retiredWrites.push(...frames);return frames.at(-1);}},key:'previous-owner',committed:()=>assert.fail('A retired owner cannot update the selected view'),failed:reason=>assert.fail(reason.message),delay:10000});
retired.push(frame(1,0));await retired.retire();retired.push(frame(2,1));await retired.flush();
assert.deepEqual(retiredWrites.map(value=>value.cursor),['1'],'Switching owners persists already received work and rejects later events');
console.log('Chat sync: commit-before-display, streaming coalescing, failed-write fencing, bounded buffers and owner retirement passed');
