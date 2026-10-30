import {createServer} from 'node:http';
import {installFixtureWebSocket} from './helpers/ws-server.mjs';
import {Readable} from 'node:stream';
import {DatabaseSync} from 'node:sqlite';

// Production UI, adapter, WebSocket, IDB and SW against disposable HTTP data.
// SQLite stores only synthetic send snapshots; no model or user store is used.
export async function attachmentPickerFixture(sourceOrigin,{durableReceipts=false}={}){
 const states=new Map(),db=new DatabaseSync(':memory:');
 db.exec('CREATE TABLE accepted (scenario TEXT, owner TEXT, request TEXT, body TEXT, PRIMARY KEY(scenario,owner,request))');
 const json=(res,value,status=200)=>{res.writeHead(status,{'content-type':'application/json','cache-control':'no-store'});res.end(JSON.stringify(value));};
 const caps={version:1,scope:'owner',operations:['chat.read','chat.send','chat.steer','chat.retry','chat.subscribe','chat.completions','chat.attachments','chat.references','chat.continue','chat.cancel','task.list','task.read','task.events','task.outcomes']};
 const get=name=>{if(!states.has(name))states.set(name,{trace:[],streams:new Set(),uploads:new Map(),image:true,pdf:true,permission:true,holdInitial:false,holdSend:false,holdUploads:false,held:[],receipt:false,revision:1});return states.get(name);};
 const frame=(s,owner)=>({kind:'reset',base_cursor:0,view:{state:{cursor:s.revision,boot_id:'picker-boot',conversation_id:s.conversation||'main',messages:[],continuation:owner==='alpha'?s.continuation:null,turn:s.running?{user_message_id:'active-turn'}:null,agent:{phase:'ready',attachments:{image:s.image,pdf:s.pdf}}},messages:(s.messages||(s.references?[{id:owner+'-source',role:'assistant',text:'Synthetic reference source for '+owner,status:'complete'}]:[])).map((message,i)=>({message,sequence:String(i+1),version:s.revision})),replies:[],history:{has_more:false,has_newer:false}}});
 const send=stream=>stream.res.write('event: conversation\ndata: '+JSON.stringify(frame(stream.state,stream.owner))+'\n\n');
 const server=createServer(async(req,res)=>{try{
  const url=new URL(req.url,'http://fixture'),p=url.pathname,name=/picker-case=([^;]+)/.exec(req.headers.cookie||'')?.[1]||'manual',s=get(name),owner=url.searchParams.get('assistant_id')||'alpha';
  let body;if(req.method==='POST'){let raw='';for await(const chunk of req)raw+=chunk;body=JSON.parse(raw||'{}');}
  if(p==='/fixture/control'){
   for(const key of ['image','pdf','permission','holdInitial','holdSend','holdUploads','receipt','running','conversation','uploadFailure','continuation','messages','holdContinue','continueError','references','rejectSend','acceptSend','dropAfterAccept','dropBeforeAccept','receiptForbidden','hideAssistant','avatar'])if(key in body)s[key]=body[key];
   if(body.releaseContinue){s.releaseContinue?.();s.releaseContinue=null;}
   if(body.releaseSends)for(const entry of (s.heldSends||[]).splice(0)){json(entry.res,{message_id:entry.message_id,duplicate:entry.duplicate});s.trace.push({kind:'post-response',owner:entry.owner,request_id:entry.message_id,duplicate:entry.duplicate});}
   if(body.releaseUploads){for(const release of s.held.splice(0))release();}
   if(body.publish||body.release){s.revision++;for(const stream of s.streams)if(stream.path.endsWith('/events'))send(stream);}
   if(body.directory){s.revision++;for(const stream of s.streams)if(stream.path.endsWith('/changes'))stream.res.write('event: changed\ndata: '+JSON.stringify({assistants:s.revision})+'\n\n');}
   return json(res,{});
  }
  if(p==='/fixture/state')return json(res,{trace:s.trace,rows:db.prepare('SELECT owner,body FROM accepted WHERE scenario=?').all(name).map(row=>({...row,body:JSON.parse(row.body)}))});
  if(p==='/access/status')return json(res,{authenticated:true});
  if(p==='/api/v1/events'||p==='/api/v1/changes'){
   const stream={res,path:p,state:s,owner};s.streams.add(stream);s.trace.push({kind:'sse-open',path:p,owner});
   res.writeHead(200,{'content-type':'text/event-stream','cache-control':'no-cache'});res.write(': open\n\n');
   if(p.endsWith('/events')){if(!s.holdInitial)send(stream);}else res.write('event: changed\ndata: {"assistants":1}\n\n');
   const timer=setInterval(()=>res.write('event: heartbeat\ndata: {}\n\n'),15000);
   req.on('close',()=>{clearInterval(timer);s.streams.delete(stream);});return;
  }
  if((p==='/api/v1/messages'||p==='/api/v1/turn/steer')&&body){
   s.trace.push({kind:'post',path:p,owner,body});if(s.dropBeforeAccept){s.dropBeforeAccept=false;s.trace.push({kind:'response-lost-before-accept',request_id:body.request_id});return;}if(s.rejectSend)return json(res,{message:'Synthetic rejection'},409);
   const duplicate=!!db.prepare('SELECT 1 FROM accepted WHERE scenario=? AND owner=? AND request=?').get(name,owner,body.request_id);
   if(s.acceptSend!==false)db.prepare('INSERT OR IGNORE INTO accepted VALUES (?,?,?,?)').run(name,owner,body.request_id,JSON.stringify(body));
   if(s.dropAfterAccept){s.dropAfterAccept=false;s.trace.push({kind:'response-lost-after-accept',request_id:body.request_id});return;}
   if(s.holdSend){(s.heldSends||=[]).push({res,message_id:body.request_id,duplicate,owner});return;}
   s.trace.push({kind:'post-response',owner,request_id:body.request_id,duplicate});
   return json(res,{message_id:body.request_id,duplicate});
  }
  if(p==='/api/v1/messages/receipt'){
   const query=Object.fromEntries(url.searchParams);s.trace.push({kind:'receipt',owner,query});
   if(s.receiptForbidden)return json(res,{message:'Synthetic permission revoked'},403);
   const accepted=s.receipt||durableReceipts&&!!db.prepare('SELECT 1 FROM accepted WHERE scenario=? AND owner=? AND request=?').get(name,owner,query.request_id);
   return json(res,{...query,runtime_id:'picker-control',owner_id:owner,state:accepted?'accepted':'unconfirmed',...(accepted?{message_id:query.request_id}:{})});
  }
  if(p==='/api/v1/turn/continue'&&body){
   s.trace.push({kind:'continue',path:p,owner,space:url.searchParams.get('space_id'),body});
   if(s.holdContinue)await new Promise(resolve=>s.releaseContinue=resolve);
   if(s.continueError)return json(res,{code:s.continueError.code,message:s.continueError.message},s.continueError.status);
   return json(res,{message_id:body.user_message_id,duplicate:false});
  }
  if(p==='/api/v1/turn/cancel'&&body){s.trace.push({kind:'cancel',owner,body});return json(res,{accepted:true});}
  if(p==='/api/v1/attachments'&&body){
   s.trace.push({kind:'upload',owner,space:url.searchParams.get('space_id'),action:body.action,id:body.id});
   if(s.uploadFailure)return json(res,{message:'Synthetic upload failure'},503);
   if(!s.permission)return json(res,{message:'Synthetic permission denied'},403);
   let item=s.uploads.get(body.id);
   if(body.action==='begin'&&!item){item={attachment:{id:body.id,name:body.name,mime_type:body.mime_type,size:body.size},offset:0};s.uploads.set(body.id,item);}
   if(body.action==='chunk')item.offset+=Buffer.from(body.data,'base64').length;
   if(body.action==='finish')item.ready=true;
   if(body.action==='remove')s.uploads.delete(body.id);
   if(s.holdUploads)await new Promise(resolve=>s.held.push(resolve));
   return json(res,item||{});
  }
  if(p.startsWith('/api/'))return json(res,p==='/api/v1/runtime'?{runtime_id:'picker-control',boot_id:'picker-boot'}:p==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Synthetic Space',kind:'local',connected:true,control_id:'picker-control'}]}:p==='/api/v1/assistants'?['alpha','beta'].filter(id=>!s.hideAssistant||id!=='alpha').map(id=>({id,name:'Synthetic '+id,revision:s.revision,avatar:s.avatar,capabilities:s.permission?caps:{...caps,operations:['chat.read','chat.subscribe']}})):p==='/api/v1/assistants/bootstrap'?{assistant:null}:p==='/api/v1/snapshot'?frame(s,owner):p==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:{items:[],total:0,cursor:0,has_more:false});
  const response=await fetch(sourceOrigin+req.url);res.writeHead(response.status,Object.fromEntries(response.headers));Readable.fromWeb(response.body).pipe(res);
 }catch(error){json(res,{message:error.stack},500);}});
 const fixtureWS=await installFixtureWebSocket(server);
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 return {origin:'http://127.0.0.1:'+server.address().port,states,close:async()=>{for(const s of states.values()){s.releaseContinue?.();for(const release of s.held)release();for(const entry of (s.heldSends||[]).splice(0))entry.res.end();for(const stream of s.streams)stream.res.end();}await fixtureWS.close();
server.closeAllConnections();await new Promise(resolve=>server.close(resolve));db.close();}};
}
