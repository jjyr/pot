export function createTaskTranscript(){return {cursor:0,messages:[],active:null,tools:new Map()};}
export function appendTaskEvents(state,events){
  for(const row of events||[]){
    if(!Number.isSafeInteger(row.sequence)||row.sequence<=state.cursor)continue;
    const update=row.event?.update;if(!update||typeof update.type!=='string')throw Error('Invalid Task event');
    state.cursor=row.sequence;
    switch(update.type){
      case'message_started':state.active={id:`message-${row.sequence}`,role:'assistant',text:''};state.messages.push(state.active);break;
      case'text_delta':if(!state.active){state.active={id:`message-${row.sequence}`,role:'assistant',text:''};state.messages.push(state.active);}state.active.text+=update.text||'';break;
      case'message_finished':state.active=null;break;
      case'tool_started':{const message={id:`tool-${row.sequence}`,role:'tool',name:update.name,text:JSON.stringify(update.args??{},null,2)};state.tools.set(update.id,message);state.messages.push(message);break;}
      case'tool_output':case'tool_finished':{const tool=state.tools.get(update.id);if(tool){if(update.text)tool.text+='\n'+update.text;if(update.result!==undefined)tool.text+='\n'+JSON.stringify(update.result,null,2);}break;}
      case'turn_finished':state.active=null;if(update.outcome?.error)state.messages.push({id:`outcome-${row.sequence}`,role:'system',text:update.outcome.error});break;
    }
  }
  return state;
}
