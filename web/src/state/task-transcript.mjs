import {activeTool,applyToolUpdate} from './tool-calls.mjs';
import {projectResourceMessages} from './display-resources.mjs';
export function createTaskTranscript(){return {cursor:0,messages:[],active:null,tools:new Map(),inputs:new Map()};}
export function appendTaskEvents(state,events){
  for(const row of events||[]){
    if(!Number.isSafeInteger(row.sequence)||row.sequence<=state.cursor)continue;
    const update=row.event?.update;if(!update||typeof update.type!=='string')throw Error('Invalid Task event');
    state.cursor=row.sequence;
    switch(update.type){
      case'input_applied':if(!state.inputs.has(update.id))state.inputs.set(update.id,row.sequence);break;
      case'message_started':state.active={id:`message-${row.sequence}`,sequence:row.sequence,role:'assistant',text:'',phase:'thinking'};state.messages.push(state.active);break;
      case'text_delta':if(!state.active){state.active={id:`message-${row.sequence}`,sequence:row.sequence,role:'assistant',text:''};state.messages.push(state.active);}state.active.text+=update.text||'';state.active.phase='streaming';break;
      case'message_finished':if(state.active){if(update.text!==undefined)state.active.text=update.text;state.active.phase='complete';}state.active=null;break;
      case'tool_started':{const tool=applyToolUpdate({},update);tool.turn_id=row.event.turn_id||'';const message={id:`tool-${row.sequence}`,sequence:row.sequence,role:'tool',text:'',tool};state.tools.set(update.id,message);state.messages.push(message);break;}
      case'tool_state':case'tool_output':case'tool_finished':{const message=state.tools.get(update.id);if(message)applyToolUpdate(message.tool,update);break;}
      case'turn_finished':for(const message of state.tools.values())if(activeTool(message.tool))applyToolUpdate(message.tool,update);state.tools.clear();if(state.active)state.active.phase=update.outcome?.kind==='success'?'complete':'stopped';state.active=null;if(update.outcome?.error&&update.outcome.kind!=='cancelled')state.messages.push({id:`outcome-${row.sequence}`,sequence:row.sequence,role:'system',text:update.outcome.error});break;
    }
  }
  return state;
}

// Applied inputs use the Runner's durable bookmark, independent of HTTP fetch
// order or clocks on separate machines. Unapplied inputs remain at the tail.
export function projectTaskMessages(state,inputs,record,{offline=false}={}){
  const phase=record.info?.phase,active=['queued','running','waiting','waiting_approval'].includes(phase),rows=[];
  for(const message of state.messages){
    if(message.role==='assistant'&&!message.text&&message!==state.active)continue;
    if(message.role==='system'){const previous=rows.at(-1);if(previous?.role==='assistant'&&!previous.error){previous.phase='failed';previous.error=message.text;}else rows.push({...message,role:'assistant',text:'',phase:'failed',error:message.text});continue;}
    let projected=message.phase;
    if(message===state.active){if(!active)projected=phase==='complete'?'complete':'stopped';else if(offline)projected='reconnecting';else if(record.info?.paused)projected='paused';else if(phase==='waiting')projected='waiting';}
    rows.push({...message,phase:projected});
  }
  const history=(inputs||[]).filter(input=>input.kind!=='input');
  history.forEach((input,index)=>{const bookmark=state.inputs.get(input.request_id),pending=input.state==='pending',sequence=bookmark??(pending||input.state==='retained'?state.cursor+1:-1);rows.push({id:`input-${input.request_id}`,sequence,order:index,role:input.kind==='user_guidance'?'user':'reference',text:input.text,input});});
  rows.sort((a,b)=>(a.sequence-b.sequence)||((a.order||0)-(b.order||0)));
  const finalText=record.result?.text||'',last=rows.findLast(row=>row.role==='assistant'&&row.text&&!row.error);
  if(finalText&&last?.text!==finalText)rows.push({id:'task-result',role:'assistant',text:finalText,phase:'complete'});
  const diagnostic=record.error||(record.result?.outcome?.kind!=='cancelled'?record.result?.outcome?.error:'');
  if(diagnostic&&!rows.some(row=>row.error===diagnostic)){const previous=rows.at(-1);if(previous?.role==='assistant'&&!previous.error){previous.phase='failed';previous.error=diagnostic;}else rows.push({id:'task-error',role:'assistant',text:'',phase:'failed',error:diagnostic});}
  if(!rows.length&&active)rows.push({id:'task-activity',role:'assistant',text:'',phase:offline?'reconnecting':record.info?.paused?'paused':phase==='waiting'?'waiting':'thinking'});
  return projectResourceMessages(rows);
}
