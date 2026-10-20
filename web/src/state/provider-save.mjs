// A failed second write must never create another connection. Only an explicit
// provider acknowledgement advances identity/revision; reminder conflicts keep
// the revision captured with the editor draft.
export function createProviderSave({profile,save,configure}) {
 let saved=profile,signature='',result;
 return async(values,context)=>{
  const {allowance_enabled,allowance_threshold,allowance_assistant,...connection}=values;
  const next=JSON.stringify(connection);
  if(signature!==next){result=await save(connection,context,saved);saved=result?.profile||result;signature=next;}
  if(configure){try{await configure(saved,{enabled:allowance_enabled,threshold:allowance_threshold,assistant:allowance_assistant},context);}catch(reason){throw Object.assign(new Error('provider_reminder_partial_save'),{uiKey:'providers.partialSave',diagnostic:reason.message});}}
  return result;
 };
}

// An interrupted acknowledgement may still have committed. Confirm only the
// exact submitted intent at a newer revision; unrelated concurrent edits remain
// conflicts and never replace the owner's draft.
export async function saveAllowanceConfiguration({request,scope,profileID,values,revision}) {
 const path='/api/v1/providers/allowances',read=()=>request(path+'?profile_id='+encodeURIComponent(profileID),scope);
 try{const result=await request(path,{...scope,method:'POST',body:{profile_id:profileID,action:'configure',...values,revision}});return result?.monitor?result:await read();}
 catch(reason){try{const current=await read(),monitor=current.monitor;if(monitor?.revision>revision&&monitor.enabled===values.enabled&&Number(monitor.threshold)===Number(values.threshold)&&monitor.assistant===values.assistant)return current;}catch{}throw reason;}
}
