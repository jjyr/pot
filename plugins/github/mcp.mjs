// Real newline-delimited JSON-RPC MCP stdio server. The optional negotiated
// background-store extension is supplied only by Pot's installed-plugin host.
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {subscriptionCall} from './subscriptions.mjs';
import {readNotifications} from './read-notifications.mjs';
const lists={type:['array','null'],items:{type:'string'},maxItems:100};
const filters={type:'object',properties:Object.fromEntries(['repositories','reasons','subject_types','threads'].map(k=>[k,lists])),required:['repositories','reasons','subject_types','threads'],additionalProperties:false};
const scalar={request_id:{type:'string',minLength:1,maxLength:128},account_id:{type:'string'},assistant:{type:'string'},enabled:{type:'boolean'},filters};
const schema=(properties,required=Object.keys(properties))=>({type:'object',properties,required,additionalProperties:false});
export const tools=[
 {name:'list_subscriptions',description:'List all GitHub notification subscriptions, their receiving assistants, filters and revisions. Every assistant can manage any subscription. First connected use creates one ordinary mention subscription for the first assistant, once; deletion never recreates it. GitHub reason is thread-level and mention can persist on later comments. No direct-reply inference.',inputSchema:schema({})},
 {name:'create_subscription',description:'Create a subscription for any explicit receiving assistant from the returned assistant directory. Use the current account_id. Every filter field is explicit: null means unrestricted; [] matches none; AND across fields, OR within lists. Repositories are owner/repo. threads are numeric notification thread IDs, not issue/PR numbers. Use reasons:null for all reasons; mention is not forced onto new subscriptions. Reuse request_id only for identical retries. This only filters the personal GitHub notification feed and never changes GitHub watch, thread subscriptions or read state.',inputSchema:schema(scalar)},
 {name:'update_subscription',description:'Replace a subscription for any receiving assistant using its ID, revision and current account_id. Same filter semantics as create. Stale revisions conflict; identical request_id retries are safe. Applies only to new snapshots after the save; keeps cursor/dedup and revalidates unaccepted snapshots for the affected subscription. Does not change GitHub subscriptions.',inputSchema:schema({id:{type:'string'},revision:{type:'integer',minimum:1},...scalar})},
 {name:'delete_subscription',description:'Delete any subscription using ID, revision and an idempotent request_id. Accepted inbox messages remain; old in-flight results are rejected. Deleting the default does not recreate it.',inputSchema:schema({id:{type:'string'},revision:{type:'integer',minimum:1},request_id:scalar.request_id})},
 JSON.parse(readFileSync(new URL('./read-tool.json',import.meta.url),'utf8')),
];
export async function handleTool(name,input,host){if(name==='read_notifications')return readNotifications(input);if(!tools.some(t=>t.name===name))throw Error('unknown_tool');return subscriptionCall(host,name,input);}
if(process.argv[1]===fileURLToPath(import.meta.url)){
 const {serve}=await import(process.env.POT_MCP_RUNTIME);
 await serve({info:{name:'pot-github',version:'1'},tools,capabilities:{tools:{},experimental:{'pot/background-store':{version:1}}},handle:(name,input,peer)=>handleTool(name,input,(method,params)=>{
  if(peer.capabilities()?.experimental?.['pot/background-store']?.version!==1)throw Error('pot_background_store_required');
  return peer.request(method,params);
 })});
}
