import assert from 'node:assert/strict';
import {taskAddress, taskColorIndex, assistantAddress, addressKey, createTaskIdentityRegistry} from '../../web/src/state/task-identity.mjs';
const task=(id,node_id='node')=>({id:'catalogue-hash',address:{node_id,kind:'task',agent_id:id}});
const a=task('a81f2abc'),b=task('a81f9abc'),r=createTaskIdentityRegistry();
assert.equal(r.short(a),'#a81f');r.register([b]);assert.equal(r.short(a),'#a81f2');assert.equal(r.short(b),'#a81f9');
r.register([a]);assert.equal(r.short(a),'#a81f2');assert.equal(r.short({id:'unconfirmed'}),'');
assert.equal(taskAddress(a).agent_id,'a81f2abc');assert.equal(taskColorIndex({id:'missing'}),-1);
assert.equal(taskColorIndex(a),taskColorIndex(a.address));
assert.notEqual(addressKey(a.address),addressKey(task('a81f2abc','other').address));
assert.equal(r.short(task('a81f2abc','other')),'#a81f2@othe');
assert.deepEqual(assistantAddress({id:'local-hash',control_id:'local',origin:{node_id:'remote',assistant_id:'real'}}),{node_id:'remote',kind:'assistant',agent_id:'real'});
assert.equal(taskColorIndex(task('测试-identity')),5);
console.log('Task identity: authoritative linked mapping, UTF-8 FNV, collisions and cross-node sources passed');

assert.equal(r.short(a),'#a81f2@node');
const prefix=createTaskIdentityRegistry();prefix.register([task('abcd'),task('abcde')]);assert.equal(prefix.short(task('abcd')),'#abcd');assert.equal(prefix.short(task('abcde')),'#abcde');
