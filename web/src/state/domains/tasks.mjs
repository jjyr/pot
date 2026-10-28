import {resourceAuthority,resourceAuthorityCurrent} from '../resource-authority.mjs';
import {translate} from '../../locales/messages.mjs';
import {machinePresentation} from '../machine-identity.mjs';
import {createTaskRepository,assertTaskEventRun} from '../task-repository.mjs';
import {createTaskPages} from '../task-pages.mjs';
import {createTaskTranscript} from '../task-transcript.mjs';
import {supportsCapability} from '../agent-links.mjs';
export function createTasksDomain(client,{resources,queries,commands,transcriptRepository}){
  let transcript=transcriptRepository,closed=false;const catchupTimers=new Map(),confirmedRuns=new Map();const transcripts=()=>transcript ||= createTaskRepository(client),pages=createTaskPages(client);
  const current=()=>{if(closed)throw new DOMException('Domain disposed','AbortError');};
  const path=id=>'/api/v1/tasks/'+encodeURIComponent(id),ref=(task,scope)=>JSON.stringify([scope.space,scope.owner,task.id,task.run_id]);
  const detail=(task,scope)=>{let confirmedRun=task.run_id;return queries.define('task:'+ref(task,scope),async(force,publish)=>{
    const authority=resourceAuthority(client,scope,resources),runKey=JSON.stringify([authority,task.id]);let run;const check=()=>{current();if(run&&confirmedRuns.has(runKey)&&confirmedRuns.get(runKey).run!==run||!resourceAuthorityCurrent(client,scope,authority,resources))throw new DOMException('Task authority retired','AbortError');};
    let record=await resources.read(path(task.id),{...scope,authoritative:force||'invalidated'});check();
    if(confirmedRun&&record.run_id!==confirmedRun){
      record=await resources.read(path(task.id),{...scope,authoritative:true});check();
    }
    const freshness=resources.state(record)?.freshness||'fresh';
    if(record.previous_run_id&&!record.launch&&record.previous_run_id!==record.run_id)throw Object.assign(Error(translate(client.uiLocale?.()||'en','loading.stale')),{uiKey:'loading.stale'});
    if(confirmedRun&&record.run_id!==confirmedRun&&freshness!=='fresh')throw Object.assign(Error(translate(client.uiLocale?.()||'en','loading.stale')),{uiKey:'loading.stale'});
    if(freshness==='fresh'){
      const known=confirmedRuns.get(runKey);
      if(known&&Number.isSafeInteger(known.revision)&&Number.isSafeInteger(record.revision)&&(record.revision<known.revision||record.revision===known.revision&&record.run_id!==known.run))throw new DOMException('Task metadata retired','AbortError');
      confirmedRun=run=record.run_id;confirmedRuns.delete(runKey);confirmedRuns.set(runKey,{run,revision:record.revision});
      while(confirmedRuns.size>128)confirmedRuns.delete(confirmedRuns.keys().next().value);
    }
    check();const taskRef={...task,run_id:record.run_id};let state=await transcripts().read(taskRef,scope)||createTaskTranscript(),inputs=[],storageError=false,connectionError='';
    check();publish({data:{authority,record,state,inputs:[],fresh:false},freshness:'retained'});
    if(freshness!=='fresh')return {data:{authority,record,state,inputs:[],fresh:false},freshness:'retained'};
    try{
      const jobs=await Promise.allSettled([
        supportsCapability(record,'task.input')?resources.read(path(task.id)+'/messages',{...scope,authoritative:force}):Promise.resolve([]),
        (async()=>{if(!supportsCapability(record,'task.events'))return {};let count=0,page;do{check();page=await client.request(path(task.id)+'/events?'+new URLSearchParams({after:state.cursor,limit:100}),{...scope,expectedControl:resources.describe?.(path(task.id),scope)?.control});check();assertTaskEventRun(taskRef,page,client.uiLocale?.()||'en');const previous=state.cursor;state=await transcripts().append(taskRef,scope,page,{validate:check});check();if(page.has_more&&previous===state.cursor)throw Error('task_event_gap');count+=(page.events||[]).length;}while(page.has_more&&count<5000);return page;})(),
      ]);
      check();if(jobs[0].status==='fulfilled')inputs=jobs[0].value;
      const failures=jobs.filter(job=>job.status==='rejected');storageError=failures.some(job=>job.reason.storage);connectionError=failures.map(job=>job.reason.message).join('\n');
      const catchingUp=!failures.length&&!!jobs[1].value?.has_more;
      if(catchingUp&&!catchupTimers.has(ref(task,scope))){
        const key=ref(task,scope);catchupTimers.set(key,setTimeout(()=>{catchupTimers.delete(key);if(closed)return;const query=detail(task,scope);if(query.active())query.requery();},0));
      }
      return {data:{authority,record,state,catchingUp,inputs:Array.isArray(inputs)?inputs:inputs.items||[],inputsTruncated:!!inputs.cache_truncated,unavailable:!!jobs[1].value?.unavailable,storageError,connectionError,fresh:freshness==='fresh'&&!failures.length&&!catchingUp&&!jobs[1].value?.unavailable},freshness:failures.length?'stale':catchingUp||jobs[1].value?.unavailable?'retained':freshness,lastConfirmed:resources.state(record)?.lastConfirmed};
    }catch(error){throw Object.assign(error,{storage:storageError});}
  },{paths:[path(task.id),path(task.id)+'/messages'],topics:['tasks','runner','spaces'],space:scope.space,owner:scope.owner,publishIntermediate:true});};
  const active=scope=>queries.define('active-tasks:'+JSON.stringify(scope),async force=>{const items=[];let offset=0,stale=false;const seen=new Set();do{if(seen.has(offset))throw Error('task_pagination');seen.add(offset);const value=await resources.read('/api/v1/tasks/query?'+new URLSearchParams({scope:'active',limit:50,offset}),{...scope,authoritative:force});stale ||= resources.state(value)?.freshness!=='fresh';items.push(...(value.items||[]));offset=value.next_offset;}while(offset!=null);return {data:items.filter(task=>['queued','running','waiting','waiting_approval'].includes(task.phase)).map(task=>({...task,_space_id:scope.space,assistant_id:scope.owner,_stale:stale})),freshness:stale?'retained':'fresh'};},{paths:['/api/v1/tasks/query'],topics:['tasks'],space:scope.space,owner:scope.owner});
  const command=(scope,task,operation,input,send,options)=>{
    const authority=task._authority||resourceAuthority(client,scope,resources);
    return commands.run(scope,'task:'+task.id+':'+(task.run_id||'')+':'+operation,input,(...args)=>{
      if(task._valid?.()===false||task.run_id&&confirmedRuns.get(JSON.stringify([authority,task.id]))?.run&&confirmedRuns.get(JSON.stringify([authority,task.id])).run!==task.run_id||!resourceAuthorityCurrent(client,args[2],authority,resources))throw Object.assign(Error(translate(client.uiLocale?.()||'en','identityChanged')),{uiKey:'identityChanged'});
      return send(...args);
    },options);
  };
  const post=(task,suffix,body,scope)=>client.request(path(task.id)+suffix,{...scope,expectedControl:resources.describe?.(path(task.id),scope)?.control,method:'POST',body});
  return {
    metadata:(task,scope,options={})=>resources.read(path(task.id),{...scope,...options}),
    readTranscript:(task,scope)=>transcripts().read(task,scope),watchTranscript(task,scope,listener){
      const query=queries.define('task-transcript:'+ref(task,scope),async()=>{const snapshot=detail(task,scope).snapshot;if(snapshot.error)throw snapshot.error;return {data:await transcripts().read(task,scope),freshness:snapshot.fresh?'fresh':'retained',lastConfirmed:snapshot.lastConfirmed};},{topics:['tasks','runner'],space:scope.space,invalidationForce:false,subscribe:notify=>{const stop=transcripts().subscribe(task,scope,notify),stopDetail=detail(task,scope).watch(notify);return()=>{stop();stopDetail();};}});
      return query.watch(listener);
    },
    detail,watchDetail:(task,scope,listener)=>detail(task,{...scope}).watch(listener),refreshDetail:(task,scope)=>detail(task,scope).refresh(true),
    list:query=>pages(query),listQuery(query){const key='task-list:'+JSON.stringify({...query,signal:undefined,onProgress:undefined});return queries.define(key,async(force,publish)=>{const [data]=await Promise.all([pages({...query,signal:undefined,authoritative:force,onProgress:pages=>publish({data:{pages,partial:true},status:'loading',freshness:'retained'})}),...client.spaces.map(space=>machinePresentation(client).load({space:space.id}))]);return {data,freshness:data.partial?'retained':'fresh'};},{paths:['/api/v1/tasks/catalog','/api/v1/assistants','/api/v1/runtime'],topics:['tasks','assistants','spaces','machines','runtime']});},
    watchList(query,listener){return this.listQuery(query).watch(listener);},refreshList(query){return this.listQuery(query).refresh(true);},
    active,watchActive:(scope,listener)=>active({...scope}).watch(listener),
    loadEarlier:(task,scope,before,options={})=>{
      const authority=resourceAuthority(client,scope,resources),runKey=JSON.stringify([authority,task.id]);
      const validate=()=>{current();options.validate?.();if(confirmedRuns.get(runKey)?.run!==task.run_id||!resourceAuthorityCurrent(client,scope,authority,resources))throw new DOMException('Task authority retired','AbortError');};
      return transcripts().earlier(task,scope,before,{...options,validate});
    },latest:(task,scope)=>transcripts().read(task,scope),
    sendInput:(task,scope,text,requestID)=>command(scope,task,'input',{text},(input,id,captured)=>post(task,'/messages',{...input,request_id:id},captured),{requestID,idempotent:true}),
    interrupt:(task,scope)=>command(scope,task,'interrupt',{},(_,id,captured)=>post(task,'/interrupt',{request_id:id},captured),{idempotent:true}),
    cancel:(task,scope)=>command(scope,task,'cancel',{},(_,id,captured)=>post(task,'/cancel',{request_id:id},captured),{idempotent:true}),
    pause:(task,scope,paused)=>command(scope,task,'pause',{run_id:task.run_id,action:paused?'pause':'resume'},(body,_,captured)=>post(task,'/control',body,captured)),
    archive:(task,scope,archived)=>command(scope,task,'archive',{task_id:task.id,run_id:task.run_id,archived,reason:''},(decision,_,captured)=>client.request('/api/v1/tasks/dispositions',{...captured,expectedControl:resources.describe?.('/api/v1/tasks/dispositions',captured)?.control,method:'POST',body:{decisions:[decision]}})),
    resume:(task,scope,wait)=>command(scope,task,'resume',wait,(body,id,captured)=>post(task,'/resume',{...body,request_id:id},captured),{idempotent:true}),
    clear:()=>pages.clear(),watchCommands:commands.watch,dispose(){closed=true;for(const timer of catchupTimers.values())clearTimeout(timer);catchupTimers.clear();transcript?.close();},
  };
}
