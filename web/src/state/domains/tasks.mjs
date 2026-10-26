import {machinePresentation} from '../machine-identity.mjs';
import {createTaskRepository} from '../task-repository.mjs';
import {createTaskPages} from '../task-pages.mjs';
import {createTaskTranscript} from '../task-transcript.mjs';
import {supportsCapability} from '../agent-links.mjs';
export function createTasksDomain(client,{resources,queries,commands}){
  let transcript,closed=false;const transcripts=()=>transcript ||= createTaskRepository(client),pages=createTaskPages(client);
  const current=()=>{if(closed)throw new DOMException('Domain disposed','AbortError');};
  const path=id=>'/api/v1/tasks/'+encodeURIComponent(id),ref=(task,scope)=>JSON.stringify([scope.space,scope.owner,task.id,task.run_id]);
  const detail=(task,scope)=>queries.define('task:'+ref(task,scope),async(force,publish)=>{
    const record=await resources.read(path(task.id),{...scope,authoritative:force}),freshness=resources.state(record)?.freshness||'fresh';
    const taskRef={...task,run_id:record.run_id};let state=await transcripts().read(taskRef,scope)||createTaskTranscript(),inputs=[],storageError=false,connectionError='';
    if(state.cursor)publish({data:{record,state,inputs:[],fresh:false},freshness:'retained'});
    try{
      const jobs=await Promise.allSettled([
        supportsCapability(record,'task.input')?resources.read(path(task.id)+'/messages',{...scope,authoritative:force}):Promise.resolve([]),
        (async()=>{if(!supportsCapability(record,'task.events'))return {};let count=0,page;do{current();page=await client.request(path(task.id)+'/events?'+new URLSearchParams({after:state.cursor,limit:100}),scope);current();const previous=state.cursor;state=await transcripts().append(taskRef,scope,page);if(page.has_more&&previous===state.cursor)throw Error('task_event_gap');count+=(page.events||[]).length;}while(page.has_more&&count<5000);return page;})(),
      ]);
      if(jobs[0].status==='fulfilled')inputs=jobs[0].value;
      const failures=jobs.filter(job=>job.status==='rejected');storageError=failures.some(job=>job.reason.storage);connectionError=failures.map(job=>job.reason.message).join('\n');
      return {data:{record,state,inputs:Array.isArray(inputs)?inputs:inputs.items||[],inputsTruncated:!!inputs.cache_truncated,unavailable:!!jobs[1].value?.unavailable,storageError,connectionError,fresh:freshness==='fresh'&&!failures.length},freshness:failures.length?'stale':freshness,lastConfirmed:resources.state(record)?.lastConfirmed};
    }catch(error){throw Object.assign(error,{storage:storageError});}
  },{topics:['tasks','runner','spaces'],space:scope.space});
  const active=scope=>queries.define('active-tasks:'+JSON.stringify(scope),async force=>{const items=[];let offset=0,stale=false;const seen=new Set();do{if(seen.has(offset))throw Error('task_pagination');seen.add(offset);const value=await resources.read('/api/v1/tasks/query?'+new URLSearchParams({scope:'active',limit:50,offset}),{...scope,authoritative:force});stale ||= resources.state(value)?.freshness!=='fresh';items.push(...(value.items||[]));offset=value.next_offset;}while(offset!=null);return {data:items.filter(task=>['queued','running','waiting','waiting_approval'].includes(task.phase)).map(task=>({...task,_space_id:scope.space,assistant_id:scope.owner,_stale:stale})),freshness:stale?'retained':'fresh'};},{topics:['tasks','runner'],space:scope.space});
  const command=(scope,task,operation,input,send,options)=>commands.run(scope,'task:'+task.id+':'+(task.run_id||'')+':'+operation,input,send,options);
  const post=(task,suffix,body,scope)=>client.request(path(task.id)+suffix,{...scope,method:'POST',body});
  return {
    metadata:(task,scope,options={})=>resources.read(path(task.id),{...scope,...options}),
    readTranscript:(task,scope)=>transcripts().read(task,scope),watchTranscript(task,scope,listener){
      const query=queries.define('task-transcript:'+ref(task,scope),async()=>{const snapshot=detail(task,scope).snapshot;if(snapshot.error)throw snapshot.error;return {data:await transcripts().read(task,scope),freshness:snapshot.fresh?'fresh':'retained',lastConfirmed:snapshot.lastConfirmed};},{topics:['tasks','runner'],space:scope.space,invalidationForce:false,subscribe:notify=>{const stop=transcripts().subscribe(task,scope,notify),stopDetail=detail(task,scope).watch(notify);return()=>{stop();stopDetail();};}});
      return query.watch(listener);
    },
    detail,watchDetail:(task,scope,listener)=>detail(task,{...scope}).watch(listener),refreshDetail:(task,scope)=>detail(task,scope).refresh(true),
    list:query=>pages(query),listQuery(query){const key='task-list:'+JSON.stringify({...query,signal:undefined,onProgress:undefined});return queries.define(key,async(force,publish)=>{const [data]=await Promise.all([pages({...query,signal:undefined,authoritative:force,onProgress:pages=>publish({data:{pages,partial:true},status:'loading',freshness:'retained'})}),...client.spaces.map(space=>machinePresentation(client).load({space:space.id}))]);return {data,freshness:data.partial?'retained':'fresh'};},{topics:['tasks','runner','assistants','spaces','machines','runtime']});},
    watchList(query,listener){return this.listQuery(query).watch(listener);},refreshList(query){return this.listQuery(query).refresh(true);},
    active,watchActive:(scope,listener)=>active({...scope}).watch(listener),
    loadEarlier:(task,scope,before,options)=>transcripts().earlier(task,scope,before,options),latest:(task,scope)=>transcripts().read(task,scope),
    sendInput:(task,scope,text,requestID)=>command(scope,task,'input',{text},(input,id,captured)=>post(task,'/messages',{...input,request_id:id},captured),{requestID,idempotent:true}),
    interrupt:(task,scope)=>command(scope,task,'interrupt',{},(_,id,captured)=>post(task,'/interrupt',{request_id:id},captured),{idempotent:true}),
    cancel:(task,scope)=>command(scope,task,'cancel',{},(_,id,captured)=>post(task,'/cancel',{request_id:id},captured),{idempotent:true}),
    pause:(task,scope,paused)=>command(scope,task,'pause',{run_id:task.run_id,action:paused?'pause':'resume'},(body,_,captured)=>post(task,'/control',body,captured)),
    archive:(task,scope,archived)=>command(scope,task,'archive',{task_id:task.id,run_id:task.run_id,archived,reason:''},(decision,_,captured)=>client.request('/api/v1/tasks/dispositions',{...captured,method:'POST',body:{decisions:[decision]}})),
    resume:(task,scope,wait)=>command(scope,task,'resume',wait,(body,id,captured)=>post(task,'/resume',{...body,request_id:id},captured),{idempotent:true}),
    clear:()=>pages.clear(),watchCommands:commands.watch,dispose(){closed=true;transcript?.close();},
  };
}
