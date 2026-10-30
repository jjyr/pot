import assert from 'node:assert/strict';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import {createMemoryResourceCopies} from './resource-copies-memory.mjs';
import {installWSFixture} from './helpers/ws-fixture.mjs';
import {getBusinessSocket} from '../../web/src/adapters/ws-client.mjs';

const fixture=installWSFixture();
const client=await createProductionClient({startupIdentity:{runtime_id:'synthetic',boot_id:'boot'},resourceStorage:createMemoryProjectionStore(),resourceCopies:createMemoryResourceCopies()});
client.confirmStartup({runtime_id:'synthetic',boot_id:'boot'});
const received=[];
const stop=client.changes((topics,scope)=>received.push({topics,scope}),{space:'local'});
try{
  await fixture.flush();
  const socket=fixture.sockets[0];
  assert.ok(socket,'changes transport must open a websocket');
  const subscriptionId=[...socket.subscriptions].find(([,path])=>typeof path==='string'&&path.startsWith('/api/v1/changes'))?.[0];
  assert.ok(subscriptionId!=null,'changes transport must subscribe to /api/v1/changes');
  fixture.emit('/changes','changed',{topics:{permissions:1}},'boot:1');
  await fixture.flush();
  const before=received.length;
  client.suspendStartup();
  client.confirmStartup({runtime_id:'synthetic',boot_id:'boot'});
  socket.emit({type:'event',id:subscriptionId,event:'changed',data:{topics:{permissions:2}},event_id:'retired-boot:2'});
  await fixture.flush();
  console.log(JSON.stringify({before,after:received.length,health:client.changesHealthy('local'),received}));
  assert.equal(received.length,before,'Retired browser changes must not notify a replacement connection');
}finally{
  stop();
  client.resources.clear();
  client.resourceCopies.close();
  try{getBusinessSocket()?.close();}catch{}
  fixture.restore();
}
