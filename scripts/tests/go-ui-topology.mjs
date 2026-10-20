import assert from 'node:assert/strict';
import {projectTopology,agentNodeID,layoutTopology} from '../../web/src/state/topology.mjs';
const spaces=[{id:'local',name:'Local',kind:'local',connected:true,control_id:'local-control'},{id:'cloud',name:'Cloud',remote:true,connected:true,control_id:'cloud-control'}];
const machines=[{id:'same-id',name:'Local runner',_space_id:'local',connected:true},{id:'same-id',name:'Cloud runner',_space_id:'cloud',connected:false}];
const pages=[{space:spaces[0],value:{self:{id:'main',node_id:'local-control',kind:'pot'},items:[{id:'codex-one',local_assistant_id:'main',peer:{id:'codex',node_id:'codex-control-one',kind:'codex',name:'Codex'},status:'connected'},{id:'remote-pot',local_assistant_id:'main',peer:{id:'main',node_id:'cloud-control',kind:'pot',name:'Cloud Agent'},status:'connected'}]}},{space:spaces[1],value:{self:{id:'main',node_id:'cloud-control',kind:'pot'},items:[{id:'return-link',local_assistant_id:'main',peer:{id:'main',node_id:'local-control',kind:'pot'},status:'connected'}]}}];
const model=projectTopology(spaces,machines,pages);
assert.equal(model.machines,2,'Codex never creates a fictional execution resource');assert.equal(model.agents,3,'A managed Control and linked peer project to one Agent');assert.equal(model.edges.filter(edge=>edge.kind==='agent').length,2,'Bidirectional views of one peer connection do not duplicate edges');
assert.equal(new Set(model.nodes.map(node=>node.id)).size,model.nodes.length,'Repeated Runner IDs in different Controls stay distinct');assert.equal(model.nodes.find(node=>node.kind==='codex').group,'','Codex is an independent endpoint, with no machine wrapper');
const remote=projectTopology(spaces,[],[{space:spaces[0],value:{items:[{id:'foreign',local_assistant_id:'main',peer:{id:'main',node_id:'foreign-control',kind:'pot',name:'Another Pot'},status:'offline'}]}}]);assert.equal(remote.groups.length,3);assert.equal(remote.machines,0,'A remote Pot never implies inventory');
for(const width of [329,393,680,1000]){const layout=layoutTopology(model,width);assert.equal(layout.positions.size,model.nodes.length);for(const position of layout.positions.values()){assert.ok(position.x>=40&&position.x<=width-40,'Nodes stay inside narrow and wide maps');assert.ok(position.y>0&&position.y<layout.height,'Every node fits the canvas');}}
console.log('Topology projection: exact ownership, independent Codex, deduplication and responsive layout passed');

const multi=projectTopology(spaces,[],[{space:spaces[0],value:{self:{id:'main',node_id:'local-control',kind:'pot'},items:[
 {id:'a',local_assistant_id:'main',peer:{id:'codex',node_id:'codex-control-one',kind:'codex',name:'Codex one'},status:'connected'},
 {id:'b',local_assistant_id:'main',peer:{id:'codex',node_id:'codex-control-two',kind:'codex',name:'Codex two'},status:'connected'},
 {id:'c',local_assistant_id:'other-local',peer:{id:'codex',node_id:'codex-control-one',kind:'codex',name:'Codex one'},status:'connected'},
]}}]);
assert.equal(multi.nodes.filter(node=>node.kind==='codex').length,2,'Two Codex hosts with the same fixed assistant ID remain distinct');
assert.equal(multi.edges.filter(edge=>edge.kind==='agent').length,2,'Multiple assistant routes share one node connection');
console.log('Agent identities: node connections stay independent of assistant routing');

assert.notEqual(agentNodeID({node_id:'node-a',kind:'codex',id:'same-agent'}),agentNodeID({node_id:'node-b',kind:'codex',id:'same-agent'}),'Generic Agent identities stay distinct by node');
assert.equal(model.nodes.find(node=>node.id==='control:cloud').identity,'cloud-control','A managed Control resolves its Agent self node without renaming Space metadata');
assert.equal(model.nodes.filter(node=>node.id==='control:cloud').length,1,'A linked managed Pot remains one Control node');
assert.equal(spaces[0].control_id,'local-control');assert.equal('node_id' in spaces[0],false,'Space ownership still uses control_id');
for(const node_id of [undefined,'','   ']){
 const invalid=projectTopology(spaces,[],[{space:spaces[0],value:{items:[{id:'invalid-peer',peer:{id:'codex',node_id,control_id:'obsolete-alias',kind:'codex'},status:'connected'}]}}]);
 assert.equal(invalid.agents,spaces.length,'Missing node identity cannot fall back to the old communication alias');
 assert.equal(invalid.edges.filter(edge=>edge.kind==='agent').length,0);
}
console.log('Node identity contract: generic Agent node IDs and specialized Space/Runner Control fields stay separate');

assert.equal(agentNodeID({node_id:'same',kind:'codex',id:'one'}),agentNodeID({node_id:'same',kind:'codex',id:'two'}),'Assistant directory changes never add topology nodes');
const directory=[{address:{node_id:'codex-node',kind:'assistant',agent_id:'review'},name:'Reviewer',adapter:'codex'}];
const paused=projectTopology([spaces[0]],[],[{space:spaces[0],value:{self:{node_id:'local-control',kind:'pot'},items:[{id:'paused',peer:{node_id:'codex-node',kind:'codex',name:'Codex'},status:'disconnected',assistants:directory}]}}]);
assert.equal(paused.agents,2,'Node links do not require a peer assistant binding');
assert.equal(paused.nodes.find(node=>node.kind==='codex').link.assistants,directory,'Inspector receives the node assistant directory');
assert.equal(paused.edges[0].status,'disconnected','Disconnect retains the relationship in the graph');
