import {tasksDomainContract} from './tasks-domain-contract.mjs';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createProjectionStore} from '../../web/src/state/projection-store.mjs';
import {domainsFor} from '../../web/src/state/domains/index.mjs';
import {createTaskRepository} from '../../web/src/state/task-repository.mjs';
const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {resolve,promise};};
const wait=async test=>{for(let i=0;i<500;i++){if(await test())return;await new Promise(done=>setTimeout(done,10));}throw Error('Domain contract timed out');};
const remove=name=>new Promise((resolve,reject)=>{const request=indexedDB.deleteDatabase(name);request.onsuccess=resolve;request.onerror=()=>reject(request.error);});
export async function domainRepositoriesContract(report=()=>{}){
  const check=(value,label)=>{if(!value)throw Error(label);report('PASS '+label);};
  const name='pot-domain-'+crypto.randomUUID(),originalFetch=globalThis.fetch;let reads=0,posts=0,hold=deferred(),catalog={profiles:[{id:'p',revision:1,name:'Committed'}]};
  globalThis.fetch=async(path,options={})=>{
    if(path.startsWith('/api/v1/runtime'))return new Response(JSON.stringify({runtime_id:'domain',boot_id:'boot'}));
    if(options.method==='POST'){posts++;return new Response(JSON.stringify({accepted:true}));}
    reads++;await hold.promise;return new Response(JSON.stringify(catalog));
  };
  let client,domains,release;
  try{
    client=await createProductionClient({resourceStorage:createProjectionStore({name})});domains=domainsFor(client);release=domains.retain();const scope={space:'local',owner:'alice'},one=[],two=[];
    const stopOne=domains.providers.watchCatalog(scope,snapshot=>one.push(snapshot)),stopTwo=domains.providers.watchCatalog({...scope,owner:'bob'},snapshot=>two.push(snapshot));
    await wait(()=>reads===1);stopOne();hold.resolve();await wait(()=>two.at(-1)?.fresh);
    check(reads===1&&two.at(-1).data.profiles[0].name==='Committed'&&one.at(-1).status==='loading','Two views share Provider IO; disposing one preserves the other');
    check((await client.resources.query('/api/v1/providers',{space:'local',owner:''})).value.profiles[0].name==='Committed','Domain watch exposes data already committed to IndexedDB');
    stopTwo();const before=one.length;catalog={profiles:[{id:'p',revision:2,name:'Changed'}]};await domains.providers.refresh(scope);check(one.length===before,'Disposed domain consumer receives no refresh');
    const put=IDBObjectStore.prototype.put;IDBObjectStore.prototype.put=function(...args){if(this.name==='projections')throw new DOMException('Synthetic quota','QuotaExceededError');return put.apply(this,args);};
    let accepted=false;try{await domains.tasks.interrupt({id:'t'},scope);}catch(error){accepted=!!error.accepted&&!!error.storage;}finally{IDBObjectStore.prototype.put=put;}
    check(accepted&&posts===1,'Domain command reports server acceptance when local commit aborts');await domains.tasks.interrupt({id:'t'},scope);check(posts===1,'Manual domain retry commits the accepted receipt without repeating HTTP');
  }finally{release?.();client?.resources.clear();globalThis.fetch=originalFetch;await remove(name);}
  const taskName='pot-history-domain-'+crypto.randomUUID(),events=[];
  for(let i=0;i<2400;i++)for(const update of [{type:'message_started',id:'m'+i},{type:'text_delta',text:'message '+i},{type:'message_finished'}])events.push({sequence:events.length+1,event:{update}});
  const scope={space:'local',owner:'a'},task={id:'t',run_id:'run'},requests=[];
  const taskClient={chatCacheName:taskName,runtime:{runtime_id:'task',boot_id:'boot'},request:async(path)=>{const after=Number(new URL(path,location.href).searchParams.get('after'));requests.push(after);const rows=events.slice(after,after+100);return {events:rows,has_more:after+rows.length<events.length};}};
  const repository=createTaskRepository(taskClient);
  try{
    for(let offset=0;offset<events.length;offset+=100)await repository.append(task,scope,{events:events.slice(offset,offset+100)});
    const older=await repository.earlier(task,scope,901);check(older.messages.length===80&&requests[0]===0&&requests.length===9,'First evicted page reconstructs the bounded prefix through after-only HTTP');
    const count=requests.length,adjacent=await repository.earlier(task,scope,older.messages[0].sequence);check(adjacent.messages.length===80&&requests.length===count,'Neighboring historical page reuses committed backfill without replay from zero');
    const live=await repository.read(task,scope);check(live.cursor===7200&&live.messages.at(-1).text==='message 2399','Backfill preserves the live cursor and current window');
    const cancelled=new AbortController();cancelled.abort();let abort=false;try{await repository.earlier(task,scope,adjacent.messages[0].sequence,{signal:cancelled.signal});}catch(error){abort=error.name==='AbortError';}check(abort,'Cancelled history consumers cannot read or render a page');
    const raceTask={id:'late',run_id:'run'};await repository.append(raceTask,scope,{events});
    const gate=deferred(),started=deferred();taskClient.request=async()=>{started.resolve();await gate.promise;return {events:events.slice(0,100),has_more:false};};const late=repository.earlier(raceTask,scope,100).then(()=>false,error=>error.name==='AbortError');await started.promise;
    await repository.append({...raceTask,run_id:'next'},scope,{events:[{sequence:1,event:{update:{type:'message_started',id:'next'}}}]});gate.resolve();check(await late,'Old run backfill cannot commit over a new conversation');
    check((await repository.read({...raceTask,run_id:'next'},scope)).messages[0].id==='message-next','New run remains intact after a late historical response');
    taskClient.scope=()=>scope;taskClient.request=async()=>({run_id:'run',info:{phase:'running'},capabilities:{version:1,operations:['task.read']}});
    const taskDomains=domainsFor(taskClient),releaseTasks=taskDomains.retain(),a=[],b=[];
    const stopA=taskDomains.tasks.watchTranscript(task,scope,snapshot=>a.push(snapshot)),stopB=taskDomains.tasks.watchTranscript(task,scope,snapshot=>b.push(snapshot));stopA();const disposedCount=a.length;
    await wait(()=>b.at(-1)?.data?.cursor===7200);check(a.length===disposedCount&&b.at(-1).data.messages.at(-1).text==='message 2399','Transcript watch first reads IndexedDB and shares background confirmation after one view disposes');stopB();releaseTasks();
  }finally{repository.close();await remove(taskName+'-tasks');}
  await tasksDomainContract(report);
  report('DONE domain repositories contract');
}
