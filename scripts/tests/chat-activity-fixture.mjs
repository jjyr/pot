import {createServer} from 'node:http';
import {Readable} from 'node:stream';

// Only synthetic HTTP state. The document, adapter, SSE lifecycle and IndexedDB
// are the product implementation; no model, product profile or Push is used.
export async function chatActivityFixture(sourceOrigin){
 const trace=[],streams=new Set();let revision=1,holdEvents=null;
 const owners=['alpha','short'];
 const messages=new Map(owners.map(owner=>[owner,Array.from({length:owner==='short'?3:800},(_,i)=>({id:`${owner}-${i+1}`,role:i%2?'assistant':'user',text:`${owner} message ${i+1}\n\n`+'Synthetic history content. '.repeat(i%5+1),status:'complete'}))]));
 Object.assign(messages.get('alpha')[689],{reply_to:'alpha-689',text:'Historical partial answer.',status:'failed',error:'Synthetic historical HTTP 404'});
 Object.assign(messages.get('alpha')[691],{reply_to:'alpha-691',text:'',status:'failed',error:'Synthetic historical empty failure'});
 Object.assign(messages.get('alpha')[693],{reply_to:'alpha-693',text:'Historical cancelled answer.',status:'cancelled'});
 const input={id:'active-input',request_id:'active-request',role:'user',text:'Synthetic active turn: keep one stable activity slot.',status:'accepted'};
 messages.get('alpha').push(input);let scenario='thinking';
 const answer=(text,status='streaming')=>({id:'active-answer',reply_to:input.id,role:'assistant',text,status});
 const updateScenario=value=>{scenario=value;const all=messages.get('alpha'),start=all.findIndex(row=>row.id===input.id);all.splice(start+1);
 if(value==='tool-start'||value==='tool-done')all.push({id:'active-tool',role:'tool',text:'',status:'complete',tool:{id:'call-1',turn_id:'active-request',name:'synthetic_inspect',phase:value==='tool-start'?'running':'completed',args:{},result:{content:[{type:'text',text:'Synthetic empty inspection result'}]}}});
 if(value.startsWith('round-')){
  all.push(answer('A persistent progress paragraph.','complete'));
  all.push({id:'empty-round-2',reply_to:input.id,role:'assistant',text:'',status:value==='round-empty'?'streaming':'complete'});
  if(value!=='round-empty')all.push({id:'round-tool',role:'tool',text:'',status:'complete',tool:{id:'round-call',turn_id:'active-request',name:'synthetic_inspect',phase:value==='round-tool'?'running':'completed',args:{},result:{content:[{type:'text',text:'Synthetic inspection result'}]}}});
  if(value==='round-next')all.push({id:'empty-round-3',reply_to:input.id,role:'assistant',text:'',status:'streaming'});
 }
 if(value==='long-progress')all.push(answer(('Long **progress** output for the same active execution.\n\n').repeat(35),'complete'));
 if(value==='parallel-start'||value==='parallel-next'){
  all.push({id:'parallel-input',request_id:'parallel-request',role:'user',text:'A second queued input',status:'accepted'});
  all.push(answer('First execution output arriving after the second input.\n\nThe active status must remain at the visible tail.','complete'));
 }
 if(value==='partial-failure')all.push(answer(JSON.stringify({state:'failed',error:'synthetic failure',partial_output:'A real partial answer.'}),'complete'));
 if(value==='terminal-after-prose')all.push(answer('A persistent progress paragraph.','complete'));
 if(value==='empty-stream')all.push(answer(''));
 if(value==='prose')all.push(answer('A rendered **progress** paragraph.'));
 };
 const caps={version:1,scope:'owner',operations:['chat.read','chat.send','chat.subscribe','chat.cancel','chat.references','task.list']};
 const log=(kind,value={})=>trace.push({at:Date.now(),kind,...value});
 const json=(res,value)=>{res.writeHead(200,{'content-type':'application/json','cache-control':'no-store'});res.end(JSON.stringify(value));};
 const envelopes=(owner,rows)=>rows.map(message=>({message,sequence:String(messages.get(owner).indexOf(message)+1),version:revision}));
 const history=(owner,rows)=>({before:String(messages.get(owner).indexOf(rows[0])+1),after:String(messages.get(owner).indexOf(rows.at(-1))+1),has_more:rows[0]!==messages.get(owner)[0],has_newer:rows.at(-1)!==messages.get(owner).at(-1)});
 const frame=owner=>{const rows=owner==='alpha'?messages.get(owner).slice(720):messages.get(owner).slice(-80);return {kind:'reset',base_cursor:0,view:{state:{cursor:revision,boot_id:'interaction-boot',conversation_id:owner+'-conversation',turn:owner==='alpha'&&!['terminal-empty','terminal-after-prose','partial-failure'].includes(scenario)?{user_message_id:scenario==='parallel-next'?'parallel-input':input.id}:null,agent:{phase:owner==='alpha'?'running':'ready'}},messages:envelopes(owner,rows),replies:owner==='alpha'?[{input_id:input.id,state:scenario==='partial-failure'?'failed':['terminal-empty','terminal-after-prose','parallel-next'].includes(scenario)?'completed':'running',...(scenario==='partial-failure'?{error:'synthetic failure'}:{})},...(scenario.startsWith('parallel-')?[{input_id:'parallel-input',state:scenario==='parallel-next'?'running':'queued'}]:[])]:[],history:history(owner,rows)}};};
 const publish=stream=>{if(stream.path.endsWith('/events')&&holdEvents===stream.owner)return;const payload=stream.path.endsWith('/events')?frame(stream.owner):{assistants:revision};log('sse-frame',{path:stream.path,owner:stream.owner,revision,payload});stream.res.write('event: '+(stream.path.endsWith('/events')?'conversation':'changed')+'\ndata: '+JSON.stringify(payload)+'\n\n');};
 const server=createServer(async(req,res)=>{try{
  const url=new URL(req.url,'http://fixture'),path=url.pathname,owner=url.searchParams.get('assistant_id')||'alpha';
  let body;if(req.method==='POST'){let raw='';for await(const chunk of req)raw+=chunk;body=JSON.parse(raw||'{}');}
  if(path==='/fixture/state')return json(res,{trace,revision});
  if(path==='/fixture/control'){
   if(body.scenario){revision++;updateScenario(body.scenario);log('scenario',{scenario,revision});for(const stream of streams)if(stream.path.endsWith('/events'))publish(stream);}
   if('holdEvents'in body){holdEvents=body.holdEvents;if(!holdEvents)for(const stream of streams)if(stream.path.endsWith('/events'))publish(stream);}
   if(body.close)for(const stream of streams)if(stream.path.endsWith('/'+body.close))stream.res.end();
   return json(res,{});
  }
  if(path.startsWith('/api/')||path==='/access/status')log('http-start',{path,method:req.method,owner,query:url.search,body});
  if(path==='/access/status')return json(res,{authenticated:true});
  if(path==='/api/v1/events'||path==='/api/v1/changes'){
   res.writeHead(200,{'content-type':'text/event-stream','cache-control':'no-cache'});res.write(': open\n\n');
   const stream={res,path,owner};streams.add(stream);publish(stream);
   const timer=setInterval(()=>{log('heartbeat',{path,owner});res.write('event: heartbeat\ndata: {}\n\n');},15000);
   req.on('close',()=>{clearInterval(timer);streams.delete(stream);log('sse-close',{path,owner});});return;
  }
  if(path==='/api/v1/messages'&&body)throw Error('Activity fixture must not send messages');
  if(path==='/api/v1/assistants'){
   log('http-response',{path,owner});return json(res,owners.map(id=>({id,name:'Synthetic '+id,executing:false,revision,capabilities:caps})));
  }
  if(path==='/api/v1/history'){
   const all=messages.get(owner);let index=url.searchParams.has('around')?all.findIndex(message=>message.id===url.searchParams.get('around')):url.searchParams.has('before')?Number(url.searchParams.get('before'))-81:url.searchParams.has('after')?Number(url.searchParams.get('after')):all.length-80;
   if(url.searchParams.has('around'))index=Math.max(0,index-35);
   const rows=all.slice(Math.max(0,index),Math.max(0,index)+80);
   log('history-response',{owner,query:url.search,first:rows[0]?.id,last:rows.at(-1)?.id});
   return json(res,{boot_id:'interaction-boot',conversation_id:owner+'-conversation',cursor:revision,messages:envelopes(owner,rows),history:history(owner,rows)});
  }
  if(path.startsWith('/api/'))return json(res,path==='/api/v1/runtime'?{runtime_id:'interaction-control',boot_id:'interaction-boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Synthetic Space',kind:'local',connected:true,control_id:'interaction-control'}]}:path==='/api/v1/assistants/bootstrap'?{assistant:null}:path==='/api/v1/snapshot'?frame(owner):path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:{items:[],total:0,cursor:0,has_more:false});
  const response=await fetch(sourceOrigin+req.url);res.writeHead(response.status,Object.fromEntries(response.headers));Readable.fromWeb(response.body).pipe(res);
 }catch(error){res.writeHead(500,{'content-type':'application/json'});res.end(JSON.stringify({message:error.stack}));}});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 return {origin:'http://127.0.0.1:'+server.address().port,trace,close:async()=>{for(const stream of streams)stream.res.end();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}};
}
