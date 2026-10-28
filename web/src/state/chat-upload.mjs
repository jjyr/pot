// Shared idempotent original-byte upload. The caller owns scope, cancellation,
// capability validation and persistence; this helper never selects a recipient.
export async function uploadChatOriginal(client,attachment,file,scope,{signal,check=()=>{},progress=()=>{}}={}){
  const send=async body=>{check();const value=await client.request('/api/v1/attachments',{...scope,method:'POST',body,signal});check();return value;};
  let status=await send({action:'begin',id:attachment.id,name:attachment.name,mime_type:attachment.mime_type,size:attachment.size});
  if(!status.ready){
    if(!file)throw Object.assign(Error('outbox_original_missing'),{outboxProblem:'originalMissing'});
    for(let offset=status.offset;offset<file.size;){
      const bytes=new Uint8Array(await file.slice(offset,offset+192*1024).arrayBuffer());check();
      let binary='';for(let i=0;i<bytes.length;i+=8192)binary+=String.fromCharCode(...bytes.subarray(i,i+8192));
      status=await send({action:'chunk',id:attachment.id,offset,data:btoa(binary)});
      if(status.offset<=offset||status.offset>file.size)throw Error('Invalid attachment upload offset');
      offset=status.offset;progress(offset/file.size);
    }
    status=await send({action:'finish',id:attachment.id});
  }
  if(!status.ready||JSON.stringify(status.attachment)!==JSON.stringify(attachment)){
    if(!status.ready)throw Error('Attachment upload did not finish');
    // The receipt owns canonical metadata; IDs, original bytes and scope stay fixed.
    if(status.attachment?.id!==attachment.id||status.attachment.size!==attachment.size||status.attachment.mime_type!==attachment.mime_type)throw Error('Attachment upload identity changed');
  }
  return status.attachment;
}
