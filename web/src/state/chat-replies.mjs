import {activeTool} from './tool-calls.mjs';

// Outcome envelopes are protocol data only when the receiver independently
// confirms the same terminal state and diagnostic for this exact input.
function replyText(text, lifecycle) {
  if(!['failed','cancelled','unknown'].includes(lifecycle.state))return text;
  try{
    const outcome=JSON.parse(text);
    if(outcome&&Object.keys(outcome).length===3&&['failed','cancelled'].includes(outcome.state)&&
      (outcome.state===lifecycle.state||lifecycle.state==='unknown')&&typeof outcome.error==='string'&&
      outcome.error===(lifecycle.error||'')&&typeof outcome.partial_output==='string')return outcome.partial_output;
  }catch{}
  return text;
}
// Reply identities survive the optimistic send, its receipt, and the first
// streamed assistant message. Transport loss never declares execution failed.
export function projectChatReplies(snapshot, send, {offline=false}={}) {
  const messages=snapshot?.messages||[],inputs=new Map(),replyInputs=new Map(),firstReplies=new Set(),rows=[];
  let currentInput='';
  for(const message of messages){
    if((message.role==='user'&&!message.steering)||message.role==='system'&&!message.compaction){
      inputs.set(message.id,message);currentInput=message.id;
    }
    if(message.role==='assistant'){const input=message.reply_to||currentInput;replyInputs.set(message.id,input);}
  }
  for(const message of messages){
    if(message.context_only&&message.role!=='tool' || message.role==='system'&&message.request_id?.startsWith('pot-result-'))continue;
    const row={...message,viewKey:message.request_id?`input:${message.request_id}`:message.id};
    if(message.role==='assistant'){
      const input=inputs.get(replyInputs.get(message.id));
      // Tool-only model rounds have no prose to keep. The next output adopts
      // their waiting bubble instead of leaving empty cards in the transcript.
      if(!message.text.trim()&&!message.presentation&&message.status==='complete')continue;
      row.inputID=input?.id||message.reply_to;
      if(input&&!firstReplies.has(input.id)){
        row.viewKey=`reply:${input.request_id||input.id}`;firstReplies.add(input.id);
      }
      row.phase=message.status==='failed'?'failed':message.status==='cancelled'?'stopped':message.status==='streaming'?(message.text?'streaming':'thinking'):'complete';
    }
    rows.push(row);
  }
  if(send&&!send.request.user_message_id&&!send.request.retry_user_message_id&&!messages.some(message=>message.request_id===send.request.request_id)){
    const request=send.request;
    rows.push({id:`pending:${request.request_id}`,viewKey:`input:${request.request_id}`,role:'user',text:request.text,attachments:request.attachments,references:request.references,status:'accepted'});
    rows.push({id:`pending-reply:${request.request_id}`,viewKey:`reply:${request.request_id}`,role:'assistant',text:'',status:'accepted',phase:send.phase==='failed'?'failed':send.phase==='uncertain'||offline?'reconnecting':send.phase,error:send.detail||'',local:true});
  }
  // The same execution contract applies to every endpoint. A persisted user
  // message settles sending; only its receiver-owned lifecycle settles waiting.
  for(const lifecycle of snapshot?.replies||[]){
    const input=inputs.get(lifecycle.input_id);if(!input)continue;
    const outputs=rows.filter(row=>row.role==='assistant'&&row.inputID===input.id);
    for(const output of outputs)output.text=replyText(output.text,lifecycle);
    const terminal=['completed','failed','cancelled','unknown'].includes(lifecycle.state);
    if(terminal&&!outputs.length&&!rows.some(row=>row.id===input.id))continue;
    let reply=outputs.at(-1);
    if(!reply){
      reply={id:`waiting:${input.id}`,viewKey:`reply:${input.request_id||input.id}`,role:'assistant',text:'',status:terminal?'complete':'streaming',inputID:input.id};rows.push(reply);
    }
    const phase={pending:'awaiting',queued:'queued',running:reply.text?'streaming':'thinking',waiting:'waiting',paused:'paused',cancelling:'cancelling',completed:reply.text||reply.presentation?'complete':'empty',failed:'failed',cancelled:'stopped',unknown:'unknown'}[lifecycle.state];
    if(!terminal&&(offline||lifecycle.uncertain))reply.phase='reconnecting';
    else {
      const tools=lifecycle.state==='running'?messages.filter(message=>activeTool(message.tool)&&message.tool.turn_id===(input.request_id||input.id)):[];
      reply.phase=tools.length?tools.every(message=>message.tool.phase==='waiting_approval')?'approval':'tools':phase;
    }
    if(lifecycle.error)reply.error=lifecycle.error;
  }
  return rows;
}

export function replyFailureKind(detail='') {
  const status=Number(detail.match(/\bHTTP\s+(\d{3})\b/i)?.[1]);
  if([400,401,403,404,422].includes(status))return 'configuration';
  if(status===429)return 'limit';
  if(status>=500)return 'service';
  return 'unknown';
}
