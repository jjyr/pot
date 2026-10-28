import assert from 'node:assert/strict';
import {pathToFileURL} from 'node:url';
import {resolve} from 'node:path';
const before=process.argv.includes('--before');
const source=process.env.POT_OUTBOX_SOURCE||resolve('web/src/state/chat-outbox.mjs');
const {createChatOutbox}=await import(pathToFileURL(source).href);
const target={host:'host',control:'control',space:'space',owner:'a',conversation:'chat'};
target.key=JSON.stringify(Object.values(target));
const row=id=>({sequence:1,revision:1,target:structuredClone(target),request:{request_id:id,conversation_id:'chat',text:id},state:'accepted',started:true,projection_pending:true,claim:null,original:true});
let current=row('old-request'),releaseOld,releaseNew,commits=0,posts=0;
const client={runtime:{runtime_id:'host'},spaces:[{id:'space',control_id:'control'}],onlineReady:true,operationsReady:true,
  resources:{applyCommand(){commits++;return new Promise(done=>{if(commits===1)releaseOld=done;else releaseNew=done;});}},
  send(){posts++;throw Error('Accepted local repair cannot POST');},
};
const storage={list:async()=>[structuredClone(current)],close(){},
  async releaseFiles(expected){
    // The before source calls the original bare-sequence API. The after source
    // must supply the complete saved submission, as the real IDB store requires.
    if(before){assert.equal(typeof expected,'number');if(current.sequence!==expected)return null;}
    else{assert.equal(typeof expected,'object');if(expected.sequence!==current.sequence||expected.target.key!==current.target.key||expected.request.request_id!==current.request.request_id)return null;}
    current.original=false;current.projection_pending=false;return structuredClone(current);
  },
};
const clock={setInterval:()=>1,clearInterval(){},setTimeout,clearTimeout};
const worker=createChatOutbox({client,storage,clock});
try{
  await worker.wake();assert.equal(commits,1);
  // Model a recreated browser database reusing sequence 1 for a distinct,
  // already accepted submission while the old local projection is pending.
  current=row('new-request');await worker.wake();
  const beforeOldCompletion={commits,original:current.original,pending:current.projection_pending};
  releaseOld();await new Promise(setImmediate);
  const afterOldCompletion={commits,original:current.original,pending:current.projection_pending,posts};
  if(before){assert.equal(commits,1);assert.equal(current.original,false);assert.equal(current.projection_pending,false);}
  else{
    assert.equal(commits,2,'reused sequence does not suppress the new request projection');
    assert(current.original&&current.projection_pending,'late old projection cannot release a new request original');
    releaseNew();await new Promise(setImmediate);assert(!current.original&&!current.projection_pending);
  }
  assert.equal(posts,0);
  console.log(JSON.stringify({passed:true,mode:before?'observed-before-negative':'after',source,beforeOldCompletion,afterOldCompletion,final:{original:current.original,pending:current.projection_pending,posts}}));
}finally{releaseOld?.();releaseNew?.();worker.close();}
