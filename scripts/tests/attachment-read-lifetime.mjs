import {installWSFixture} from './helpers/ws-fixture.mjs';
installWSFixture();
import assert from 'node:assert/strict';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import {createMemoryResourceCopies} from './resource-copies-memory.mjs';
const originalFetch=globalThis.fetch,originalDocument=globalThis.document,originalRevoke=URL.revokeObjectURL;
const requests=[],revoked=[],downloads=[];let ignoreAbort=false;
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
const entered=async count=>{for(let i=0;requests.length<count&&i<100;i++)await tick();assert.equal(requests.length,count);return requests[count-1];};
URL.revokeObjectURL=url=>{revoked.push(url);originalRevoke(url);};
globalThis.document={createElement:()=>({click(){downloads.push(this.href);},remove(){}}),body:{append(){}}};
globalThis.fetch=async(url,options)=>{
 const path=new URL(url,'http://fixture');
 if(path.pathname==='/api/v1/runtime')return {ok:true,status:200,json:async()=>({runtime_id:'runtime'})};
 assert(path.pathname.startsWith('/api/v1/attachments/'));
 const id=decodeURIComponent(path.pathname.split('/').at(-1));let resolve,reject;
 const pending=new Promise((done,fail)=>{resolve=done;reject=fail;}),entry={signal:options.signal,finish(){resolve({ok:true,status:200,json:async()=>({size:3,offset:0,revision:id,data:'AQID'})});}};
 requests.push(entry);const cancel=()=>{if(!ignoreAbort)reject(options.signal.reason);};options.signal.addEventListener('abort',cancel,{once:true});
 try{return await pending;}finally{options.signal.removeEventListener('abort',cancel);}
};
let client;
try{
 client=await createProductionClient({resourceStorage:createMemoryProjectionStore(),resourceCopies:createMemoryResourceCopies()});client.spaces=[{id:'local',control_id:'runtime'}];client.setScope({owner:'alice',space:'local'});
 const attachment=id=>({id,size:3,mime_type:'image/png',name:id+'.png'});
 // Shared viewers keep the read alive until the last lease leaves.
 const shared=attachment('shared'),releaseA=client.retainAttachment(shared),releaseB=client.retainAttachment(shared);
 const a=client.attachmentURL(shared),b=client.attachmentURL(shared),rejected=Promise.allSettled([a,b]),first=await entered(1);
 releaseA();await tick();assert(!first.signal.aborted);releaseB();await tick();assert(first.signal.aborted);assert((await rejected).every(item=>item.status==='rejected'&&item.reason.name==='AbortError'));
 // A prepared/live transfer reuses the in-progress bytes within one turn.
 const transfer=attachment('transfer'),releasePrepared=client.retainAttachment(transfer),prepared=client.attachmentURL(transfer),second=await entered(2);
 releasePrepared();const releaseLive=client.retainAttachment(transfer),live=client.attachmentURL(transfer),blobRead=client.attachmentBlob(transfer);assert.equal(blobRead,client.attachmentBlob(transfer),'preview and URL share the pending Blob');await tick();assert(!second.signal.aborted);assert.equal(requests.length,2);second.finish();const liveURL=await live;assert.equal(await prepared,liveURL);assert.equal((await blobRead).size,3);assert.equal(requests.length,2,'Blob access does not duplicate transport');releaseLive();await tick();assert(revoked.includes(liveURL));
 // Deliberately ignore transport abort to expose stale async completions.
 ignoreAbort=true;const retired=attachment('retired'),releaseOld=client.retainAttachment(retired),old=client.attachmentURL(retired),oldResult=Promise.allSettled([old]),third=await entered(3);
 releaseOld();await tick();assert(third.signal.aborted);
 const releaseNew=client.retainAttachment(retired),fresh=client.attachmentURL(retired),fourth=await entered(4);fourth.finish();const freshURL=await fresh;third.finish();assert.equal((await oldResult)[0].status,'rejected');assert.equal(await client.attachmentURL(retired),freshURL);assert.equal(requests.length,4,'old rejection cannot erase the new generation');releaseNew();await tick();assert(revoked.includes(freshURL));ignoreAbort=false;
 // Clearing all resources detaches old lease groups from later readers.
 const reset=attachment('reset'),releaseResetOld=client.retainAttachment(reset);client.releaseAttachments();const releaseResetNew=client.retainAttachment(reset),resetRead=client.attachmentURL(reset),fifth=await entered(5);releaseResetOld();await tick();assert(!fifth.signal.aborted);fifth.finish();await resetRead;releaseResetNew();await tick();
 // Download owns its own lease while the originating preview unmounts.
 const download=attachment('download'),releasePreview=client.retainAttachment(download),preview=client.attachmentURL(download),sixth=await entered(6),saving=client.downloadAttachment(download),preflight=await entered(7);
 preflight.finish();await tick();releasePreview();await tick();assert(!sixth.signal.aborted);sixth.finish();const downloadURL=await preview;await saving;assert(downloads.includes(downloadURL));assert(!revoked.includes(downloadURL));await new Promise(resolve=>setTimeout(resolve,1050));assert(revoked.includes(downloadURL));
 console.log('Attachment read lifetime: shared leases, last-reader abort, prepared transfer, late completion fencing, clear/reacquire and download ownership passed');
}finally{client?.releaseAttachments();client?.resources.clear();globalThis.fetch=originalFetch;globalThis.document=originalDocument;URL.revokeObjectURL=originalRevoke;}
