import assert from 'node:assert/strict';
import {taskAddress, taskColorIndex, assistantAddress, addressKey, createTaskIdentityRegistry, taskShortID} from '../../web/src/state/task-identity.mjs';
const task=(id,node_id='node')=>({id:'catalogue-hash',address:{node_id,kind:'task',agent_id:id}});
const a=task('a81f2abc'),b=task('a81f9abc'),r=createTaskIdentityRegistry();
assert.equal(r.short(a),'#a81f');r.register([b]);assert.equal(r.short(a),'#a81f');assert.equal(r.short(b),'#a81f');
r.register([a]);assert.equal(r.short(a),'#a81f');assert.equal(r.short({id:'unconfirmed'}),'');
assert.equal(taskAddress(a).agent_id,'a81f2abc');assert.equal(taskColorIndex({id:'missing'}),-1);
assert.equal(taskColorIndex(a),taskColorIndex(a.address));
assert.notEqual(addressKey(a.address),addressKey(task('a81f2abc','other').address));
assert.equal(r.short(task('a81f2abc','other')),'#a81f');
assert.deepEqual(assistantAddress({id:'local-hash',control_id:'local',origin:{node_id:'remote',assistant_id:'real'}}),{node_id:'remote',kind:'assistant',agent_id:'real'});
assert.equal(taskColorIndex(task('测试-identity')),5);
console.log('Task identity: authoritative linked mapping, UTF-8 FNV, fixed prefixes and cross-node identity passed');

assert.equal(r.short(a),'#a81f');
const prefix=createTaskIdentityRegistry();prefix.register([task('abcd'),task('abcde')]);assert.equal(prefix.short(task('abcd')),'#abcd');assert.equal(prefix.short(task('abcde')),'#abcd');

assert.equal(r.resolve(a.address).id,'catalogue-hash');r.register([a.address]);assert.equal(r.resolve(a.address).id,'catalogue-hash');
const retained=r.resolve(a.address);r.register([{...a,phase:'running'}]);assert.equal(r.resolve(a.address),retained,'phase-only updates retain the display profile');
r.register([{...a,_space_id:'catalogue-space',assistant_id:'catalogue-owner'}]);r.register([a]);
assert.equal(r.resolve(a.address)._space_id,'catalogue-space');assert.equal(r.resolve(a.address).assistant_id,'catalogue-owner','unscoped display refreshes retain confirmed navigation scope');
assert.equal(r.resolve({node_id:'unknown',kind:'task',agent_id:'missing'}),undefined);

for(const address of [{node_id:{},kind:'task',agent_id:'id'},{node_id:'node',kind:'task',agent_id:123}]){assert.equal(addressKey(address),null);assert.equal(r.short(address),'');assert.equal(taskColorIndex(address),-1);}

const normal=createTaskIdentityRegistry(),uuid=task('a078c820-5dca-4aba-b473-e26a060224ab');
normal.register([uuid,task('b169c820-5dca-4aba-b473-e26a060224ab')]);
assert.equal(normal.short({id:'a078-local-hash'}),'','unmapped catalogue IDs are never used as display IDs');
assert.equal(normal.short({...uuid,id:'different-local-hash'}),'#a078');

assert.equal(normal.short(uuid),'#a078');
assert.equal(taskShortID(uuid.address),'#a078');assert.equal(taskShortID(task('xy')),'#xy');
normal.register([task('a078ffff-1111-4222-8333-444444444444')]);assert.equal(normal.short(uuid),'#a078');
normal.register([task(uuid.address.agent_id,'other-node')]);assert.equal(normal.short(uuid),'#a078');
normal.register([uuid,task('b169ffff-1111-4222-8333-444444444444')].reverse());assert.equal(normal.short(uuid),'#a078');
