import {activeTool,toolFailure,toolForest,toolResult} from './tool-calls.mjs';

const identity=(turn,id)=>JSON.stringify([turn||'',id]);
const unsuccessful=tool=>['failed','denied','unknown','stopped'].includes(tool.phase);

function summarize(tools){
  const active=tools.filter(activeTool),failure=tools.find(tool=>tool.failurePhase||unsuccessful(tool)),starts=tools.map(tool=>tool.started_at_ms).filter(Boolean);
  return {
    phase:active.some(tool=>tool.phase==='waiting_approval')?'waiting_approval':active.length?'running':failure?(unsuccessful(failure)?failure.phase:'failed'):'completed',
    diagnostic:failure?toolFailure(failure):'',
    failurePhase:failure?.failurePhase||failure?.phase,
    failureID:failure?.failureID||failure?.id,
    focus:active.findLast(tool=>!['exec','wait'].includes(tool.name))||tools.findLast(tool=>!['exec','wait'].includes(tool.name)),
    started_at_ms:starts.length?Math.min(...starts):0,
    finished_at_ms:active.length?undefined:Math.max(...tools.map(tool=>tool.finished_at_ms||0)),
  };
}

const processIdentity=(tool,id)=>JSON.stringify([tool.turn_id||'',tool.target?.runtime_id||'',id]);
const sessionAction=tool=>!!(tool.args?.chars||tool.args?.interrupt||tool.args?.terminate||tool.args?.close_stdin||tool.args?.rows||tool.args?.cols);

// Fold receipts before building the composition forest, including commands inside
// exec. Session ownership is fenced by turn and execution machine.
function projectProcesses(messages,activeTurn){
  const runs=new Map(),sessions=new Map(),receipts=new Map();
  for(const message of messages){
    const tool=message.tool;if(!tool)continue;
    if(tool.name==='exec_command'){
      const run={message,calls:[tool]};runs.set(message,run);
      const id=toolResult(tool)?.session_id;if(id)sessions.set(processIdentity(tool,id),run);
    }else if(tool.name==='write_stdin'){
      const run=sessions.get(processIdentity(tool,tool.args?.session_id));
      if(run){run.calls.push(tool);receipts.set(message,run);}
    }
  }
  return messages.flatMap(message=>{
    const receipt=receipts.get(message);
    if(receipt){
      // Input/control actions and exact approvals remain real, inspectable calls.
      if(sessionAction(message.tool)||message.tool.phase==='waiting_approval')return [{...message,tool:{...message.tool,parent_id:receipt.message.tool.id}}];
      return [];
    }
    const run=runs.get(message);
    if(!run){
      const tool=message.tool;
      if(tool?.name==='write_stdin'&&tool.phase==='completed'&&toolResult(tool)?.session_id)return [{...message,tool:{...tool,phase:tool.turn_id===activeTurn?'running':'unknown',finished_at_ms:tool.turn_id===activeTurn?undefined:tool.finished_at_ms}}];
      return [message];
    }
    const last=run.calls.at(-1),result=toolResult(last),tools=run.calls.map(tool=>{
      const value=toolResult(tool);
      return tool.phase==='completed'&&value?.exit_code!=null&&value.exit_code!==0?{...tool,phase:'failed'}:tool;
    });
    if(last.phase==='completed'&&result?.session_id)tools.push({...last,phase:last.turn_id===activeTurn?'running':'unknown',finished_at_ms:undefined});
    const state=summarize(tools);
    // A folded poll has no row to locate; its diagnostic opens the original command.
    if(state.failureID&&!run.calls.some(tool=>tool.id===state.failureID&&sessionAction(tool)))state.failureID=message.tool.id;
    const output=run.calls.flatMap(tool=>{
      const chunks=tool.output||[],value=toolResult(tool);
      return [...chunks,...['stdout','stderr'].filter(stream=>value?.[stream]&&!chunks.some(chunk=>chunk.stream===stream)).map(stream=>({stream,text:value[stream]}))];
    });
    const finalResult=result&&typeof result==='object'?{...result,truncated:run.calls.some(tool=>toolResult(tool)?.truncated)}:result;
    return [{...message,tool:{...message.tool,...state,process:{calls:run.calls,result:finalResult},output}}];
  });
}

// Presentation only: receipts stay unchanged, and ownership never crosses turns.
export function projectScriptRuns(messages,{activeTurn=null}={}){
  messages=projectProcesses(messages,activeTurn);
  const forest=toolForest(messages),runs=new Map(),jobs=new Map(),waits=new Set();
  const descendants=entry=>entry.children.flatMap(child=>[child.message.tool,...descendants(child)]);
  for(const message of messages){
    const tool=message.tool;if(!tool||tool.parent_id)continue;
    if(tool.name==='exec'){
      const run={calls:[tool],children:descendants(forest.byID.get(`${tool.turn_id}:${tool.id}`))};
      runs.set(message,run);
      const job=toolResult(tool)?.exec_id;if(job)jobs.set(identity(tool.turn_id,job),run);
    }else if(tool.name==='wait'){
      const run=jobs.get(identity(tool.turn_id,tool.args?.exec_id));
      if(run){run.calls.push(tool);waits.add(message);}
    }
  }
  for(const run of runs.values()){
    const last=run.calls.at(-1),result=toolResult(last),pending=last.phase==='completed'&&result?.completed===false&&result?.exec_id;
    const tools=[...run.calls,...run.children];
    // A yielded invocation is still executing; after the turn ends its missing
    // final receipt is unconfirmed rather than a successful completion.
    if(pending)tools.push({...last,phase:last.turn_id===activeTurn?'running':'unknown',finished_at_ms:undefined});
    run.state=summarize(tools);
    run.output=run.calls.map(tool=>toolResult(tool)?.output).filter(value=>typeof value==='string').join('');
  }
  // All surviving calls stay in transcript order. The shared work-record window
  // owns the only visible group; parent_id remains evidence, never a second list.
  return messages.filter(message=>!waits.has(message)).map(message=>{
    const run=runs.get(message);
    return run?{...message,tool:{...message.tool,...run.state,script:{...run.state,calls:run.calls,output:run.output}}}:message;
  });
}
