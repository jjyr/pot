import test from 'node:test';
import assert from 'node:assert/strict';
import {connect} from './mcp-client.mjs';

test('stdio initializes, discovers nested schema, calls once, and closes its child',async()=>{
 const code=`let b='';process.stdin.on('data',v=>{b+=v;let end;while((end=b.indexOf('\\n'))>=0){const q=JSON.parse(b.slice(0,end));b=b.slice(end+1);if(q.id===undefined)continue;let result;if(q.method==='initialize')result={protocolVersion:'2025-11-25',capabilities:{tools:{}}};else if(q.method==='tools/list')result={tools:[{name:'echo',inputSchema:{type:'object',properties:{nested:{type:'object'}}}}]};else result={content:[{type:'text',text:JSON.stringify(q.params.arguments)}]};process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:q.id,result})+'\\n')}})`;
 const client=await connect({transport:'stdio',command:process.execPath,args:['-e',code]});
 try{await client.initialize();const tools=await client.tools();assert.equal(tools[0].name,'echo');const result=await client.call('echo',{nested:{value:1}});assert.deepEqual(JSON.parse(result.content[0].text),{nested:{value:1}})}finally{await client.close()}
});

test('Streamable HTTP supports SSE, paging and session headers without retrying calls',async()=>{
 const {createServer}=await import('node:http');let calls=0,deleted=0;
 const server=createServer(async(req,res)=>{
  assert.equal(req.headers.authorization,'Bearer fixture-only');
  if(req.method==='DELETE'){deleted++;assert.equal(req.headers['mcp-session-id'],'session-one');res.writeHead(204).end();return}
  let text='';for await(const chunk of req)text+=chunk;const request=JSON.parse(text);
  if(request.method==='notifications/initialized'){res.writeHead(202).end();return}
  let result;
  if(request.method==='initialize'){res.setHeader('Mcp-Session-Id','session-one');result={protocolVersion:'2025-11-25',capabilities:{tools:{}}}}
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
