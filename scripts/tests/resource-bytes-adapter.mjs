import assert from 'node:assert/strict';
import {createDisplayResources} from '../../web/src/state/display-resources.mjs';
import {createResourceReader} from '../../web/src/state/resource-reader.mjs';
import {copyKey,digestBlob,messageRevision,resourceScope} from '../../web/src/state/resource-bytes.mjs';
import {createMemoryResourceCopies as memory} from './resource-copies-memory.mjs';
const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return{promise,resolve};};

const blob=new Blob(['# Saved original\n']),digest=await digestBlob(blob),file={path:'report.md',mime_type:'text/plain',size:blob.size,digest};
const resource={id:'immutable',name:'report.md',entry:file.path,files:[file],mime_type:file.mime_type,size:file.size,download:file};
const client={runtime:{runtime_id:'host'},scope:()=>({space:'local',owner:'alice'}),spaces:[{id:'local',control_id:'control'}]},copies=memory();let requests=0,offline=false,gate,entered;
const request=async path=>{requests++;if(offline)throw Error('offline');if(gate){entered?.resolve();await gate.promise;}if(path.includes('message-files'))return[{message:'message',id:'reference',revision:await messageRevision('source'),state:'ready',href:'report.md',resource}];return{size:blob.size,offset:0,revision:digest,data:btoa(await blob.text())};};
const reader=createResourceReader(client,request,{copies}),acquire=createDisplayResources(request,client.scope,()=>'/live',{copies,identity:reader.identity,current:reader.current});
await reader.bindings('message',{sourceText:'source'});const a=acquire(resource),b=acquire(resource);assert.equal(a.read(file.path),b.read(file.path));assert.equal(await (await a.read(file.path)).text(),await blob.text());assert.equal(await a.url(file.path),await b.url(file.path));a.release();b.release();await Promise.resolve();
offline=true;const before=requests,restored=acquire(resource);assert.equal(await(await restored.read(file.path)).text(),await blob.text());assert.equal(requests,before);assert.equal((await reader.bindings('message',{sourceText:'source'}))[0].resource.id,'immutable');await assert.rejects(reader.bindings('message',{sourceText:'different'}),/offline/);
restored.release();await Promise.resolve();
await reader.retire(reader.identity(client.scope()));await assert.rejects(acquire(resource).read(file.path),/source changed|retired/);
offline=false;await reader.bindings('message',{sourceText:'source'});const recovered=acquire(resource);assert.equal(await(await recovered.read(file.path)).text(),await blob.text());recovered.release();
const stable=acquire(resource);await stable.read(file.path);const stableURL=await stable.url(file.path);copies.compact();offline=true;assert.equal(await(await stable.read(file.path)).text(),await blob.text());assert.equal(await stable.url(file.path),stableURL);stable.release();offline=false;
await reader.retire(reader.identity(client.scope()));copies.compact();await reader.bindings('message',{sourceText:'source'});const compacted=acquire(resource);assert.equal(await(await compacted.read(file.path)).text(),await blob.text());compacted.release();
// A request started before revoke cannot publish after a later explicit live recovery.
const raceCopies=memory(),race=createResourceReader(client,request,{copies:raceCopies});gate=deferred();entered=deferred();const late=race.bindings('message',{sourceText:'source'});await entered.promise;await race.retire(race.identity(client.scope()));gate.resolve();gate=null;await assert.rejects(late);
console.log('Resource adapter: cold cache without network await, immutable identity, shared lease, source revision isolation, retirement, live recovery and late response fencing passed');
await import('./resource-chunks.mjs');
