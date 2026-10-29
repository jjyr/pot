import assert from 'node:assert/strict';
import {readResourceChunks} from '../../web/src/state/resource-chunks.mjs';
import {digestBlob} from '../../web/src/state/resource-bytes.mjs';
const chunkSize=192*1024,bytes=Uint8Array.from({length:chunkSize*8+31},(_,i)=>i%251),revision='immutable';
let active=0,peak=0;
const pause=(ms,signal)=>new Promise((resolve,reject)=>{const done=()=>{signal?.removeEventListener('abort',cancel);resolve();},timer=setTimeout(done,ms),cancel=()=>{clearTimeout(timer);reject(signal.reason);};signal?.addEventListener('abort',cancel,{once:true});});
const read=async(offset,signal)=>{active++;peak=Math.max(peak,active);try{await pause(offset===0?12:2,signal);return {offset,size:bytes.length,revision,data:Buffer.from(bytes.subarray(offset,offset+chunkSize)).toString('base64')};}finally{active--;}};
const load=options=>readResourceChunks({size:bytes.length,revision,type:'image/png',read,...options});
const blobs=await Promise.all(Array.from({length:3},()=>load()));
assert.equal(peak,6,'resources share six transport slots');assert.equal(active,0);
for(const blob of blobs){assert.equal(blob.type,'image/png');assert.deepEqual(new Uint8Array(await blob.arrayBuffer()),bytes,'out-of-order replies assemble in original order');}
let singleActive=0,singlePeak=0;
await load({read:async(...args)=>{singlePeak=Math.max(singlePeak,++singleActive);try{return await read(...args);}finally{singleActive--;}}});assert.equal(singlePeak,4,'one file uses four lanes');
for(const change of [chunk=>({...chunk,revision:'other'}),chunk=>({...chunk,size:bytes.length-1}),chunk=>({...chunk,offset:1}),chunk=>({...chunk,data:'AA=='})]){
 await assert.rejects(load({read:async(...args)=>change(await read(...args))}),/Invalid resource/);assert.equal(active,0,'failed file aborts its sibling requests');
}
// Saturate the shared budget, then cancel readers queued behind the first files.
const abort=new AbortController();let reads=0;
const pending=Array.from({length:4},()=>load({signal:abort.signal,read:async(offset,signal)=>{reads++;await pause(1000,signal);return read(offset,signal);}}));
await new Promise(resolve=>setTimeout(resolve,5));abort.abort(new DOMException('retired','AbortError'));
const cancelled=await Promise.allSettled(pending);assert(cancelled.every(result=>result.status==='rejected'&&result.reason.name==='AbortError'));assert.equal(reads,6,'queued cancellations never start requests');
await load();assert.equal(active,0,'cancellation returns all transport slots');
// Rechecking source identity after transport must reject late immutable bytes.
let current=true;await assert.rejects(load({check(){if(!current)throw Error('source changed');},read:async(...args)=>{const result=await read(...args);current=false;return result;}}),/source changed/);
let digestReads=0;const blob=new Blob([bytes]),arrayBuffer=blob.arrayBuffer.bind(blob);blob.arrayBuffer=()=>{digestReads++;return arrayBuffer();};
const first=digestBlob(blob),second=digestBlob(blob);assert.equal(first,second);await first;await digestBlob(blob);assert.equal(digestReads,1,'verification and commit share one immutable Blob digest');
assert.equal(await digestBlob(new Blob([bytes])),await first,'a newly read Blob is independently verified');
console.log('Resource chunks: ordered parallel reads, four-per-file/six-shared bounds, cancellation, source retirement, malformed replies, and one digest per live Blob passed');
