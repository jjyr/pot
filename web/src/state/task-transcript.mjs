import {activeTool,applyToolUpdate} from './tool-calls.mjs';
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
      case'tool_started':{const tool=applyToolUpdate({},update);tool.turn_id=row.event.turn_id||'';const message={id:`tool-${row.sequence}`,role:'tool',text:'',tool};state.tools.set(update.id,message);state.messages.push(message);break;}
      case'tool_state':case'tool_output':case'tool_finished':{const message=state.tools.get(update.id);if(message)applyToolUpdate(message.tool,update);break;}
      case'turn_finished':for(const message of state.tools.values())if(activeTool(message.tool))applyToolUpdate(message.tool,update);state.tools.clear();state.active=null;if(update.outcome?.error)state.messages.push({id:`outcome-${row.sequence}`,role:'system',text:update.outcome.error});break;
    }
  }
  return state;
}
