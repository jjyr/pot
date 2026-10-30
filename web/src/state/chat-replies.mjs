import {activeTool,toolFailure} from './tool-calls.mjs';

// Archive bookkeeping stays in model history; only its exceptional outcomes
// need a chat card. Unknown/interrupted calls and approval requests stay visible.
function routineTaskDisposition(message) {
  const tool=message.tool;
  return message.role==='tool'&&tool?.name==='set_task_dispositions'&&
    ['preparing','running','completed'].includes(tool.phase)&&
    !['failed','cancelled'].includes(message.status)&&!message.error&&!toolFailure(tool);
}

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
// Execution activity has its own stable identity, independent of model rounds.
// Prose/tool ordering stays chronological; transport loss never ends execution.
export function projectChatReplies(snapshot, send, {offline=false}={}) {
  const messages=snapshot?.messages||[],inputs=new Map(),replyInputs=new Map(),rows=[];
  const positions=new Map(messages.map((message,index)=>[message.id,index])),anchors=new Map(),turnInputs=new Map();
  const lifecycleInputs=new Set((snapshot?.replies||[]).map(reply=>reply.input_id));
  let currentInput='';
  for(const message of messages){
    if(!message.local_only&&((message.role==='user'&&!message.steering)||(message.role==='system'&&!message.compaction))){
      inputs.set(message.id,message);currentInput=message.id;
      anchors.set(message.id,positions.get(message.id));turnInputs.set(message.request_id||message.id,message.id);
    }
    if(message.role==='assistant'){const input=message.reply_to||currentInput;replyInputs.set(message.id,input);}
  }
  for(const message of messages){
    const input=message.role==='assistant'?replyInputs.get(message.id):message.role==='tool'?turnInputs.get(message.tool?.turn_id):null;
    if(input)anchors.set(input,Math.max(anchors.get(input)??-1,positions.get(message.id)));
  }
  for(const message of messages){
    if(message.context_only&&message.role!=='tool')continue;
    if(message.role==='system'&&message.request_id?.startsWith('pot-result-')&&!message.correspondence?.length)continue;
    if(routineTaskDisposition(message))continue;
    const inputRequest=message.request_id||(message.role==='user'&&message.steering?message.accepted_request_id:undefined);
    const fallbackViewKey=inputRequest?`input:${inputRequest}`:message.id;
    const row={...message,viewKey:message.viewKey||fallbackViewKey};
    if(message.role==='system'&&message.correspondence?.length){
      // Receipt details are a disclosure row, never ordinary user/assistant prose.
      // The authoritative system input stays unchanged in the model transcript.
      row.role='tool';row.text='';row.tool={id:message.id,turn_id:message.request_id,name:'receive_message',args:{messages:message.correspondence},phase:'completed',started_at_ms:message.created_at_ms,finished_at_ms:message.created_at_ms};
    }
    if(message.role==='assistant'){
      row.viewKey=message.viewKey||message.id;
      const input=inputs.get(replyInputs.get(message.id));
      // Empty model rounds do not own activity. Only the receiver lifecycle
      // owns that frame, including between tool completion and the next round.
      if(!message.text.trim()&&!message.presentation&&(lifecycleInputs.has(input?.id||message.reply_to)||message.status==='complete'))continue;
      row.inputID=input?.id||message.reply_to;
      // Historical pages carry durable message outcomes but need not include
      // their receiver lifecycle. Keep those diagnostics on the message; only
      // an available lifecycle takes ownership in its separate activity row.
      if(lifecycleInputs.has(row.inputID))row.activity=false;
      row.phase=message.status==='failed'?'failed':message.status==='cancelled'?'stopped':message.status==='streaming'?(message.text?'streaming':'thinking'):'complete';
    }
    rows.push(row);
  }
  if(send&&!send.request.user_message_id&&!send.request.retry_user_message_id&&!messages.some(message=>message.request_id===send.request.request_id)){
    const request=send.request;
    rows.push({id:`pending:${request.request_id}`,viewKey:`input:${request.request_id}`,role:'user',text:request.text,attachments:request.attachments,references:request.references,status:'accepted'});
    rows.push({id:`pending-reply:${request.request_id}`,viewKey:`activity:${request.request_id}`,role:'assistant',text:'',status:'accepted',phase:send.phase==='failed'?'failed':send.phase==='uncertain'||offline?'reconnecting':send.phase,error:send.detail||'',local:true,activity:true});
  }
  // The same execution contract applies to every endpoint. A persisted user
  // message settles sending; only its receiver-owned lifecycle settles waiting.
  for(const lifecycle of snapshot?.replies||[]){
    const input=inputs.get(lifecycle.input_id);if(!input)continue;
    const outputs=rows.filter(row=>row.role==='assistant'&&row.inputID===input.id);
    // Successful execution does not require a user-facing reply. Settling an
    // empty turn removes its activity bubble without inventing assistant text.
    if(lifecycle.state==='completed')continue;
    for(const output of outputs){output.text=replyText(output.text,lifecycle);if(!output.text.trim()&&!output.presentation)rows.splice(rows.indexOf(output),1);}
    const terminal=['completed','failed','cancelled','unknown'].includes(lifecycle.state);
    if(terminal&&!outputs.length&&!rows.some(row=>row.id===input.id))continue;
    const reply={id:`waiting:${input.id}`,viewKey:`activity:${input.request_id||input.id}`,role:'assistant',text:'',status:terminal?'complete':'streaming',inputID:input.id,activity:true};rows.push(reply);
    // Keep the current execution visible at Latest even when another input's
    // output arrives late. Queued/terminal outcomes retain their input context.
    const current=!terminal&&(snapshot.turn_id===input.id||!snapshot.turn_id&&lifecycle.state==='running');
    positions.set(reply.id,current?messages.length+positions.get(input.id)/(messages.length+1):anchors.get(input.id)+0.5);
    const phase={pending:'awaiting',queued:'queued',running:'thinking',waiting:'waiting',paused:'paused',cancelling:'cancelling',completed:'complete',failed:'failed',cancelled:'stopped',unknown:'unknown'}[lifecycle.state];
    if(!terminal&&(offline||lifecycle.uncertain))reply.phase='reconnecting';
    else {
      const tools=lifecycle.state==='running'?messages.filter(message=>activeTool(message.tool)&&message.tool.turn_id===(input.request_id||input.id)):[];
      reply.phase=tools.length?tools.every(message=>message.tool.phase==='waiting_approval')?'approval':'tools':lifecycle.state==='running'&&outputs.some(output=>output.status==='streaming'&&output.text)?'streaming':phase;
    }
    if(lifecycle.error)reply.error=lifecycle.error;
  }
  const order=new Map(rows.map((row,index)=>[row,positions.get(row.id)??messages.length+index]));
  return rows.sort((a,b)=>order.get(a)-order.get(b));
}

export function replyFailureKind(detail='') {
  const status=Number(detail.match(/\bHTTP\s+(\d{3})\b/i)?.[1]);
  if([400,401,403,404,422].includes(status))return 'configuration';
  if(status===429)return 'limit';
  if(status>=500)return 'service';
  return 'unknown';
}
