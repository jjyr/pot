import test from 'node:test';
import assert from 'node:assert/strict';
import {connect} from './mcp-client.mjs';

test('stdio initializes, discovers nested schema, calls once, and closes its child',async()=>{
 const code=`let b='';process.stdin.on('data',v=>{b+=v;let end;while((end=b.indexOf('\\n'))>=0){const q=JSON.parse(b.slice(0,end));b=b.slice(end+1);if(q.id===undefined)continue;let result;if(q.method==='initialize')result={protocolVersion:'2025-11-25',capabilities:{tools:{}}};else if(q.method==='tools/list')result={tools:[{name:'echo',inputSchema:{type:'object',properties:{nested:{type:'object'}}}}]};else result={content:[{type:'text',text:JSON.stringify(q.params.arguments)}]};process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:q.id,result})+'\\n')}})`;
 const client=await connect({transport:'stdio',command:process.execPath,args:['-e',code]});
 try{await client.initialize();const tools=await client.tools();assert.equal(tools[0].name,'echo');const result=await client.call('echo',{nested:{value:1}});assert.deepEqual(JSON.parse(result.content[0].text),{nested:{value:1}})}finally{await client.close()}
});
