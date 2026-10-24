import assert from 'node:assert/strict';
import {createFileImages} from '../../web/src/state/file-images.mjs';

const bytes=Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jucsAAAAASUVORK5CYII='),c=>c.charCodeAt(0));
const encode=value=>Buffer.from(value).toString('base64');
const scope={owner:'alice',space:'remote'},reads=[],revoked=[],oldCreate=URL.createObjectURL,oldRevoke=URL.revokeObjectURL;
let mode='normal',resume,blob;
URL.createObjectURL=value=>{blob=value;return 'blob:fixture';};URL.revokeObjectURL=url=>revoked.push(url);
const acquire=createFileImages(async(path,options)=>{
  reads.push({path,options});const query=new URL(path,'http://localhost').searchParams,offset=Number(query.get('offset'));
  if(mode==='held')await new Promise(resolve=>{resume=resolve;});
  if(mode==='error')throw Error('Offline');
  return {size:mode==='large'?17*1024*1024:bytes.length,offset:mode==='offset'?offset+1:offset,revision:mode==='revision'&&offset?'changed':'original',data:mode==='html'?encode(Buffer.from('<svg onload="x()">')):encode(bytes.subarray(offset,offset+32))};
},()=>scope);
try{
  const a=acquire('task','avatar.png'),b=acquire('task','avatar.png');scope.owner='bob';scope.space='local';
  assert.equal(await a.url,await b.url);assert.equal(reads.length,3,'Visible copies share one chunk read');
  assert(reads.every(read=>read.options.owner==='alice'&&read.options.space==='remote'),'Every chunk retains the captured owner and Space');
  assert.match(reads[1].path,/revision=original/);assert.equal(blob.type,'image/png');assert.deepEqual(new Uint8Array(await blob.arrayBuffer()),bytes);
  a.release();await Promise.resolve();assert.equal(revoked.length,0,'Another visible image keeps its URL');
  b.release();const replacement=acquire('task','avatar.png',{owner:'alice',space:'remote'});await Promise.resolve();assert.equal(revoked.length,0,'A streamed replacement keeps its original');replacement.release();await Promise.resolve();await Promise.resolve();assert.deepEqual(revoked,['blob:fixture']);
  for(const failure of ['large','offset','revision','html','error']){mode=failure;const lease=acquire('task',failure+'.png');await assert.rejects(lease.url,undefined,failure);lease.release();await Promise.resolve();}
  mode='held';const stale=acquire('task','stale.png');const rejected=assert.rejects(stale.url);stale.release();await Promise.resolve();assert(reads.at(-1).options.signal.aborted,'Detached previews abort pending reads');mode='normal';resume();await rejected;
  mode='error';const failed=acquire('task','retry.png');await assert.rejects(failed.url);failed.release();mode='normal';const retry=acquire('task','retry.png');await retry.url;retry.release();
  const one=acquire('task','scoped.png',{owner:'one',space:'one'}),two=acquire('task','scoped.png',{owner:'two',space:'two'});assert.notEqual(one.url,two.url,'Owner and Space isolate cache entries');await Promise.all([one.url,two.url]);one.release();two.release();
}finally{URL.createObjectURL=oldCreate;URL.revokeObjectURL=oldRevoke;}
console.log('Artifact images: chunk bounds, raster types, revision fencing, captured scope, sharing, disposal and retries passed');
