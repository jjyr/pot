import {createServer} from 'node:http';
import {Readable} from 'node:stream';

// Only synthetic HTTP state. The document, adapter, SSE lifecycle and IndexedDB
// are the product implementation; no model, product profile or Push is used.
export async function chatInteractionsFixture(sourceOrigin){
 const trace=[],streams=new Set(),held=new Set();let revision=1,executing=true,holdDirectory=false,holdChat=false,sendAllowed=true;
 const messages=new Map(['alpha','beta','short'].map(owner=>[owner,Array.from({length:owner==='short'?3:800},(_,i)=>({id:`${owner}-${i+1}`,role:i%2?'assistant':'user',text:`${owner} message ${i+1}\n\n`+('Synthetic history content. '.repeat(i%5+1)),status:'complete'}))]));
 const caps={version:1,scope:'owner',operations:['chat.read','chat.send','chat.subscribe','chat.cancel','chat.references','task.list']};
 const log=(kind,value={})=>trace.push({at:Date.now(),kind,...value});
 const json=(res,value)=>{res.writeHead(200,{'content-type':'application/json','cache-control':'no-store'});res.end(JSON.stringify(value));};
 const envelopes=(owner,rows)=>rows.map(message=>({message,sequence:String(messages.get(owner).indexOf(message)+1),version:revision}));
 const history=(owner,rows)=>({before:String(messages.get(owner).indexOf(rows[0])+1),after:String(messages.get(owner).indexOf(rows.at(-1))+1),has_more:rows[0]!==messages.get(owner)[0],has_newer:rows.at(-1)!==messages.get(owner).at(-1)});
 const frame=owner=>{const rows=messages.get(owner).slice(-80);return {kind:'reset',base_cursor:0,view:{state:{cursor:revision,boot_id:'interaction-boot',conversation_id:owner+'-conversation',turn:null,agent:{phase:'ready'}},messages:envelopes(owner,rows),replies:[],history:history(owner,rows)}};};
 const publish=stream=>{log('sse-frame',{path:stream.path,owner:stream.owner,revision});stream.res.write('event: '+(stream.path.endsWith('/events')?'conversation':'changed')+'\ndata: '+JSON.stringify(stream.path.endsWith('/events')?frame(stream.owner):{assistants:revision})+'\n\n');};
 const server=createServer(async(req,res)=>{try{
  const url=new URL(req.url,'http://fixture'),path=url.pathname,owner=url.searchParams.get('assistant_id')||'alpha';
  let body;if(req.method==='POST'){let raw='';for await(const chunk of req)raw+=chunk;body=JSON.parse(raw||'{}');}
  if(path==='/fixture/state')return json(res,{trace,revision});
  if(path==='/fixture/control'){
   log('control',body);
   if('executing'in body)executing=body.executing;
   if('sendAllowed'in body)sendAllowed=body.sendAllowed;
   if('holdChat'in body){holdChat=body.holdChat;if(!holdChat)for(const stream of streams)if(stream.path.endsWith('/events'))publish(stream);}
   if('holdDirectory'in body){holdDirectory=body.holdDirectory;if(!holdDirectory){for(const release of held)release();held.clear();}}
   if(body.close)for(const stream of streams)if(stream.path.endsWith('/'+body.close))stream.res.end();
   if(body.background){messages.get('alpha').push({id:'background-'+(++revision),role:'assistant',text:'Synthetic background update',status:'complete'});for(const stream of streams)if(stream.path.endsWith('/events'))publish(stream);}
   if(body.directory){revision++;for(const stream of streams)if(stream.path.endsWith('/changes'))publish(stream);}
   return json(res,{});
  }
  if(path.startsWith('/api/')||path==='/access/status')log('http-start',{path,method:req.method,owner,body});
  if(path==='/access/status')return json(res,{authenticated:true});
  if(path==='/api/v1/events'||path==='/api/v1/changes'){
   res.writeHead(200,{'content-type':'text/event-stream','cache-control':'no-cache'});res.write(': open\n\n');
   const stream={res,path,owner};streams.add(stream);if(!path.endsWith('/events')||!holdChat)publish(stream);
   const timer=setInterval(()=>{log('heartbeat',{path,owner});res.write('event: heartbeat\ndata: {}\n\n');},15000);
   req.on('close',()=>{clearInterval(timer);streams.delete(stream);log('sse-close',{path,owner});});return;
  }
  if(path==='/api/v1/messages'&&body){
   revision++;messages.get(owner).push({id:body.request_id,request_id:body.request_id,role:'user',text:body.text,references:body.references,attachments:body.attachments,status:'complete'});
   json(res,{message_id:body.request_id,duplicate:false});log('http-response',{path,owner,request:body.request_id});
   for(const stream of streams)if(stream.path.endsWith('/events')&&stream.owner===owner)publish(stream);return;
  }
  if(path==='/api/v1/assistants'){
   if(holdDirectory)await new Promise(resolve=>held.add(resolve));
   log('http-response',{path,owner,executing,sendAllowed});return json(res,['alpha','beta','short'].map(id=>({id,name:'Synthetic '+id,executing,revision,capabilities:sendAllowed?caps:{...caps,operations:['chat.read','chat.subscribe']}})));
  }
  if(path==='/api/v1/history'){
   const all=messages.get(owner);let index=url.searchParams.has('around')?all.findIndex(message=>message.id===url.searchParams.get('around')):url.searchParams.has('before')?Number(url.searchParams.get('before'))-81:url.searchParams.has('after')?Number(url.searchParams.get('after')):all.length-80;
   if(url.searchParams.has('around'))index=Math.max(0,index-35);
   const rows=all.slice(Math.max(0,index),Math.max(0,index)+80);
   return json(res,{boot_id:'interaction-boot',conversation_id:owner+'-conversation',cursor:revision,messages:envelopes(owner,rows),history:history(owner,rows)});
  }
  if(path.startsWith('/api/'))return json(res,path==='/api/v1/runtime'?{runtime_id:'interaction-control',boot_id:'interaction-boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Synthetic Space',kind:'local',connected:true,control_id:'interaction-control'}]}:path==='/api/v1/assistants/bootstrap'?{assistant:null}:path==='/api/v1/snapshot'?frame(owner):path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:{items:[],total:0,cursor:0,has_more:false});
  const response=await fetch(sourceOrigin+req.url);res.writeHead(response.status,Object.fromEntries(response.headers));Readable.fromWeb(response.body).pipe(res);
 }catch(error){res.writeHead(500,{'content-type':'application/json'});res.end(JSON.stringify({message:error.stack}));}});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 return {origin:'http://127.0.0.1:'+server.address().port,trace,close:async()=>{for(const release of held)release();for(const stream of streams)stream.res.end();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}};
}
