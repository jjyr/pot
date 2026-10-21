import test from 'node:test';
import assert from 'node:assert/strict';
import {connect} from './assets/node-runtime.mjs';

test('stdio negotiates June, preserves standard metadata, calls once, and closes its child',async()=>{
 const code=`let b='';process.stdin.on('data',v=>{b+=v;let end;while((end=b.indexOf('\\n'))>=0){const q=JSON.parse(b.slice(0,end));b=b.slice(end+1);if(q.id===undefined)continue;let result;if(q.method==='initialize'){if(q.params.protocolVersion!=='2025-11-25')process.exit(3);result={protocolVersion:'2025-06-18',capabilities:{tools:{}},serverInfo:{name:'fixture',version:'1'}};}else if(q.method==='tools/list')result={tools:[{name:'echo',title:'Echo title',annotations:{readOnlyHint:true},inputSchema:{type:'object',properties:{nested:{type:'object'}}}}]};else result={content:[{type:'text',text:JSON.stringify(q.params.arguments),annotations:{audience:['user']}}],structuredContent:{ok:true},_meta:{fixture:'retained'}};process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:q.id,result})+'\\n')}})`;
 const client=await connect({transport:'stdio',command:process.execPath,args:['-e',code]});
 try{await client.initialize();const tools=await client.tools();assert.equal(tools[0].name,'echo');assert.equal(tools[0].title,'Echo title');assert.equal(tools[0].annotations.readOnlyHint,true);const result=await client.call('echo',{nested:{value:1}});assert.deepEqual(JSON.parse(result.content[0].text),{nested:{value:1}});assert.deepEqual(result.content[0].annotations,{audience:['user']});assert.deepEqual(result.structuredContent,{ok:true});assert.deepEqual(result._meta,{fixture:'retained'});}finally{await client.close()}
});

test('SDK cancellation reaches the server without retrying the tool',async()=>{
 const source=`import {serve} from ${JSON.stringify(new URL('./assets/node-runtime.mjs',import.meta.url).href)};
 let calls=0,canceled=false;
 await serve({info:{name:'cancel-fixture',version:'1'},tools:[{name:'wait',inputSchema:{type:'object'}},{name:'state',inputSchema:{type:'object'}}],handle:async(name,args,peer)=>{
  if(name==='state')return {calls,canceled};
  calls++;
  await new Promise(resolve=>peer.signal.addEventListener('abort',()=>{canceled=true;resolve();},{once:true}));
  return {canceled};
 }});`;
 // A separate state call proves the remote handler observed cancellation.
 const client=await connect({transport:'stdio',command:process.execPath,args:['--input-type=module','-e',source]});
 try{
  await client.initialize();
  const controller=new AbortController();
  const pending=assert.rejects(client.call('wait',{}, {signal:controller.signal}));
  for(let attempt=0;attempt<100;attempt++){
   const state=(await client.call('state',{})).structuredContent;
   if(state.calls===1)break;
   if(attempt===99)assert.fail('tool did not start');
   await new Promise(resolve=>setTimeout(resolve,10));
  }
  controller.abort();await pending;
  let state;
  for(let attempt=0;attempt<100;attempt++){
   state=(await client.call('state',{})).structuredContent;
   if(state.canceled)break;
   await new Promise(resolve=>setTimeout(resolve,10));
  }
  assert.deepEqual(state,{calls:1,canceled:true});
 }finally{await client.close();}
});

test('Streamable HTTP supports SSE, paging and session headers without retrying calls',async()=>{
 const {createServer}=await import('node:http');let calls=0,deleted=0;
 const server=createServer(async(req,res)=>{
  assert.equal(req.headers.authorization,'Bearer fixture-only');
  if(req.method==='GET'){res.writeHead(405).end();return;}
  if(req.method==='DELETE'){deleted++;assert.equal(req.headers['mcp-session-id'],'session-one');res.writeHead(204).end();return}
  let text='';for await(const chunk of req)text+=chunk;const request=JSON.parse(text);
  if(request.method==='notifications/initialized'){res.writeHead(202).end();return}
  let result;
  if(request.method==='initialize'){res.setHeader('Mcp-Session-Id','session-one');result={protocolVersion:'2025-11-25',capabilities:{tools:{}},serverInfo:{name:'fixture',version:'1'}}}
  else{assert.equal(req.headers['mcp-session-id'],'session-one');assert.equal(req.headers['mcp-protocol-version'],'2025-11-25');
   if(request.method==='tools/call'){calls++;res.setHeader('Content-Type','application/json');res.end(JSON.stringify({jsonrpc:'2.0',id:request.id,error:{code:-32602,message:'fixture failure'}}));return}
   result=request.params.cursor?{tools:[{name:'second',inputSchema:{type:'object'}}]}:{tools:[{name:'first',inputSchema:{type:'object'}}],nextCursor:'next'};
  }
  const frame=JSON.stringify({jsonrpc:'2.0',id:request.id,result});res.setHeader('Content-Type','text/event-stream');res.write('event: message\ndata: '+frame.slice(0,8));setImmediate(()=>res.end(frame.slice(8)+'\n\n'));
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const client=await connect({transport:'http',url:'http://127.0.0.1:'+server.address().port},{headers:{Authorization:'Bearer fixture-only'}});
 try{await client.initialize();assert.deepEqual((await client.tools()).map(tool=>tool.name),['first','second']);await assert.rejects(client.call('first',{}));assert.equal(calls,1)}finally{await client.close();await new Promise(resolve=>server.close(resolve))}
 assert.equal(deleted,1);
});

test('SDK client discovers the actual GitHub server using its standard handshake',async()=>{
 const {fileURLToPath}=await import('node:url');
 const client=await connect({transport:'stdio',command:process.execPath,args:[fileURLToPath(new URL('../../plugins/github/mcp.mjs',import.meta.url))]}, {env:{POT_MCP_RUNTIME:new URL('./assets/node-runtime.mjs',import.meta.url).href}});
 try{await client.initialize();assert.deepEqual((await client.tools()).map(t=>t.name).sort(),['create_subscription','delete_subscription','list_subscriptions','read_notifications','update_subscription']);}finally{await client.close();}
});

test('exit and invalid initialize responses reject without retry',async()=>{
 const exited=await connect({transport:'stdio',command:process.execPath,args:['-e','process.exit(2)']});
 try{await assert.rejects(exited.initialize());}finally{await exited.close();}
 const source=`process.stdin.once('data',chunk=>{const q=JSON.parse(chunk.toString().trim());process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:q.id,result:{protocolVersion:'2025-11-25',capabilities:{tools:{}},serverInfo:'invalid'}})+'\\n');})`;
 const invalid=await connect({transport:'stdio',command:process.execPath,args:['-e',source]});
 try{await assert.rejects(invalid.initialize());}finally{await invalid.close();}
});
