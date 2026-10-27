import assert from 'node:assert/strict';
import {projectScriptRuns} from '../../web/src/state/script-runs.mjs';
import {toolForest,toolResult} from '../../web/src/state/tool-calls.mjs';

const serialized={result:'{"output":"first"}'};
assert.equal(toolResult(serialized),toolResult(serialized),'Repeated reads reuse a parsed receipt');
serialized.result='{"output":"updated"}';
assert.equal(toolResult(serialized).output,'updated','Replacing a serialized receipt invalidates its parsed result');
serialized.result='raw output';assert.equal(toolResult(serialized),'raw output');

const call=(id,name,args={},extra={})=>({id:`message-${id}`,viewKey:`view-${id}`,role:'tool',tool:{id,turn_id:'turn',name,args,phase:'completed',started_at_ms:100,finished_at_ms:200,...extra}});
const exec=(id,extra={})=>call(id,'exec',{code:'await work()'},{result:{completed:false,exec_id:`job-${id}`,output:'start\n'},...extra});
const wait=(id,job,result,extra={})=>call(id,'wait',{exec_id:job},{result,...extra});
const execution=(rows,id)=>rows.find(row=>row.tool?.id===id).tool;

const first=exec('first'),poll=wait('poll','job-first',{completed:false,exec_id:'job-first',output:'middle\n'}),done=wait('done','job-first',{completed:true,output:'end\n'},{started_at_ms:400,finished_at_ms:600});
const original=structuredClone([first,poll,done]);
let rows=projectScriptRuns([first,poll],{activeTurn:'turn'});
assert.equal(execution(rows,'first').script.output,'start\nmiddle\n');
assert.equal(rows.some(row=>row.tool?.name==='wait'),false);
rows=projectScriptRuns([first,poll,done]);
assert.equal(execution(rows,'first').script.output,'start\nmiddle\nend\n');
assert.deepEqual(execution(rows,'first').script.calls,original.map(row=>row.tool),'Raw receipts retain their original phases and results');
assert.deepEqual([first,poll,done],original,'Projection never changes durable records');

const second=exec('second',{started_at_ms:150,finished_at_ms:250}),secondDone=wait('second-done','job-second',{completed:true,output:'second\n'},{finished_at_ms:500});
rows=projectScriptRuns([first,second,secondDone,done]);
assert.equal(execution(rows,'second').script.output,'start\nsecond\n','Interleaved waits keep their execution ownership');
assert.equal(toolForest(rows).roots.length,2);

const prose={id:'prose',role:'assistant',text:'Next step'},guidance={id:'guide',role:'user',text:'Use another option'},empty={id:'empty',role:'assistant',text:''};
rows=projectScriptRuns([first,prose,done,second,secondDone]);
assert.equal(execution(rows,'first').phase,'completed','A wait beyond prose still updates its original execution');

const other=exec('other',{turn_id:'other',result:{completed:false,exec_id:'job-first',output:'other\n'}}),otherDone=wait('other-done','job-first',{completed:true,output:'other end'},{turn_id:'other'});
rows=projectScriptRuns([first,other,done,otherDone]);
assert.equal(execution(rows,'first').script.output,'start\nend\n');
assert.equal(execution(rows,'other').script.output,'other\nother end','Job IDs cannot associate across turns');
const orphan=wait('orphan','missing',{completed:true,output:'orphan'});
assert.equal(projectScriptRuns([orphan])[0],orphan,'Unmatched waits stay visible');

const failure=call('child','exec_command',{cmd:'check'},{parent_id:'first',phase:'failed',result:{exit_code:1,stderr:'Check failed'},finished_at_ms:300});
rows=projectScriptRuns([first,failure,done,second],{activeTurn:'turn'});
rows=projectScriptRuns([first,failure,done,second,secondDone]);
const pending=call('pending','exec_command',{cmd:'write'},{parent_id:'first',phase:'waiting_approval',finished_at_ms:undefined});
rows=projectScriptRuns([first,pending],{activeTurn:'turn'});
assert.equal(execution(rows,'first').phase,'waiting_approval');
assert.equal(toolForest(rows).roots[0].children[0].message.tool.id,'pending','Approvals retain their real child under the execution and group');
const denied=wait('denied','job-first',{error:'Wait denied'},{phase:'denied'});
console.log('Script execution projection contracts passed');

// Shell sessions follow their command, including nested and interleaved receipts.
const command=(id,session,extra={})=>call(id,'exec_command',{cmd:`run ${id}`},{result:{stdout:'start\n',session_id:session,exit_code:null},...extra});
const receipt=(id,session,result,extra={})=>call(id,'write_stdin',{session_id:session},{result,...extra});
const cmd=command('cmd','session'),reading=receipt('reading','session',{stdout:'middle\n',session_id:'session'}),finished=receipt('finished','session',{stdout:'end\n',exit_code:0},{started_at_ms:400,finished_at_ms:700});
const saved=structuredClone([cmd,reading,finished]);
rows=projectScriptRuns([cmd,reading],{activeTurn:'turn'});
assert.equal(execution(rows,'cmd').phase,'running');
assert.equal(rows.some(row=>row.tool?.name==='write_stdin'),false);
rows=projectScriptRuns([cmd,reading,finished]);
assert.equal(execution(rows,'cmd').phase,'completed');
assert.deepEqual(execution(rows,'cmd').output.map(chunk=>chunk.text),['start\n','middle\n','end\n']);
assert.equal(execution(rows,'cmd').process.result.exit_code,0);
assert.deepEqual(execution(rows,'cmd').process.calls,saved.map(row=>row.tool));
assert.deepEqual([cmd,reading,finished],saved);
assert.equal(execution(projectScriptRuns([cmd]),'cmd').phase,'unknown');

const streamed=receipt('streamed','session',{stdout:'streamed\n',stderr:'error\n',exit_code:0},{output:[{stream:'stdout',text:'streamed\n'}]});
assert.deepEqual(execution(projectScriptRuns([cmd,streamed]),'cmd').output.map(chunk=>chunk.text),['start\n','streamed\n','error\n'],'Stream and final receipt are not duplicated; missing streams use the receipt');
const cmd2=command('cmd2','session2',{started_at_ms:150}),finish2=receipt('finish2','session2',{stdout:'second',exit_code:0},{finished_at_ms:600});
rows=projectScriptRuns([cmd,cmd2,finished,finish2]);
assert.equal(execution(rows,'cmd2').output.at(-1).text,'second');
rows=projectScriptRuns([cmd,prose,finished,cmd2,finish2]);
assert.equal(execution(rows,'cmd').phase,'completed','A receipt beyond prose still updates the command');
const wrongTurn=receipt('wrong-turn','session',{exit_code:0},{turn_id:'another'}),wrongMachine=receipt('wrong-machine','session',{exit_code:0},{target:{runtime_id:'another'}});
for(const unmatched of [wrongTurn,wrongMachine,receipt('missing','absent',{exit_code:0})])assert(projectScriptRuns([cmd,unmatched],{activeTurn:'turn'}).some(row=>row===unmatched),'Unmatched session receipts remain visible');
const nested=command('nested','nested-session',{parent_id:'first'}),nestedDone=receipt('nested-done','nested-session',{stdout:'nested done',exit_code:0},{parent_id:'second'});
rows=projectScriptRuns([first,nested,done,second,nestedDone,secondDone]);
assert.equal(execution(rows,'nested').output.at(-1).text,'nested done');
assert.equal(toolForest(rows).byID.get('turn:nested').message.tool.parent_id,'first');
assert.equal(rows.some(row=>row.tool?.id==='nested-done'),false);
const input=receipt('input','session',{session_id:'session'},{args:{session_id:'session',chars:'hello\n'}}),approval=receipt('poll-approval','session',undefined,{phase:'waiting_approval'});
rows=projectScriptRuns([cmd,input,approval],{activeTurn:'turn'});
assert.equal(toolForest(rows).byID.get('turn:cmd').children.length,2,'Input and approvals remain beneath the command');
const failedPoll=receipt('failed-poll','session',{stderr:'Test failed',exit_code:2});
rows=projectScriptRuns([cmd,failedPoll,cmd2,finish2]);
assert.equal(execution(projectScriptRuns([cmd,receipt('cancelled','session',undefined,{phase:'stopped'})]),'cmd').phase,'stopped');
console.log('Command session projection contracts passed');

const remoteA=command('machine-a','shared',{target:{runtime_id:'a'}}),remoteB=command('machine-b','shared',{target:{runtime_id:'b'}}),remoteDone=receipt('remote-done','shared',{stdout:'remote done',exit_code:0},{target:{runtime_id:'b'}});
rows=projectScriptRuns([remoteA,remoteB,remoteDone],{activeTurn:'turn'});
assert.equal(execution(rows,'machine-a').phase,'running');
assert.equal(execution(rows,'machine-b').phase,'completed','Identical session IDs on different machines retain their ownership');
const unresolved=receipt('unresolved','missing',{session_id:'missing'});
assert.equal(execution(projectScriptRuns([unresolved],{activeTurn:'turn'}),'unresolved').phase,'running');
assert.equal(execution(projectScriptRuns([unresolved]),'unresolved').phase,'unknown','Orphan live sessions cannot claim completion or run forever after a turn');
