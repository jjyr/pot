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

// Presentation only: receipts stay unchanged, and ownership never crosses turns.
export function projectScriptRuns(messages,{activeTurn=null}={}){
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
  const rows=[];let group=null;
  for(const message of messages){
    if(waits.has(message))continue;
    const run=runs.get(message);
    if(run){
      if(!group||group.tool.turn_id!==message.tool.turn_id){
        const id=`script-group:${message.tool.id}`;
        group={...message,id,viewKey:`script-group:${message.viewKey||message.id}`,tool:{id,turn_id:message.tool.turn_id,name:'script_group',script:{runs:[]}}};rows.push(group);
      }
      const projected={...message,tool:{...message.tool,...run.state,parent_id:group.tool.id,script:{...run.state,calls:run.calls,output:run.output}}};
      group.tool.script.runs.push(projected.tool);rows.push(projected);
    }else{
      rows.push(message);
      // Children and empty model-round placeholders do not interrupt a block.
      if(!message.tool?.parent_id&&!(message.role==='assistant'&&!message.text&&!message.error))group=null;
    }
  }
  for(const row of rows)if(row.tool?.name==='script_group'){
    const tools=row.tool.script.runs;
    Object.assign(row.tool,summarize(tools));
    const active=tools.filter(activeTool);
    row.tool.script.focus=(active.length?active:tools).findLast(tool=>tool.script.focus)?.script.focus;
    const focus=row.tool.script.focus;
    if(focus)for(const key of ['target','workdir','execution_mode'])row.tool[key]=focus[key];
  }
  return rows;
}
