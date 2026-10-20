// This adapter runs inside the ordinary pinned, supervised plugin process.
// Configuration contains no credentials. Calls are never retried automatically.
import {readFile} from 'node:fs/promises';
import {spawn} from 'node:child_process';
import {StringDecoder} from 'node:string_decoder';

export async function connect(config,credentials={}) {
 let child,stdin,reader,session,version,next=0,closed=false;
 const pending=new Map();
 const fail=()=>{for(const {reject,timer} of pending.values()){clearTimeout(timer);reject(Error('mcp_connection_closed'))}pending.clear()};
 if(config.transport==='stdio') {
  const env={...process.env,...credentials.env};delete env.MCP_CREDENTIALS;
  child=spawn(config.command,config.args||[],{env,cwd:config.cwd||undefined,stdio:['pipe','pipe','ignore'],shell:false});
  child.once('error',fail);child.once('exit',fail);stdin=child.stdin;stdin.on('error',fail);
  let buffered='';const stdoutDecoder=new StringDecoder('utf8');
  child.stdout.on('data',chunk=>{buffered+=stdoutDecoder.write(chunk);if(Buffer.byteLength(buffered)>1024*1024){fail();child.kill();return}let end;while((end=buffered.indexOf('\n'))>=0){const line=buffered.slice(0,end);buffered=buffered.slice(end+1);handleLine(line)}});
  function handleLine(line){if(Buffer.byteLength(line)>1024*1024){fail();child.kill();return}try{const value=JSON.parse(line);if(value.method&&value.id!==undefined){stdin.write(JSON.stringify({jsonrpc:'2.0',id:value.id,error:{code:-32601,message:'Unsupported method'}})+'\n');return}const wait=pending.get(value.id);if(wait){pending.delete(value.id);clearTimeout(wait.timer);value.error?wait.reject(Error('mcp_protocol_error')):wait.resolve(value.result)}}catch{fail()}}

 }
 async function rpc(method,params,notification=false){
  const id=notification?undefined:++next,body={jsonrpc:'2.0',...(notification?{}:{id}),method,params};
  if(config.transport==='stdio') {
   if(notification){stdin.write(JSON.stringify(body)+'\n');return}
   return new Promise((resolve,reject)=>{const timer=setTimeout(()=>{pending.delete(id);reject(Error('mcp_timeout'))},20000);pending.set(id,{resolve,reject,timer});stdin.write(JSON.stringify(body)+'\n')});
  }
  const headers={...credentials.headers,'Content-Type':'application/json',Accept:'application/json, text/event-stream'};
  if(session)headers['Mcp-Session-Id']=session;if(version)headers['MCP-Protocol-Version']=version;
  const response=await fetch(config.url,{method:'POST',headers,body:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(20000)});
  if(!response.ok){await response.body?.cancel();throw Error(response.status===401?'mcp_authorization_required':'mcp_http_'+response.status)}
  if(response.headers.get('Mcp-Session-Id'))session=response.headers.get('Mcp-Session-Id');
  if(notification){await response.body?.cancel();return}
  const stream=response.body.getReader(),decoder=new TextDecoder();let text='',bytes=0;
  try{for(;;){const {done,value}=await stream.read();if(done)break;bytes+=value.length;if(bytes>1024*1024)throw Error('mcp_response_limit');text+=decoder.decode(value,{stream:true});if(response.headers.get('content-type')?.includes('text/event-stream')){const events=text.split(/\r?\n\r?\n/);text=events.pop();for(const event of events){const data=event.split(/\r?\n/).filter(l=>l.startsWith('data:')).map(l=>l.slice(5).trimStart()).join('\n');if(!data)continue;let result;try{result=JSON.parse(data)}catch{continue}if(result.id===id){if(result.error)throw Error('mcp_protocol_error');return result.result}}}}
   const result=JSON.parse(text);if(result.id!==id||result.jsonrpc!=='2.0'||result.error)throw Error('mcp_protocol_error');return result.result;
  }finally{await stream.cancel().catch(()=>{})}
 }
 return {
  async initialize(){const result=await rpc('initialize',{protocolVersion:'2025-11-25',capabilities:{},clientInfo:{name:'pot',version:'1'}});if(!['2025-11-25','2025-06-18'].includes(result.protocolVersion)||!result.capabilities?.tools)throw Error('mcp_tools_unavailable');version=result.protocolVersion;await rpc('notifications/initialized',{},true)},
  async tools(){const tools=[];let cursor;const seen=new Set();let pages=0;do{if(++pages>32)throw Error('mcp_page_limit');const page=await rpc('tools/list',cursor?{cursor}:{});if(!Array.isArray(page.tools))throw Error('mcp_invalid_tools');tools.push(...page.tools);if(tools.length>128)throw Error('mcp_tool_limit');cursor=page.nextCursor;if(cursor){if(seen.has(cursor))throw Error('mcp_invalid_cursor');seen.add(cursor)}}while(cursor);return tools},
  call:(name,args)=>rpc('tools/call',{name,arguments:args}),
  async close(){if(closed)return;closed=true;fail();reader?.close();if(child){stdin.end();child.kill();await Promise.race([new Promise(r=>child.once('exit',r)),new Promise(r=>setTimeout(r,200))]);if(child.exitCode===null)child.kill('SIGKILL')}else if(session){await fetch(config.url,{method:'DELETE',headers:{...credentials.headers,'Mcp-Session-Id':session,'MCP-Protocol-Version':version},redirect:'error',signal:AbortSignal.timeout(2000)}).then(r=>r.body?.cancel()).catch(()=>{})}}
 };
}
export function createPlugin({emit}) {
 let client;
 return {
  async execute({input}) {
   const config=JSON.parse(await readFile(new URL('./config.json',import.meta.url),'utf8'));
   const credentials=JSON.parse(process.env.MCP_CREDENTIALS||'{}');client=await connect(config,credentials);
   await client.initialize();
   if(input.discover){emit({type:'text',text:JSON.stringify(await client.tools())});return}
   if(!config.tools.includes(input.tool))throw Error('mcp_tool_disabled_or_unknown');
   const args=JSON.parse(input.arguments||'{}');if(!args||typeof args!=='object'||Array.isArray(args))throw Error('mcp_arguments_must_be_object');
   const result=await client.call(input.tool,args);if(!Array.isArray(result.content))throw Error('mcp_invalid_result');
   for(const content of result.content){if(content.type==='text'||content.type==='image')emit(content);else emit({type:'text',text:JSON.stringify(content)})}
   return result.isError?{isError:true,error:'mcp_tool_error'}:undefined;
  },
  async dispose(){await client?.close()}
 };
}
