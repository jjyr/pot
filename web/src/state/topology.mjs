// A projection of authorized Control inventory and Agent Links, never a second
// connection registry. In particular an Agent identity is not a machine fact.
export const agentNodeID=peer=>'peer:'+JSON.stringify([peer.node_id,peer.kind]);
const identityKey=peer=>JSON.stringify([peer.node_id,peer.kind]);
export const machineDisplayName=machine=>machine.hostname||machine.name||machine.ip_addresses?.[0]||machine.os||'';
export function linkEndpoint(link){
  try{const url=new URL(link?.peer?.address);return url.protocol==='tcp:'&&!url.username&&!url.password&&!url.search&&!url.hash?url.host:'';}catch{return '';}
}

export function assistantActivityState(value){
  if(!value)return 'unknown';
  if(value.fenced)return 'fenced';
  if(value.waiting_approval>0)return 'approval';
  if(value.waiting_allowance>0||value.chat_state==='waiting')return 'allowance';
  if(value.chat_state==='failed')return 'failed';
  if(value.running>0||value.chat_state==='running')return 'running';
  if(value.queued>0||value.chat_state==='queued')return 'queued';
  if(value.chat_state==='paused')return 'paused';
  return value.chat_state==='idle'?'idle':'unknown';
}

export function nodeObservation(sample,status,now=Date.now()){
  const value=sample?.value,counts={running:0,queued:0,waiting_approval:0,waiting_allowance:0};
  const fresh=status==='connected'&&!!value&&!sample.error&&Number.isSafeInteger(sample.observed_at_ms)&&now-sample.observed_at_ms<30000&&sample.observed_at_ms<=now+1000;
  for(const activity of value?.assistants||[])for(const key of Object.keys(counts))if(Number.isSafeInteger(activity[key])&&activity[key]>=0)counts[key]+=activity[key];
  return {value,counts,fresh,observedAt:sample?.observed_at_ms||0};
}

export function projectTopology(spaces,machines,linkPages=[]) {
  const nodes=[],edges=[],groups=[],selfIDs=new Map(),bySpace=new Map();
  for(const space of spaces){const page=linkPages.find(item=>item.space.id===space.id),self=page?.value?.self,group={id:space.id,name:space.name,local:space.kind==='local'&&!space.remote,nodes:[]},agent={id:'control:'+space.id,kind:'pot',name:space.name||self?.name||space.control_id,space:space.id,group:space.id,status:space.connected===false||page?.stale?'offline':'connected',identity:self?.node_id||space.control_id,self:true};groups.push(group);nodes.push(agent);group.nodes.push(agent.id);bySpace.set(space.id,agent);if(self?.node_id)selfIDs.set(identityKey(self),agent);}
  for(const machine of machines){const group=groups.find(item=>item.id===machine._space_id);if(!group)continue;const node={id:'machine:'+JSON.stringify([machine._space_id,machine.runtime_id||machine.id]),kind:'machine',name:machine.name||machine.runtime_id||machine.id,space:machine._space_id,group:group.id,status:machine._stale||spaces.find(space=>space.id===machine._space_id)?.connected===false?'stale':machine.connected?'connected':'offline',identity:machine.runtime_id||machine.id,machine};nodes.push(node);group.nodes.push(node.id);edges.push({id:'execute:'+node.id,from:bySpace.get(node.space).id,to:node.id,kind:'execute',status:node.status});}
  for(const page of linkPages){const from=bySpace.get(page.space.id);if(!from)continue;for(const link of page.value?.items||[]){if(typeof link.peer?.node_id!=='string'||!link.peer.node_id.trim())continue;let peer=selfIDs.get(identityKey(link.peer))||nodes.find(node=>node.id===agentNodeID(link.peer));const status=page.stale?'stale':link.status;
      if(!peer){const kind=link.peer.kind==='codex'?'codex':'pot',groupID=kind==='pot'?(spaces.find(space=>space.control_id===link.peer.node_id)?.id||'peer-control:'+link.peer.node_id):'';peer={id:agentNodeID(link.peer),kind,name:link.peer.name||link.peer.node_id,space:page.space.id,group:groupID,status,identity:link.peer.node_id,link};nodes.push(peer);if(groupID){const group=groups.find(item=>item.id===groupID);if(group)group.nodes.push(peer.id);else groups.push({id:groupID,name:peer.name,local:false,nodes:[peer.id],remote:true});}}
      peer.link=link;peer.space=page.space.id;if(from.id===peer.id)continue;const key=[page.value?.self?.node_id||page.space.control_id,link.peer.node_id].sort().join('|');const existing=edges.find(edge=>edge.kind==='agent'&&edge.pair===key);if(!existing)edges.push({id:'agent:'+page.space.id+':'+link.id,from:from.id,to:peer.id,pair:key,kind:'agent',status,link,space:page.space.id});
    }}
  for(const node of nodes){
    if(node.kind==='machine'){node.name=machineDisplayName(node.machine);node.subtitle=node.machine.ip_addresses?.[0]||'';}
    else if(node.self){node.subtitle=machines.find(machine=>machine._space_id===node.group)?.hostname||'';}
    else {node.subtitle=node.link?.observation?.value?.hostname||linkEndpoint(node.link);}
    if(node.kind!=='machine'){
      const self=linkPages.find(page=>page.space.id===node.group)?.value?.self;
      node.observation=nodeObservation(node.self?self?.observation:node.link?.observation,node.status);
      node.activities=new Map((node.observation.value?.assistants||[]).map(activity=>[activity.assistant_id,activity]));
      node.issue=node.status==='disconnected'?'':node.link?.last_error==='agent_link_reconnect_required'?'reconnect':node.status!=='connected'?'offline':!node.observation.fresh?'stale':'';
      if(node.observation.fresh){for(const reason of ['fenced','approval','allowance','failed'])if([...node.activities.values()].some(activity=>assistantActivityState(activity)===reason)){node.issue=reason;break;}}
    }
  }
  const agents=nodes.filter(node=>node.kind!=='machine'),counts={running:0,queued:0};
  for(const node of agents)if(node.observation.fresh)for(const key of Object.keys(counts))counts[key]+=node.observation.counts[key];
  return {nodes,edges,groups,agents:agents.length,machines:nodes.filter(node=>node.kind==='machine').length,online:agents.filter(node=>node.status==='connected').length,counts};
}

export function layoutTopology(model,width){
  const narrow=width<500,padding=narrow?10:22,gap=narrow?22:64,columns=2,zoneWidth=(width-padding*2-gap)/columns,positions=new Map(),zones=[];let y=24;
  for(let row=0;row<model.groups.length;row+=columns){const batch=model.groups.slice(row,row+columns);let rowHeight=0;for(const [column,group]of batch.entries()){const members=group.nodes.map(id=>model.nodes.find(node=>node.id===id)).filter(Boolean),agents=members.filter(node=>node.kind!=='machine'),machines=members.filter(node=>node.kind==='machine'),machineColumns=narrow?1:2,rows=Math.ceil(machines.length/machineColumns),agentRows=Math.ceil(agents.length/(narrow?1:2)),machineStart=286+Math.max(0,agentRows-1)*184,height=Math.max(narrow?210:230,machineStart-20+rows*(narrow?144:152));rowHeight=Math.max(rowHeight,height);const x=padding+column*(zoneWidth+gap);zones.push({id:group.id,name:group.name,x,y,width:zoneWidth,height,group});for(const [index,agent]of agents.entries()){const count=Math.min(narrow?1:2,agents.length);positions.set(agent.id,{x:x+zoneWidth*(index%(narrow?1:2)+.5)/count,y:y+96+Math.floor(index/(narrow?1:2))*184});}for(const [index,machine]of machines.entries()){const count=Math.min(machineColumns,machines.length),cx=x+zoneWidth*(index%machineColumns+.5)/count;positions.set(machine.id,{x:cx,y:y+machineStart+Math.floor(index/machineColumns)*(narrow?144:152)});}}
    y+=rowHeight+28;
  }
  const bottoms=[padding,padding];for(const zone of zones){const column=zone.x<width/2?0:1;bottoms[column]=Math.max(bottoms[column],zone.y+zone.height);}
  const standalone=model.nodes.filter(node=>!node.group);for(const node of standalone){const edge=model.edges.find(item=>item.to===node.id),source=positions.get(edge?.from),column=!source||source.x<width/2?1:0,cx=padding+zoneWidth/2+column*(zoneWidth+gap),cy=bottoms[column]+88;positions.set(node.id,{x:cx,y:cy});bottoms[column]=cy+132;}
  return {positions,zones,height:Math.max(460,y,...bottoms.map(value=>value+28)),narrow};
}
