import {toolResult} from './tool-calls.mjs';

// Only complete JSON can supply message content. Never scrape streamed fragments
// or tool results for a body: acceptance is separate from the peer's response.
export function communicationArgs(tool){
  let args=tool?.args;
  if(typeof args==='string'){try{args=JSON.parse(args);}catch{return null;}}
  return args&&typeof args==='object'&&!Array.isArray(args)?args:null;
}
export const communicationKey=address=>JSON.stringify([address?.node_id,address?.kind,address?.agent_id]);
export function communication(tool,actor){
  if(!['send_message','receive_message','create_task'].includes(tool?.name))return null;
  const args=communicationArgs(tool),result=toolResult(tool);
  if(tool.name==='receive_message'){
    const messages=Array.isArray(args?.messages)?args.messages:[];
    return {kind:'receive',messages:messages.map(mail=>({from:mail?.from,to:mail?.to,text:typeof mail?.text==='string'?mail.text:null,...(typeof mail?.id==='string'?{id:mail.id}:{})})),sources:[...new Map(messages.map(mail=>[communicationKey(mail?.from),mail?.from])).values()],count:messages.length};
  }
  const task=tool.name==='create_task';
  const envelope=result?.message||result,mailID=typeof result?.message_id==='string'?result.message_id:typeof envelope?.id==='string'&&envelope?.from&&envelope?.to?envelope.id:undefined;
  return {kind:task?'delegate':'send',sources:[],count:1,task:task&&result?.id?result:null,accepted:!task&&tool.phase==='completed'&&result?.accepted===true,messages:[{...(mailID?{id:mailID}:{}),from:task?actor:envelope?.from||actor,to:task?(args?.target||(result?.agent_link?.assistant_id?{node_id:result.agent_link.node_id,kind:'assistant',agent_id:result.agent_link.assistant_id}:result?.address)):envelope?.to||args?.to,text:typeof args?.text==='string'?args.text:null}]};
}
