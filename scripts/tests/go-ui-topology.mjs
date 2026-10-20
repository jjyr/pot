import assert from 'node:assert/strict';
import {projectTopology,agentNodeID,layoutTopology} from '../../web/src/state/topology.mjs';
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
console.log('Node topology: local inventory, independent peers, directory projection, deduplication and responsive layout passed');
