import assert from 'node:assert/strict';
import {createCommunicationProjection,communicationCounts,stackableCommunication} from '../../web/src/state/communication-stack.mjs';
const actor={node_id:'local',kind:'assistant',agent_id:'pot'},task={node_id:'local',kind:'task',agent_id:'abcd-one'};
const send=(id,to=task)=>({id,viewKey:id,role:'tool',tool:{id,name:'send_message',phase:'completed',args:{to,text:id}}});
const receive={id:'batch',viewKey:'batch',role:'tool',tool:{name:'receive_message',phase:'completed',args:{messages:[{from:task,to:actor,text:'first'},{from:{...task,node_id:'remote'},to:actor,text:'second'}]}}};
const project=createCommunicationProjection(),a=send('a'),b=send('b');
let rows=project([a,receive,b],{actor});
assert.deepEqual(rows[0].members.map(row=>row.id),['a','batch','b']);
const key=rows[0].viewKey;
const counts=communicationCounts(rows[0].members,actor);
assert.deepEqual([counts.records,counts.messages,counts.tasks],[3,4,2]);
rows=project([send('earlier'),a,receive,b,send('later')],{actor});
assert.equal(rows[0].viewKey,key,'prepend and append retain the original group key');
for(const boundary of [
  {id:'body',role:'assistant',text:'body'}, {id:'user',role:'user',text:'guidance'},
  {...send('other'),tool:{name:'exec_command',phase:'completed'}},send('assistant',actor),
  ...['preparing','running','failed','denied','unknown','waiting_approval'].map(phase=>({...send(phase),tool:{...send(phase).tool,phase}})),
  {...receive,tool:{...receive.tool,args:{messages:[{from:actor,to:actor,text:'assistant'},{from:task,to:actor,text:'task'}]}}},
  {...send('partial'),tool:{...send('partial').tool,args:'{"to":'}},
  send('missing',{node_id:'local',kind:'task'}),
  {...send('script'),tool:{...send('script').tool,script:{}}},
]){
  assert.equal(stackableCommunication(boundary,actor),null);
  assert.equal(project([a,b,boundary,send('c'),send('d')],{actor}).length,3);
}
assert.equal(project([a,b],{actor,blocked:message=>message.id==='b'}).length,2);
const split=createCommunicationProjection();
split([a,b,send('c'),send('d')],{actor});
rows=split([a,b,{id:'break',role:'user'},send('c'),send('d')],{actor,anchor:'c'});
assert.equal(new Set(rows.map(row=>row.viewKey)).size,rows.length,'split groups have unique keys');
assert.equal(rows[2].viewKey,'communication:a','the reading group keeps its key on split');
assert.deepEqual([a,b].map(row=>row.viewKey),['a','b'],'the source transcript is unchanged');
console.log('Task communication: adjacency, boundaries, batches, original order, append/prepend and split identity passed');
