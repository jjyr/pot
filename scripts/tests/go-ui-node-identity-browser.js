// Generic Agent identities use node_id; Space ownership and Runner enrollment
// keep their specialized control_id fields. All identities here are synthetic.
async page => {
 const assert=(condition,message)=>{if(!condition)throw Error(message);},errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 const cdp=await page.context().newCDPSession(page);await cdp.send('Network.setCacheDisabled',{cacheDisabled:true});
 await page.setViewportSize({width:1024,height:700});
 const fixtureURL=new URL('/scripts/tests/fixtures/topology.html',page.url()).href;
 for(const invalid of [{omit:true},{node:''},{node:'   '}]){
  await page.goto(fixtureURL);await page.getByRole('button',{name:'Add Agent',exact:true}).waitFor();
  await page.evaluate(value=>{topologyFixture.omitPreviewNodeID=!!value.omit;topologyFixture.previewNodeID=value.node;},invalid);
  await page.getByRole('button',{name:'Add Agent',exact:true}).click();
  await page.getByLabel('Other Agent address',{exact:true}).fill('tcp://fixture.example:8793#synthetic');
  await page.locator('.connection-preview-error').waitFor({state:'visible'});
  assert(await page.locator('.editor-actions .primary').isDisabled(),'A missing or empty node_id cannot authorize Agent addition');
  assert(!await page.evaluate(()=>topologyFixture.calls.some(call=>call.body?.action==='connect')),'An invalid generic node identity never reaches connect');
  await page.getByRole('button',{name:'Cancel',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'Discard changes',exact:true}).click();await page.getByRole('button',{name:'Add Agent',exact:true}).waitFor();
 }
 await page.goto(fixtureURL);await page.getByRole('button',{name:'Add Agent',exact:true}).waitFor();
 async function connect(peer,raw,id){
  await page.evaluate(value=>{topologyFixture.agentPeer=value.peer;topologyFixture.rawConnectionResult=value.raw;topologyFixture.agentLinkID=value.id;},{peer,raw,id});
  await page.getByRole('button',{name:'Add Agent',exact:true}).click();
  await page.getByLabel('Other Agent address',{exact:true}).fill('tcp://fixture.example:8793#synthetic');
  await page.getByText('Identity verified',{exact:true}).waitFor();
  await page.locator('.editor-actions .primary').click();
  await page.locator('.topology-inspector').getByRole('heading',{name:peer.name,exact:true}).waitFor();
 }
 const simple={id:'codex',node_id:'simple-codex-node',kind:'codex',name:'Simple Codex'};
 await connect(simple,false,'simple-agent-link');
 assert(await page.locator('.topology-node[aria-pressed=true]').getAttribute('data-node-id')==='peer:'+JSON.stringify([simple.node_id,simple.kind,simple.id]),'The simplified peer.node_id response selects its exact Agent node');
 const raw={id:'codex',node_id:'raw-codex-node',kind:'codex',name:'Raw Codex'};
 await connect(raw,true,'raw-agent-link');
 assert(await page.locator('.topology-node[aria-pressed=true]').getAttribute('data-node-id')==='peer:'+JSON.stringify([raw.node_id,raw.kind,raw.id]),'A raw AgentRef address.node_id response selects its exact Agent node');
 assert(await page.locator('.topology-node').filter({hasText:'Codex'}).count()===3,'Repeated assistant IDs on distinct nodes remain distinct');
 await connect({id:'cloud-assistant',node_id:'cloud-id',kind:'pot',name:'Cloud Pot'},true,'managed-pot-link');
 assert(await page.locator('.topology-node[aria-pressed=true]').getAttribute('data-node-id')==='control:cloud','A Pot node maps to the owning managed Space.control_id');
 assert(await page.locator('.topology-node[data-node-id="control:cloud"]').count()===1,'The managed Control is not duplicated');
 assert(await page.locator('.topology-node.machine').count()===4,'Agent node identity never manufactures Runner inventory');
 assert(await page.evaluate(()=>topologyFixture.client.spaces.every(space=>typeof space.control_id==='string'&&!('node_id' in space))),'Space owner metadata retains control_id');
 assert(!errors.length,'No browser exceptions: '+errors.join('; '));
 return {passed:true,requiredNodeIdentity:true,simplifiedPeerRoute:true,rawAgentRefRoute:true,managedControlMapping:true,spaceControlIdentityPreserved:true};
}
