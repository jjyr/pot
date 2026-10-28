import assert from 'node:assert/strict';
import {spawn,execFileSync} from 'node:child_process';
import {mkdtempSync,rmSync} from 'node:fs';
import {homedir} from 'node:os';
import {join} from 'node:path';
import {createInterface} from 'node:readline';
import {registerHooks} from 'node:module';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';

registerHooks({resolve(specifier,context,next){return specifier==='/wails/runtime.js'?{url:new URL('./fixtures/native-bridge/runtime.mjs',import.meta.url).href,shortCircuit:true}:next(specifier,context);}});
const buildDirectory=process.env.POT_NATIVE_BRIDGE?null:mkdtempSync(join(homedir(),'.pot-native-bridge-'));
let executable=process.env.POT_NATIVE_BRIDGE;
if(buildDirectory){executable=join(buildDirectory,'bridge');try{execFileSync('go',['build','-o',executable,'./scripts/tests/native-bridge'],{stdio:'inherit',env:{...process.env,TMPDIR:buildDirectory,GOTMPDIR:buildDirectory}});}catch(error){rmSync(buildDirectory,{recursive:true,force:true});throw error;}}
const child=spawn(executable,[],{stdio:['pipe','pipe','inherit']});
const handlers=new Map(),pending=new Map(),trace=[],subscriptions=[];let sequence=0;
const emit=(name,data)=>{trace.push({event:name,data});for(const handler of [...handlers.get(name)||[]])handler({data});};
createInterface({input:child.stdout}).on('line',line=>{
  const value=JSON.parse(line);
  if(value.event)emit(value.event,value.data);
  else{const task=pending.get(value.id);pending.delete(value.id);value.error?task.reject(Error(value.error)):task.resolve(value.value);}
});
const command=(method,args=[],extra={})=>new Promise((resolve,reject)=>{const id=++sequence;pending.set(id,{resolve,reject});child.stdin.write(JSON.stringify({id,method,args,...extra})+'\n');});
const exit=new Promise((resolve,reject)=>{child.once('error',reject);child.once('exit',(code,signal)=>{for(const task of pending.values())task.reject(Error('Native fixture exited'));code===0?resolve():reject(Error(`Native fixture exit ${code}/${signal}`));});});
const deadline=setTimeout(()=>child.kill('SIGKILL'),15000);
globalThis.fakeNative={
  on(name,handler){const set=handlers.get(name)||new Set();handlers.set(name,set);set.add(handler);return()=>set.delete(handler);},
  async call(name,...args){const method=name.split('.').at(-1),value=await command(method,args);if(method==='Subscribe')subscriptions.push({token:args[0],stream:value});return value;},
};
const waitFor=async(test)=>{for(let i=0;i<200;i++){if(test())return;await new Promise(resolve=>setTimeout(resolve,5));}throw Error('Synthetic event deadline');};
const frame=(cursor,boot='boot-1')=>({kind:'reset',base_cursor:0,view:{state:{api_version:7,boot_id:boot,conversation_id:'conversation',cursor,agent:{model:'synthetic',phase:'ready'},messages:[]},messages:[],replies:[],history:{before:'',after:'',has_more:false,has_newer:false}}});
const clients=[];let stops=[];
try{
  for(let i=0;i<2;i++){const client=await createProductionClient({desktop:true,resourceStorage:createMemoryProjectionStore()});client.setScope({space:'control',owner:'assistant'});clients.push(client);}
  const frames=[[],[]],status=[[],[]];
  stops=clients.map((client,index)=>client.subscribe(value=>frames[index].push(value.cursor),value=>status[index].push(value)));
  await waitFor(()=>subscriptions.length===2);
  assert.equal(status.flat().includes('connected'),false);
  await command('frame',[],{stream:subscriptions[0].stream,frame:frame(10)});
  assert.deepEqual(frames,[['10'],[]]);
  await command('frame',[],{stream:subscriptions[1].stream,frame:frame(11)});
  assert.deepEqual(frames,[['10'],['11']]);
  await command('disconnect',[],{stream:subscriptions[0].stream});
  const recovered=await command('reconnected');
  const old=trace.find(value=>value.event==='pot:snapshot'&&value.data.token===subscriptions[0].token);
  emit(old.event,{...old.data,snapshot:frame(999)});
  await command('frame',[],{stream:recovered,frame:frame(12)});
  assert.deepEqual(frames,[['10','12'],['11']]);
  assert.equal(subscriptions.length,2,'The recovered Go stream keeps its native subscription; no UI reattachment');
  stops[0]();stops[0]=()=>{};
  emit(old.event,{...old.data,snapshot:frame(1000)});
  emit('pot:subscription',{...old.data,status:'connected',snapshot:undefined});
  assert.deepEqual(frames,[['10','12'],['11']]);
  console.log(JSON.stringify({frames,status,subscriptions,trace},null,2));
}finally{
  for(const stop of stops)stop();for(const client of clients)client.resources.clear();
  try{await command('close');await exit;}finally{clearTimeout(deadline);if(buildDirectory)rmSync(buildDirectory,{recursive:true,force:true});}
}
