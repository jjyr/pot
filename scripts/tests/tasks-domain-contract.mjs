import {createTasksDomain} from '../../web/src/state/domains/tasks.mjs';
import {createQueryRegistry,createCommands} from '../../web/src/state/domains/query.mjs';

// Exercise the domain boundary: testing createTaskPages alone missed a dropped
// onProgress callback in the domain's listQuery adapter.
export async function tasksDomainContract(report=()=>{}) {
  const check=(condition,label)=>{if(!condition)throw Error(label);report('PASS '+label);};
  const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {resolve,promise};};
  const flush=async()=>{for(let i=0;i<32;i++)await Promise.resolve();};
  const slow=deferred(),retired=deferred(),calls=[],updates=[],later=[];
  const resources={subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{},state:()=>({freshness:'fresh'})};
  const queries=createQueryRegistry(resources),commands=createCommands({});
  const client={spaces:[{id:'fast',control_id:'a'},{id:'slow',control_id:'b'}],async read(path,scope){
    calls.push({path,space:scope.space,owner:scope.owner});
    if(path==='/api/v1/runtime')return {};
    const q=new URL(path,'http://fixture').searchParams.get('q');
    if(q==='retired')await retired.promise;
    else if(q==='initial'&&scope.space==='slow')await slow.promise;
    return {items:[{id:scope.space+'-'+q}],total:1,next_offset:null};
  }};
  const domain=createTasksDomain(client,{resources,queries,commands});let stop,stopOld,stopNew;
  try {
    const writes=[];client.request=async(path,options)=>{writes.push({path,...options});return {accepted:true};};
    const captured={space:'fast',owner:'original'},task={id:'task',run_id:'run-one'};
    const pause=domain.pause(task,captured,true);captured.owner='changed';await pause;
    check(writes[0].owner==='original'&&writes[0].space==='fast'&&writes[0].body.run_id==='run-one'&&writes[0].body.action==='pause','Task controls capture original owner, Space and run before dispatch');
    const first=domain.sendInput(task,captured,'intent','same-intent'),second=domain.sendInput(task,captured,'intent','same-intent');
    check(first===second,'Duplicate Task input shares one in-flight intent');await first;
    check(writes.length===2&&writes[1].body.request_id==='same-intent','Task guidance sends its explicit idempotency identity once');
    await domain.archive(task,captured,true);
    check(writes[2].body.decisions[0].run_id==='run-one'&&writes[2].body.decisions[0].task_id==='task','Task archive retains exact task and run identity');
    stop=domain.watchList({scope:'active',q:'initial'},state=>updates.push(state));
    await flush();
    check(updates.at(-1)?.data?.partial===true&&updates.at(-1).status==='loading'&&updates.at(-1).data.pages[0].space.id==='fast','Domain publishes fast Space while slow Space is unresolved');
    check(calls.filter(call=>call.path.includes('/catalog?')).length===2,'Domain reads each source once and retains explicit owner scope');
    check(calls.every(call=>call.owner===''),'Catalogue and host metadata never inherit the selected assistant');
    slow.resolve();await domain.listQuery({scope:'active',q:'initial'}).value();
    check(updates.at(-1).fresh&&updates.at(-1).data.pages.length===2,'Domain confirms both sources after the final response');
    stopOld=domain.watchList({scope:'active',q:'retired'},state=>later.push(state));
    await flush();stopOld();const length=later.length;
    const replacement=[];stopNew=domain.watchList({scope:'active',q:'replacement'},state=>replacement.push(state));
    await domain.listQuery({scope:'active',q:'replacement'}).value();
    retired.resolve();await domain.listQuery({scope:'active',q:'retired'}).value();
    check(later.length===length,'Disposed search consumer ignores late source completion');
    check(replacement.at(-1).data.pages.every(page=>page.value.items[0].id.endsWith('-replacement')),'Late prior query cannot replace the current scope results');
  }finally{slow.resolve();retired.resolve();stop?.();stopOld?.();stopNew?.();domain.dispose();queries.dispose();commands.dispose();}
}
