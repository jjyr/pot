// Pot's flat tool adapter. Protocol and transports live in the shared SDK asset.
import {readFile} from 'node:fs/promises';
import {connect} from './mcp-runtime.mjs';
export function createPlugin({emit}){
 let client;
 return {
  async execute({input}){
   const config=JSON.parse(await readFile(new URL('./config.json',import.meta.url),'utf8'));
   client=await connect(config,JSON.parse(process.env.MCP_CREDENTIALS||'{}'));
   await client.initialize();
   if(input.discover){emit({type:'text',text:JSON.stringify(await client.tools())});return;}
   if(!config.tools.includes(input.tool))throw Error('mcp_tool_disabled_or_unknown');
   const args=JSON.parse(input.arguments||'{}');if(!args||typeof args!=='object'||Array.isArray(args))throw Error('mcp_arguments_must_be_object');
   const result=await client.call(input.tool,args);
   for(const content of result.content){if(content.type==='text'||content.type==='image')emit(content);else emit({type:'text',text:JSON.stringify(content)});}
   // Preserve standard structured output and metadata rather than silently losing it.
   if(result.structuredContent!==undefined||result._meta!==undefined)emit({type:'text',text:JSON.stringify({structuredContent:result.structuredContent,_meta:result._meta})});
   return result.isError?{isError:true,error:'mcp_tool_error'}:undefined;
  },
  async dispose(){await client?.close();}
 };
}
