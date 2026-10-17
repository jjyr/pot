// Reply identities survive the optimistic send, its receipt, and the first
// streamed assistant message. Transport loss never declares execution failed.
export function projectChatReplies(snapshot, send, {offline=false}={}) {
  const messages=snapshot?.messages||[],inputs=new Map(),firstReplies=new Set(),rows=[];
  for(const message of messages){
    if((message.role==='user'&&!message.steering)||message.role==='system'){
      inputs.set(message.id,message);
    }
  }
  let currentInput='';
  for(const message of messages){
    if((message.role==='user'&&!message.steering)||message.role==='system')currentInput=message.id;
    if(message.role==='system'&&message.request_id?.startsWith('pot-result-'))continue;
    const row={...message,viewKey:message.request_id?`input:${message.request_id}`:message.id};
    if(message.role==='assistant'){
      const input=inputs.get(message.reply_to||currentInput);
      row.inputID=input?.id;
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
  if(send&&!send.request.user_message_id&&!messages.some(message=>message.request_id===send.request.request_id)){
    const request=send.request;
    rows.push({id:`pending:${request.request_id}`,viewKey:`input:${request.request_id}`,role:'user',text:request.text,status:'accepted'});
    rows.push({id:`pending-reply:${request.request_id}`,viewKey:`reply:${request.request_id}`,role:'assistant',text:'',status:'accepted',phase:send.phase==='failed'?'failed':send.phase==='uncertain'||offline?'reconnecting':send.phase,error:send.detail||'',local:true});
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
