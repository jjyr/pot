import {activeTool} from './tool-calls.mjs';
// Reply identities survive the optimistic send, its receipt, and the first
// streamed assistant message. Transport loss never declares execution failed.
export function projectChatReplies(snapshot, send, {offline=false}={}) {
  const messages=snapshot?.messages||[],inputs=new Map(),replyInputs=new Map(),latestReplies=new Map(),firstReplies=new Set(),rows=[];
  let currentInput='';
  for(const message of messages){
    if((message.role==='user'&&!message.steering)||message.role==='system'){
      inputs.set(message.id,message);currentInput=message.id;
    }
    if(message.role==='assistant'){const input=message.reply_to||currentInput;replyInputs.set(message.id,input);latestReplies.set(input,message.id);}
  }
  for(const message of messages){
    if(message.context_only&&message.role!=='tool' || message.role==='system'&&message.request_id?.startsWith('pot-result-'))continue;
    const row={...message,viewKey:message.request_id?`input:${message.request_id}`:message.id};
    if(message.role==='assistant'){
      const input=inputs.get(replyInputs.get(message.id));
      // Tool-only model rounds have no prose to keep. The next output adopts
      // their waiting bubble instead of leaving empty cards in the transcript.
      if(!message.text.trim()&&message.status==='complete'&&(!snapshot.running||input?.id!==snapshot.turn_id||latestReplies.get(input?.id)!==message.id))continue;
      row.inputID=input?.id||message.reply_to;
      if(input&&!firstReplies.has(input.id)){
        row.viewKey=`reply:${input.request_id||input.id}`;firstReplies.add(input.id);
      }
      row.phase=message.status==='failed'?'failed':message.status==='cancelled'?'stopped':message.status==='streaming'?(message.text?'streaming':'thinking'):'complete';
      if(offline&&snapshot.running&&row.inputID===snapshot.turn_id&&['thinking','streaming'].includes(row.phase))row.phase='reconnecting';
      else if(snapshot.running&&row.inputID===snapshot.turn_id&&['thinking','streaming'].includes(row.phase)&&snapshot.turn?.paused)row.phase='paused';
      else if(snapshot.running&&row.inputID===snapshot.turn_id&&['thinking','streaming'].includes(row.phase)&&snapshot.turn?.state==='waiting')row.phase='waiting';
    }
    rows.push(row);
  }
  const input=inputs.get(snapshot?.turn_id);
  const active=rows.filter(row=>row.role==='assistant'&&row.inputID===input?.id);
  if(snapshot?.running&&input&&!active.some(row=>['thinking','streaming','reconnecting','paused','waiting','failed'].includes(row.phase))){
    const phase=offline?'reconnecting':snapshot.turn?.paused?'paused':snapshot.turn?.state==='waiting'?'waiting':'thinking';
    if(active.length)active.at(-1).phase=phase;
    else rows.push({id:`waiting:${input.id}`,viewKey:`reply:${input.request_id||input.id}`,role:'assistant',text:'',status:'streaming',phase,inputID:input.id});
  }
  if(send&&!send.request.user_message_id&&!send.request.retry_user_message_id&&!messages.some(message=>message.request_id===send.request.request_id)){
    const request=send.request;
    rows.push({id:`pending:${request.request_id}`,viewKey:`input:${request.request_id}`,role:'user',text:request.text,attachments:request.attachments,references:request.references,status:'accepted'});
    rows.push({id:`pending-reply:${request.request_id}`,viewKey:`reply:${request.request_id}`,role:'assistant',text:'',status:'accepted',phase:send.phase==='failed'?'failed':send.phase==='uncertain'||offline?'reconnecting':send.phase,error:send.detail||'',local:true});
  }
  if(snapshot?.running&&!offline&&!snapshot.turn?.paused&&snapshot.turn?.state!=='waiting'){
    const start=messages.findIndex(message=>message.id===snapshot.turn_id);
    const tools=start<0?[]:messages.slice(start+1).filter(message=>activeTool(message.tool));
    const reply=rows.findLast(row=>row.role==='assistant'&&row.inputID===snapshot.turn_id);
    if(reply&&tools.length)reply.phase=tools.every(message=>message.tool.phase==='waiting_approval')?'approval':'tools';
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
