import assert from 'node:assert/strict';
import {projectScriptRuns} from '../../web/src/state/script-runs.mjs';
import {toolForest} from '../../web/src/state/tool-calls.mjs';

const call=(id,name,args={},extra={})=>({id:`message-${id}`,viewKey:`view-${id}`,role:'tool',tool:{id,turn_id:'turn',name,args,phase:'completed',started_at_ms:100,finished_at_ms:200,...extra}});
const exec=(id,extra={})=>call(id,'exec',{code:'await work()'},{result:{completed:false,exec_id:`job-${id}`,output:'start\n'},...extra});
const wait=(id,job,result,extra={})=>call(id,'wait',{exec_id:job},{result,...extra});
const groups=rows=>rows.filter(row=>row.tool?.name==='script_group');
const execution=(rows,id)=>rows.find(row=>row.tool?.id===id).tool;

const first=exec('first'),poll=wait('poll','job-first',{completed:false,exec_id:'job-first',output:'middle\n'}),done=wait('done','job-first',{completed:true,output:'end\n'},{started_at_ms:400,finished_at_ms:600});
const original=structuredClone([first,poll,done]);
let rows=projectScriptRuns([first,poll],{activeTurn:'turn'});
assert.equal(groups(rows).length,1);
assert.equal(groups(rows)[0].tool.phase,'running','A yielded invocation and yielded wait remain running');
assert.equal(execution(rows,'first').script.output,'start\nmiddle\n');
assert.equal(rows.some(row=>row.tool?.name==='wait'),false);
const stableID=groups(rows)[0].id;
rows=projectScriptRuns([first,poll,done]);
assert.equal(groups(rows)[0].id,stableID);
assert.equal(groups(rows)[0].tool.phase,'completed');
assert.equal(groups(rows)[0].tool.started_at_ms,100);
assert.equal(groups(rows)[0].tool.finished_at_ms,600,'Elapsed time includes gaps between waits');
assert.equal(execution(rows,'first').script.output,'start\nmiddle\nend\n');
assert.deepEqual(execution(rows,'first').script.calls,original.map(row=>row.tool),'Raw receipts retain their original phases and results');
assert.deepEqual([first,poll,done],original,'Projection never changes durable records');
assert.equal(groups(projectScriptRuns([first]))[0].tool.phase,'unknown','Ended turns cannot leave yielded scripts running forever');

const second=exec('second',{started_at_ms:150,finished_at_ms:250}),secondDone=wait('second-done','job-second',{completed:true,output:'second\n'},{finished_at_ms:500});
rows=projectScriptRuns([first,second,secondDone,done]);
assert.equal(groups(rows).length,1,'Parallel scripts share one history');
assert.equal(groups(rows)[0].tool.finished_at_ms,600,'Parallel durations are never summed');
assert.equal(execution(rows,'second').script.output,'start\nsecond\n','Interleaved waits keep their execution ownership');
assert.equal(toolForest(rows).roots[0].children.length,2);

const prose={id:'prose',role:'assistant',text:'Next step'},guidance={id:'guide',role:'user',text:'Use another option'},empty={id:'empty',role:'assistant',text:''};
assert.equal(groups(projectScriptRuns([first,empty,second])).length,1);
for(const boundary of [prose,guidance,call('patch','apply_patch')])assert.equal(groups(projectScriptRuns([first,boundary,second])).length,2);
rows=projectScriptRuns([first,prose,done,second,secondDone]);
assert.equal(groups(rows).length,2);
assert.equal(execution(rows,'first').phase,'completed','A wait beyond prose still updates its original execution');

const other=exec('other',{turn_id:'other',result:{completed:false,exec_id:'job-first',output:'other\n'}}),otherDone=wait('other-done','job-first',{completed:true,output:'other end'},{turn_id:'other'});
rows=projectScriptRuns([first,other,done,otherDone]);
assert.equal(groups(rows).length,2);
assert.equal(execution(rows,'first').script.output,'start\nend\n');
assert.equal(execution(rows,'other').script.output,'other\nother end','Job IDs cannot associate across turns');
const orphan=wait('orphan','missing',{completed:true,output:'orphan'});
assert.equal(projectScriptRuns([orphan])[0],orphan,'Unmatched waits stay visible');

const failure=call('child','exec_command',{cmd:'check'},{parent_id:'first',phase:'failed',result:{exit_code:1,stderr:'Check failed'},finished_at_ms:300});
rows=projectScriptRuns([first,failure,done,second],{activeTurn:'turn'});
assert.equal(groups(rows)[0].tool.phase,'running','Later work continues after an earlier failure');
assert.equal(groups(rows)[0].tool.diagnostic,'Check failed','Failure remains visible during later work');
assert.equal(groups(rows)[0].tool.failureID,'child');
rows=projectScriptRuns([first,failure,done,second,secondDone]);
assert.equal(groups(rows)[0].tool.phase,'failed','Later success does not erase an earlier failure');
assert.equal(groups(rows)[0].tool.diagnostic,'Check failed');
const pending=call('pending','exec_command',{cmd:'write'},{parent_id:'first',phase:'waiting_approval',finished_at_ms:undefined});
rows=projectScriptRuns([first,pending],{activeTurn:'turn'});
assert.equal(groups(rows)[0].tool.phase,'waiting_approval');
assert.equal(execution(rows,'first').phase,'waiting_approval');
assert.equal(toolForest(rows).roots[0].children[0].children[0].message.tool.id,'pending','Approvals retain their real child under the execution and group');
const denied=wait('denied','job-first',{error:'Wait denied'},{phase:'denied'});
assert.equal(groups(projectScriptRuns([first,denied]))[0].tool.phase,'denied');
console.log('Script execution projection contracts passed');
