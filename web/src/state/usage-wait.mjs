import {translate} from '../locales/messages.mjs';

// Authority-only snapshot: normalization must not confirm a Chat session or
// commit a latest window as a side effect of a Usage action.
export function captureUsageWait(client,scope,waiting,{current=()=>true,locale=()=> 'en'}={}){
  scope={space:scope.space,owner:scope.owner};
  const generation=client.connectionGeneration,boot=client.runtime?.boot_id,runtime=client.runtime?.runtime_id;
  const control=()=>client.spaces?.find(item=>item.id===scope.space)?.control_id;
  const controlID=control();
  const check=()=>{
    if(!current()||(client.production&&(!runtime||!boot))||!scope.space||!scope.owner||!controlID||control()!==controlID||client.runtime?.runtime_id!==runtime||client.runtime?.boot_id!==boot||client.connectionGeneration!==generation||client.operationScopeReady?.(scope)===false||client.sessionReady?.(scope)===false||client.supports?.('chat.cancel',scope)===false)
      throw Object.assign(Error(translate(locale(),'identityChanged')),{uiKey:'identityChanged'});
  };
  const turn=waiting.turn;
  return async(requestID,{current:requestCurrent=()=>true}={})=>{
    const valid=()=>{check();if(!requestCurrent())throw Object.assign(Error(translate(locale(),'identityChanged')),{uiKey:'identityChanged'});};
    valid();const snapshot=await client.snapshot(scope);valid();
    const expectedOwner=JSON.stringify([scope.space,scope.owner]);
    const message=snapshot.messages?.find(message=>message.id===snapshot.turn?.user_message_id);
    if((client.production&&snapshot.owner_id!==expectedOwner)||!message||message.request_id!==turn)
      throw Object.assign(Error(translate(locale(),'usage.waitChanged')),{uiKey:'usage.waitChanged'});
    return client.request('/api/v1/turn/cancel',{...scope,expectedControl:controlID,session:true,requiredCapabilities:['chat.cancel'],method:'POST',body:{request_id:requestID,user_message_id:message.id}});
  };
}
