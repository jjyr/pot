import assert from 'node:assert/strict';
import {createWorkRecordProjection,workRecordSummary,workRecordParents} from '../../web/src/state/work-records.mjs';
import {projectScriptRuns} from '../../web/src/state/script-runs.mjs';
import {communication} from '../../web/src/state/communication.mjs';
const actor={node_id:'local',kind:'assistant',agent_id:'owner'},peer={...actor,agent_id:'peer'},task={node_id:'remote',kind:'task',agent_id:'abcd-long'};
const tool=(id,name='exec_command',extra={})=>({id,viewKey:id,role:'tool',tool:{id,turn_id:'turn',name,phase:'completed',args:{},...extra}});
const send=(id,to=task)=>tool(id,'send_message',{args:{to,text:'same body'},result:{accepted:true,message_id:id}});
const receive=(id,mails)=>tool(id,'receive_message',{args:{messages:mails}});
const first=tool('exec','exec'),child=tool('child','exec_command',{parent_id:'exec'}),a=send('mail'),b=send('peer',peer),create=tool('create','create_task'),compact={id:'compact',viewKey:'compact',role:'system',turn_id:'turn',compaction:{state:'complete'}};
const source=[first,child,a,create,b,compact],saved=structuredClone(source),project=createWorkRecordProjection();
let rows=project(source,{actor});
assert.equal(rows.length,1);assert.deepEqual(rows[0].members.map(m=>m.id),source.map(m=>m.id));assert.deepEqual([rows[0].work.tools,rows[0].work.messages],[2,2]);assert.deepEqual(rows[0].work.participants,[task,peer]);
const key=rows[0].viewKey;
rows=project([tool('earlier'),...source,tool('later')],{actor});assert.equal(rows[0].viewKey,key);
for(const boundary of [{id:'prose',role:'assistant',text:'Visible prose'},{id:'user',role:'user',text:'Steering'},tool('approval','exec_command',{phase:'waiting_approval'}),tool('unknown','exec',{phase:'unknown'}),{id:'result',role:'assistant',presentation:{kind:'html'}},{id:'error',role:'assistant',error:'Failure'}]){
 const next=createWorkRecordProjection()([a,b,boundary,send('after')],{actor});assert.equal(next.length,3);assert.equal(next[1],boundary);
}
assert.equal(createWorkRecordProjection()([a,tool('next','exec_command',{turn_id:'other'})],{actor}).length,2);
assert.equal(createWorkRecordProjection()([a,b],{actor,blocked:m=>m.id==='peer'}).length,2);
const failure=tool('fail','exec_command',{phase:'failed'}),wrapper=tool('wrapper','exec',{phase:'failed',failureID:'fail',failurePhase:'failed'});
rows=createWorkRecordProjection()([wrapper,{...failure,tool:{...failure.tool,parent_id:'wrapper'}}],{actor});assert.equal(rows[0].work.failures,1);
assert.equal(createWorkRecordProjection()([a,tool('running','cua_repl',{phase:'running',args:{code:'secret code'}})],{actor})[0].work.active.name,'cua_repl');
assert.deepEqual(source,saved,'No transcript facts mutated');
// Exact envelope IDs and complete endpoints correlate receipts, never text.
const envelope={id:'mail',from:actor,to:task,text:'same body'};
const duplicate=receive('projection',[envelope]);
rows=createWorkRecordProjection()([a,duplicate,receive('new',[{...envelope,id:'new'}])],{actor});
assert.equal(rows[0].work.messages,2);assert.equal(rows[0].members[1].workReceipt,true);
for(const changed of [{...envelope,id:undefined},{...envelope,to:{...task,node_id:'another'}},{...envelope,from:{...actor,agent_id:'other'}}])assert.equal(createWorkRecordProjection()([a,receive('other',[changed])],{actor})[0].work.messages,2);
assert.equal(communication(tool('envelope','send_message',{args:{to:task,text:'body'},result:envelope}).tool,actor).messages[0].id,'mail');
assert.equal(communication(duplicate.tool,actor).messages[0].id,'mail');
const split=createWorkRecordProjection();split([a,b,send('c'),send('d')],{actor});
rows=split([a,b,{id:'boundary',role:'assistant',text:'Boundary'},send('c'),send('d')],{actor,anchor:'c'});
assert.equal(rows[2].viewKey,key.replace('exec','mail'));assert.equal(new Set(rows.map(r=>r.viewKey)).size,rows.length);
// Parent/child operations and messages remain in global order even around prose.
const interleaved=[first,child,{id:'body',role:'assistant',text:'Body'},tool('late-child','apply_patch',{parent_id:'exec'}),send('later-mail')];
rows=createWorkRecordProjection()(projectScriptRuns(interleaved),{actor});
assert.deepEqual(rows.flatMap(r=>r.members||[r]).map(r=>r.id),interleaved.map(r=>r.id));
assert.equal(rows[0].work.tools,1);assert.equal(rows[2].work.tools,1);
const logical=new Map([['child','exec'],['late-child','exec']]),navigation=workRecordParents(rows,logical);
assert.equal(navigation.get('exec'),rows[0].id,'Root records connect to their virtual group');
assert.equal(navigation.get('child'),'exec','Same-group logical ancestry remains revealable');
assert.equal(navigation.get('late-child'),rows[2].id,'A reference across prose mounts the target group');
assert.equal(logical.has('exec'),false,'Projection does not mutate caller ancestry');
const cyclic=workRecordParents(rows,new Map([['exec','child'],['child','exec']]));
assert.equal(cyclic.get('exec'),rows[0].id);assert.equal(cyclic.get('child'),rows[0].id,'Malformed ancestry cannot trap navigation outside the group root');
assert.equal(workRecordParents(rows,new Map([['exec','missing']])).get('exec'),rows[0].id,'Missing parents do not override the mounted group');
assert.equal(createWorkRecordProjection()([tool('diagnostic','fixture_tool',{script:{failureID:'child'}})],{actor})[0].members[0].id,'diagnostic','Failure-only script metadata remains navigable without a calls array');
assert.equal(workRecordSummary([create],{actor}).participants.length,0,'Task creation is not peer communication');
assert.equal(createWorkRecordProjection()([tool('cmd'),tool('input','write_stdin',{parent_id:'cmd',args:{chars:'yes'}})],{actor})[0].work.tools,2,'A command and its real input action are both operations, not an orchestration wrapper');

const batch=receive('batch',[envelope,{...envelope,id:'second',from:peer}]);
rows=createWorkRecordProjection()([a,batch],{actor});assert.equal(rows[0].work.messages,2);assert.equal(rows[0].members[1].workMail.length,1);assert.deepEqual(rows[0].work.participants,[task,peer]);
for(const boundary of [tool('stopped','exec_command',{phase:'stopped'}),{id:'compact-failed',compaction:{state:'failed'}}])assert.equal(createWorkRecordProjection()([a,boundary,b],{actor}).length,3);
const sequenced=createWorkRecordProjection(),one={...tool('one'),sequence:'10'},two={...tool('two'),sequence:'11'},older={...tool('older'),sequence:'9'};
rows=sequenced([one,two]);const sequenceKey=rows[0].viewKey;
assert.deepEqual(rows[0].sourceAnchor,{id:'one',sequence:'10',before:'10',after:'11'});
for(let update=0;update<2;update++){
 rows=sequenced([older,one,two]);assert.equal(rows[0].viewKey,sequenceKey);
 assert.deepEqual(rows[0].sourceAnchor,{id:'one',sequence:'10',before:'9',after:'11'},'A stable group keeps one native representative while exposing its actual source range');
}
const running=tool('live','exec_command',{phase:'running'}),input={id:'input',viewKey:'input',role:'user',request_id:'turn'},activity={id:'waiting:input',viewKey:'activity:turn',role:'assistant',text:'',phase:'tools',activity:true,inputID:'input'};
rows=createWorkRecordProjection()([input,running,activity]);
assert.equal(rows.at(-1),activity,'Tool grouping cannot remove the independent execution activity row');
assert.equal(rows[1].work.active,null,'The same execution has one activity owner');
assert.equal(createWorkRecordProjection()([running,{...activity,activity:undefined}])[0].work.active.name,'exec_command','Task callers without an independent owner retain group activity');
rows=sequenced([older,two],{anchor:'two'});
assert.deepEqual(rows[0].sourceAnchor,{id:'two',sequence:'11',before:'9',after:'11'},'Clipping the representative selects a real retained member');
const separated=sequenced([older,{id:'separate',role:'assistant',text:'A prose boundary'},two],{anchor:'two'});
for(const row of separated.filter(row=>row.members))assert(row.members.some(member=>member.id===row.sourceAnchor.id),'A split cannot retain a representative outside its current group');
const largeA={...tool('large-a'),sequence:'900719925474099299999'},largeB={...tool('large-b'),sequence:'900719925474099300000'};
assert.deepEqual(createWorkRecordProjection()([largeB,largeA])[0].sourceAnchor,{id:'large-b',sequence:largeB.sequence,before:largeA.sequence,after:largeB.sequence},'Native string sequence ranges retain precision beyond Number');
assert.deepEqual(createWorkRecordProjection()([{...tool('task-a'),sequence:17},{...tool('task-b'),sequence:20}])[0].sourceAnchor,{id:'task-a',sequence:'17',before:'17',after:'20'},'Safe Task event bookmarks become exact DOM anchor strings');
assert.equal(createWorkRecordProjection()([older,one,two],{anchor:'two'})[0].sourceAnchor.id,'two','A fresh projection selects the saved native anchor when its virtual group key changes');
const otherExecution={...running,id:'other-live',viewKey:'other-live',tool:{...running.tool,id:'other-live',turn_id:'other-execution'}};
rows=createWorkRecordProjection()([input,running,activity,otherExecution]);
assert.equal(rows[1].work.active,null);assert.equal(rows.at(-1).work.active.id,'other-live','An execution without its own independent slot retains its group indicator');
console.log('Work records: mixed order, boundaries, count ownership, exact mail correlation, stable group identity and counterparts passed');
