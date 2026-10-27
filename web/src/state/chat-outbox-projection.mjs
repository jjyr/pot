// Outbox user rows join the same prepared/virtualized message window as durable
// transcript rows. Acceptance alone never invents a model execution or reply.
export function projectOutboxRows(rows,queued,{editing=new Map()}={}){
  const ids=new Set(rows.map(row=>row.request_id||row.accepted_request_id));
  const result=[...rows];
  for(const value of queued){
    if(value.state==='cancelled'||value.state==='accepted'&&value.request.retry_user_message_id||ids.has(value.request.request_id))continue;
    const request=value.request;
    result.push({id:'pending:'+request.request_id,viewKey:'input:'+request.request_id,role:'user',text:request.text,attachments:request.attachments,references:request.references,status:'accepted',queue:value,queueEditing:editing.has(value.sequence)});
  }
  return result;
}
