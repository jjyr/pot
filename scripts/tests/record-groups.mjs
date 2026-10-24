import assert from 'node:assert/strict';
import {groupRecords,recordOwner,ownerChoices} from '../../web/src/components/record-groups.mjs';
const client={spaces:[{id:'local',control_id:'node-a',name:'Work'},{id:'linked',control_id:'node-a',name:'Linked'}],assistants:[
 {id:'alpha',control_id:'node-a',space_id:'local',name:'Pot'},
 {id:'directory-hash',control_id:'node-a',space_id:'linked',name:'Pot',profile_name:'Pot',origin:{node_id:'node-b',assistant_id:'alpha'}},
]};
const row=(id,owner,space='local')=>({id,_assistant_id:owner,_space_id:space});
const records=[row('first','alpha'),row('second','directory-hash','linked'),row('third','alpha'),{id:'unowned',machine_id:'alpha',spec:{title:'Pot'}}];
const groups=groupRecords(records,client);
assert.equal(groups.length,3);assert.deepEqual(groups[0].items.map(t=>t.id),['first','third']);
assert.equal(groups[0].key,JSON.stringify(['node-a','assistant','alpha']));
assert.equal(groups[1].key,JSON.stringify(['node-b','assistant','alpha']));
assert.equal(groups[2].key,'unknown-owner','A machine ID or same title never infers a schedule owner');
assert.equal(recordOwner(records[1],client).address.agent_id,'alpha','Linked directory hash is not a remote assistant ID');
assert.equal(ownerChoices(client,'en').filter(([id])=>id.startsWith('[')).length,2);
console.log('Record groups: full cross-node identity, linked directory mapping, page-local order and neutral ownership passed');
