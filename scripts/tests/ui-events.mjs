import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
const source=await readFile(new URL('../../crates/web-ui/web/events.js',import.meta.url),'utf8');
const {Decoder,subscribe}=await import('data:text/javascript;base64,'+Buffer.from(source).toString('base64'));
const bytes=text=>new TextEncoder().encode(text);
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
test('SSE handles UTF-8 split at every byte, CRLF, comments and multiline data',()=>{
  const decoder=new Decoder(),frames=[];
  for(const byte of bytes(': keepalive\r\n\r\nevent: snapshot\r\ndata: 中文\r\ndata: second\r\n\r\n'))frames.push(...decoder.push(new Uint8Array([byte])));
  assert.deepEqual(frames,[{event:'snapshot',data:'中文\nsecond'}]);decoder.finish();
});
test('SSE bounds individual frames, rejects truncated and invalid UTF-8',()=>{
  assert.throws(()=>new Decoder().push(new Uint8Array([255])));
  const partial=new Decoder();partial.push(bytes('data: partial\n'));assert.throws(()=>partial.finish());
  assert.throws(()=>new Decoder().push(bytes('data: '+'中'.repeat(3*1024*1024))));
  const decoder=new Decoder();assert.equal(decoder.push(bytes('data: x\n\n'.repeat(1000000))).length,1000000);
});
test('An idle subscription does not issue requests; cancellation releases the reader',async()=>{
  let requests=0,cancelled=false,frames=0;
  globalThis.fetch=async()=>{requests++;return new Response(new ReadableStream({start(controller){controller.enqueue(bytes('event: changes\ndata: {}\n\n'));},cancel(){cancelled=true;}}),{headers:{'content-type':'text/event-stream'}});};
  const stop=subscribe('/api/v1/changes',{},()=>frames++,error=>assert.fail(error));
  await pause(350);assert.equal(requests,1);assert.equal(frames,1);stop();
  await pause(20);assert.equal(cancelled,true);
});
test('Disconnect reconnects and auth rejection terminates retries',async()=>{
  let requests=0,errors=0;
  globalThis.fetch=async()=>{requests++;return requests===1?new Response(bytes('data: {}\n\n'),{headers:{'content-type':'text/event-stream'}}):new Response('',{status:401});};
  const stop=subscribe('/api/v1/changes',{},()=>{},()=>errors++);
  await pause(650);assert.equal(requests,2);assert.equal(errors,2);stop();
});
