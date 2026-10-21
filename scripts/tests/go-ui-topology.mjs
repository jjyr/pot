import {machineDisplayName,linkEndpoint} from '../../web/src/state/machine-identity.mjs';
import assert from 'node:assert/strict';
import {projectTopology,agentNodeID,layoutTopology,assistantActivityState,nodeObservation} from '../../web/src/state/topology.mjs';
const local={id:'local',name:'Local Pot',kind:'local',connected:true,control_id:'local-node'},spaces=[local];
const machines=[{id:'my-mac',name:'My Mac',_space_id:'local',connected:true}];
const links=[
 {id:'codex-one',peer:{node_id:'codex-node-one',kind:'codex',name:'Codex'},status:'connected'},
 {id:'remote-pot',peer:{node_id:'cloud-node',kind:'pot',name:'Cloud Pot'},status:'connected'},
];
const page=items=>({space:local,value:{self:{node_id:'local-node',kind:'pot'},items}});
const model=projectTopology(spaces,machines,[page(links)]);
assert.equal(model.machines,1,'Only local physical inventory creates an execution resource');
assert.equal(model.agents,3,'Local Pot, cloud Pot and Codex are distinct nodes');
assert.equal(model.edges.filter(edge=>edge.kind==='agent').length,2);
assert.equal(new Set(model.nodes.map(node=>node.id)).size,model.nodes.length);
assert.equal(model.nodes.find(node=>node.kind==='codex').group,'','Codex has no fictional machine wrapper');
assert.equal(model.groups.length,2,'Cloud Pot keeps its own topology region');
assert.equal(model.nodes.find(node=>node.link?.id==='remote-pot').group,'peer-control:cloud-node');
const remote=projectTopology(spaces,[],[page([links[1]])]);
assert.equal(remote.machines,0,'A remote Pot never implies machine inventory');
for(const width of [329,393,680,1000]){
 const layout=layoutTopology(model,width);assert.equal(layout.positions.size,model.nodes.length);
 for(const position of layout.positions.values()){
  assert.ok(position.x>=40&&position.x<=width-40,'Nodes stay inside narrow and wide maps');
  assert.ok(position.y>0&&position.y<layout.height,'Every node fits the canvas');
 }
}
const repeated=projectTopology(spaces,[],[page([
 links[0],{id:'codex-two',peer:{node_id:'codex-node-two',kind:'codex',name:'Second Codex'},status:'connected'},
 {...links[0],id:'repeated-observation'},
])]);
assert.equal(repeated.nodes.filter(node=>node.kind==='codex').length,2,'Separate Codex hosts remain distinct');
assert.equal(repeated.edges.filter(edge=>edge.kind==='agent').length,2,'Repeated node observations do not duplicate edges');
assert.notEqual(agentNodeID({node_id:'node-a',kind:'codex'}),agentNodeID({node_id:'node-b',kind:'codex'}));
for(const node_id of [undefined,'','   ']){
 const invalid=projectTopology(spaces,[],[page([{id:'invalid-peer',peer:{node_id,control_id:'obsolete-alias',kind:'codex'},status:'connected'}])]);
 assert.equal(invalid.agents,1,'Missing node identity cannot fall back to an obsolete alias');
 assert.equal(invalid.edges.filter(edge=>edge.kind==='agent').length,0);
}
assert.equal(agentNodeID({node_id:'same',kind:'codex',id:'one'}),agentNodeID({node_id:'same',kind:'codex',id:'two'}),'Assistant directory changes never add topology nodes');
const directory=[{address:{node_id:'codex-node',kind:'assistant',agent_id:'review'},name:'Reviewer',adapter:'codex'}];
const paused=projectTopology(spaces,[],[page([{id:'paused',peer:{node_id:'codex-node',kind:'codex',name:'Codex'},status:'disconnected',assistants:directory}])]);
assert.equal(paused.agents,2,'Node links do not require a peer assistant binding');
assert.equal(paused.nodes.find(node=>node.kind==='codex').link.assistants,directory,'Inspector receives the node assistant directory');
assert.equal(paused.edges[0].status,'disconnected','Disconnect retains the relationship in the graph');
const inventory=[{id:'opaque-id',runtime_id:'opaque-id',name:'mac-studio.local',hostname:'mac-studio.local',ip_addresses:['192.168.1.24','fd00::24'],_space_id:'local',connected:true}];
const located=projectTopology(spaces,inventory,[page([{...links[0],peer:{...links[0].peer,address:'tcp://dev-box.example:8793'}}])]);
assert.equal(located.nodes.find(node=>node.kind==='machine').name,'mac-studio.local');
assert.equal(located.nodes.find(node=>node.kind==='machine').identity,'opaque-id','Host metadata never changes routing identity');
assert.equal(located.nodes.find(node=>node.kind==='machine').subtitle,'192.168.1.24');
assert.equal(located.nodes.find(node=>node.self).subtitle,'mac-studio.local · 192.168.1.24');
assert.equal(located.nodes.find(node=>node.kind==='codex').subtitle,'dev-box.example:8793');
assert.equal(machineDisplayName({ip_addresses:['10.0.0.2']}),'10.0.0.2');
for(const address of [undefined,'invalid','https://host.example','tcp://host.example:8793#invitation','tcp://user:secret@host.example:8793'])assert.equal(linkEndpoint({peer:{address}}),'','Only capability-free TCP endpoints become display metadata');
assert.equal(linkEndpoint({peer:{address:'tcp://[fd00::1]:8793'}}),'[fd00::1]:8793');
const activity={assistant_id:'review',running:1,queued:2,waiting_approval:0,waiting_allowance:0,chat_state:'idle',fenced:false},sample={observed_at_ms:Date.now(),value:{node_id:'codex-node-one',hostname:'dev-box.local',assistants:[activity],outbox:{pending:2,oldest_at_ms:1}}};
const observed=projectTopology(spaces,[],[page([{...links[0],observation:sample}])]);
assert.equal(observed.nodes.find(node=>node.kind==='codex').subtitle,'dev-box.local','A verified node report supplies the remote hostname');
assert.deepEqual(observed.counts,{running:1,queued:2});
assert.equal(observed.nodes.find(node=>node.kind==='codex').issue,'','Ordinary execution and queueing have no warning');
assert.equal(assistantActivityState(activity),'running');
assert.equal(assistantActivityState({...activity,fenced:true}),'fenced');
assert.equal(assistantActivityState({...activity,waiting_approval:1}),'approval');
assert.equal(assistantActivityState({...activity,waiting_allowance:1}),'allowance');
assert.equal(assistantActivityState(undefined),'unknown');
for(const change of [{observed_at_ms:Date.now()-31000},{error:'unavailable'}]){
 const staleSample={...sample,...change};assert.equal(nodeObservation(staleSample,'connected').fresh,false);
 const staleModel=projectTopology(spaces,[],[page([{...links[0],observation:staleSample}])]);
 assert.equal(staleModel.counts.running,0,'Stale node totals are excluded from current counts');
 assert.equal(staleModel.nodes.find(node=>node.kind==='codex').observation.counts.running,1,'The inspector can retain explicitly cached values');
}
assert.equal(nodeObservation(sample,'disconnected').fresh,false,'Disconnect never advertises last known load as current');
const waiting=projectTopology(spaces,[],[page([{...links[0],observation:{...sample,value:{...sample.value,assistants:[{...activity,waiting_allowance:1}]}}}])]);
assert.equal(waiting.nodes.find(node=>node.kind==='codex').issue,'allowance','Problems belong to the affected node');
console.log('Node topology: local inventory, independent peers, directory projection, deduplication and responsive layout passed');

// Human-readable labels never become routing keys, including absent metadata.
const {machinePresentation,machineHostLabel,linkHostLabel,linkDisplayName}=await import('../../web/src/state/machine-identity.mjs');
assert.equal(machineHostLabel({id:'opaque',name:'opaque'}),'');
assert.equal(machineHostLabel({hostname:'10.0.0.2',ip_addresses:['10.0.0.2']}),'10.0.0.2');
assert.equal(linkHostLabel({peer:{address:'tcp://[fd00::1]:8793'},observation:{value:{hostname:'dev-box'}}}),'dev-box · [fd00::1]:8793');
assert.equal(linkDisplayName({peer:{node_id:'opaque',name:'opaque',kind:'codex'}}),'Codex');
const anonymous=projectTopology([{...local,name:''}],[],[page([{id:'anonymous',peer:{node_id:'opaque',kind:'pot'},status:'offline'}])]);
assert.ok(anonymous.nodes.every(node=>!['opaque','local-node'].includes(node.name)));
let fail=false;
const machineClient={runtime:{boot_id:'one'},spaces:[{id:'local',control_id:'owner-a'},{id:'other',control_id:'owner-b'}],scope:()=>({space:'local'}),request:async(path,{space,owner})=>{assert.equal(owner,'');if(fail)throw Error('offline');return {items:[{id:'same-id',hostname:space+'-host',ip_addresses:[space==='local'?'10.0.0.1':'10.0.0.2']}]};}};
const presentation=machinePresentation(machineClient);
await Promise.all(['local','other'].map(space=>presentation.load({space})));
assert.equal(presentation.label('same-id',{space:'local'},'en'),'local-host · 10.0.0.1');
assert.equal(presentation.label('same-id',{space:'other'},'en'),'other-host · 10.0.0.2');
fail=true;await presentation.load({space:'local'});
assert.equal(presentation.label('same-id',{space:'local'},'en'),'local-host · 10.0.0.1','Offline samples retain their own host metadata');
machineClient.spaces[0].control_id='replacement-owner';
assert.equal(presentation.label('same-id',{space:'local'},'zh-CN'),'机器信息不可用','A replacement node never inherits another machine label');
machineClient.assistants=[{id:'linked',space_id:'other',origin:{host:'remote-host · 100.88.0.8:8793'}}];
assert.equal(presentation.label('same-id',{space:'other',owner:'linked'},'en'),'remote-host · 100.88.0.8:8793','Linked execution never borrows local inventory');
