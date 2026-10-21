// Protocol implementation is provided by the official JS SDK. Pot owns only
// invocation bounds, process lifetime and injected tool/extension handlers.
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {Server} from '@modelcontextprotocol/sdk/server/index.js';
import {StdioServerTransport} from '@modelcontextprotocol/sdk/server/stdio.js';
import {StreamableHTTPClientTransport} from '@modelcontextprotocol/sdk/client/streamableHttp.js';
import {ListToolsRequestSchema,CallToolRequestSchema} from '@modelcontextprotocol/sdk/types.js';
import {z} from 'zod';
import {spawn} from 'node:child_process';

const timeout=20000,maxBytes=3*1024*1024;
export async function connect(config,credentials={}){
 let child,transport,closed=false;
 const client=new Client({name:'pot',version:'1'},{capabilities:{}});
 if(config.transport==='stdio'){
  const env={...process.env,...credentials.env};delete env.MCP_CREDENTIALS;
  child=spawn(config.command,config.args||[],{env,cwd:config.cwd||undefined,stdio:['pipe','pipe','ignore'],shell:false});
  transport=new StdioServerTransport(child.stdout,child.stdin,{maxBufferSize:maxBytes});
  const close=transport.close.bind(transport);
  transport.close=async()=>{await close();child.stdin.end();if(child.exitCode===null){child.kill();await Promise.race([new Promise(resolve=>child.once('exit',resolve)),new Promise(resolve=>setTimeout(resolve,200))]);if(child.exitCode===null)child.kill('SIGKILL');}};
  child.once('error',error=>transport.onerror?.(error));child.once('exit',()=>transport.onclose?.());
  child.stdin.on('error',error=>transport.onerror?.(error));
 }else if(config.transport==='http'){
  transport=new StreamableHTTPClientTransport(new URL(config.url),{requestInit:{headers:credentials.headers,redirect:'error'},reconnectionOptions:{maxRetries:0,maxReconnectionDelay:0,initialReconnectionDelay:0,reconnectionDelayGrowFactor:1},fetch:async(url,init)=>{
   const response=await fetch(url,{...init,redirect:'error',signal:AbortSignal.any([init?.signal||new AbortController().signal,AbortSignal.timeout(timeout)])});
   if(!response.body)return response;
   let bytes=0;
   const body=response.body.pipeThrough(new TransformStream({transform(chunk,controller){bytes+=chunk.byteLength;if(bytes>maxBytes)throw Error('mcp_response_limit');controller.enqueue(chunk);}}));
   return new Response(body,{status:response.status,statusText:response.statusText,headers:response.headers});
  }});
 }else throw Error('mcp_invalid_transport');
 return {
  async initialize(){await client.connect(transport,{timeout});if(!client.getServerCapabilities()?.tools)throw Error('mcp_tools_unavailable');},
  async tools(){const tools=[],seen=new Set(),names=new Set();let cursor,bytes=0;for(let page=0;page<128;page++){
   const result=await client.listTools(cursor?{cursor}:{},{timeout});
   for(const tool of result.tools){bytes+=Buffer.byteLength(JSON.stringify(tool));if(bytes>128*1024||Buffer.byteLength(JSON.stringify(tool.inputSchema))>65536||Buffer.byteLength(tool.description||'')>8192)throw Error('mcp_catalog_limit');if(!tool.name||tool.name.length>128)throw Error('mcp_invalid_tool');if(names.has(tool.name))throw Error('mcp_duplicate_tool');names.add(tool.name);tools.push(tool);}
   if(tools.length>128)throw Error('mcp_tool_limit');cursor=result.nextCursor;if(!cursor)return tools;
   if(cursor.length>4096||seen.has(cursor))throw Error('mcp_invalid_cursor');seen.add(cursor);
  }throw Error('mcp_page_limit');},
  call:(name,args,options={})=>client.callTool({name,arguments:args},undefined,{timeout,...options}),
  async close(){if(closed)return;closed=true;if(config.transport==='http'&&transport.sessionId)await transport.terminateSession().catch(()=>{});await client.close();}
 };
}
export async function serve({info,tools,handle,capabilities={tools:{}}},input=process.stdin,output=process.stdout){
 const server=new Server(info,{capabilities});
 server.setRequestHandler(ListToolsRequestSchema,async()=>({tools}));
 server.setRequestHandler(CallToolRequestSchema,async(request,extra)=>{
  try{
   if(!tools.some(tool=>tool.name===request.params.name))throw Error('unknown_tool');
   const value=await handle(request.params.name,request.params.arguments||{},{capabilities:()=>server.getClientCapabilities(),request:(method,params)=>server.request({method,params},z.unknown(),{timeout,signal:extra.signal}),signal:extra.signal});
   return {content:[{type:'text',text:JSON.stringify(value)}],structuredContent:value};
  }catch(error){return {isError:true,content:[{type:'text',text:JSON.stringify({error:error.message})}],structuredContent:{error:error.message}};}
 });
 const transport=new StdioServerTransport(input,output,{maxBufferSize:maxBytes});
 await server.connect(transport);
 input.once('end',()=>server.close());
 return server;
}
