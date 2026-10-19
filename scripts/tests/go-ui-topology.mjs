import assert from 'node:assert/strict';
import {projectTopology,layoutTopology,parseMachineJoinInfo} from '../../web/src/state/topology.mjs';
const spaces=[{id:'local',name:'Local',kind:'local',connected:true,control_id:'local-control'},{id:'cloud',name:'Cloud',remote:true,connected:true,control_id:'cloud-control'}];
const machines=[{id:'same-id',name:'Local runner',_space_id:'local',connected:true},{id:'same-id',name:'Cloud runner',_space_id:'cloud',connected:false}];
const pages=[{space:spaces[0],value:{self:{id:'main',control_id:'local-control',kind:'pot'},items:[{id:'codex-one',local_assistant_id:'main',peer:{id:'codex',control_id:'codex-control-one',kind:'codex',name:'Codex'},status:'connected'},{id:'remote-pot',local_assistant_id:'main',peer:{id:'main',control_id:'cloud-control',kind:'pot',name:'Cloud Agent'},status:'connected'}]}},{space:spaces[1],value:{self:{id:'main',control_id:'cloud-control',kind:'pot'},items:[{id:'return-link',local_assistant_id:'main',peer:{id:'main',control_id:'local-control',kind:'pot'},status:'connected'}]}}];
const model=projectTopology(spaces,machines,pages);
assert.equal(model.machines,2,'Codex never creates a fictional execution resource');assert.equal(model.agents,3,'A managed Control and linked peer project to one Agent');assert.equal(model.edges.filter(edge=>edge.kind==='agent').length,2,'Bidirectional views of one peer connection do not duplicate edges');
assert.equal(new Set(model.nodes.map(node=>node.id)).size,model.nodes.length,'Repeated Runner IDs in different Controls stay distinct');assert.equal(model.nodes.find(node=>node.kind==='codex').group,'','Codex is an independent endpoint, with no machine wrapper');
const remote=projectTopology(spaces,[],[{space:spaces[0],value:{items:[{id:'foreign',local_assistant_id:'main',peer:{id:'main',control_id:'foreign-control',kind:'pot',name:'Another Pot'},status:'offline'}]}}]);assert.equal(remote.groups.length,3);assert.equal(remote.machines,0,'A remote Pot never implies inventory');
for(const width of [329,393,680,1000]){const layout=layoutTopology(model,width);assert.equal(layout.positions.size,model.nodes.length);for(const position of layout.positions.values()){assert.ok(position.x>=40&&position.x<=width-40,'Nodes stay inside narrow and wide maps');assert.ok(position.y>0&&position.y<layout.height,'Every node fits the canvas');}}
console.log('Topology projection: exact ownership, independent Codex, deduplication and responsive layout passed');

const info={version:1,url:'https://control.example',control_id:'control-id',fingerprint:'SHA256:sample',invitation:'synthetic-invite'};
assert.deepEqual(parseMachineJoinInfo(JSON.stringify({...info,command:'rm -rf /'})),info,'Join import projects only typed fields, never a command');
for(const invalid of [{...info,version:2},{...info,url:'javascript:alert(1)'},{...info,url:'https://owner:credential@control.example'},{...info,url:'https://control.example#credential'},{...info,fingerprint:''},{...info,invitation:[]},null])assert.throws(()=>parseMachineJoinInfo(JSON.stringify(invalid)));
assert.throws(()=>parseMachineJoinInfo(' '.repeat(16385)));
console.log('Machine join data: bounded typed parsing and credential/URL rejection passed');

const multi=projectTopology(spaces,[],[{space:spaces[0],value:{self:{id:'main',control_id:'local-control',kind:'pot'},items:[
 {id:'a',local_assistant_id:'main',peer:{id:'codex',control_id:'codex-control-one',kind:'codex',name:'Codex one'},status:'connected'},
 {id:'b',local_assistant_id:'main',peer:{id:'codex',control_id:'codex-control-two',kind:'codex',name:'Codex two'},status:'connected'},
 {id:'c',local_assistant_id:'other-local',peer:{id:'codex',control_id:'codex-control-one',kind:'codex',name:'Codex one'},status:'connected'},
]}}]);
assert.equal(multi.nodes.filter(node=>node.kind==='codex').length,2,'Two Codex hosts with the same fixed assistant ID remain distinct');
assert.equal(multi.edges.filter(edge=>edge.kind==='agent').length,3,'Different local assistant routes to one peer keep their own relationship facts');
console.log('Agent identities: complete Control/adapter/assistant identity and independent routes passed');
