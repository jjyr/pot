import {createServer} from 'node:http';
import {Readable} from 'node:stream';

// Only synthetic HTTP state. The document, adapter, SSE lifecycle and IndexedDB
// are the product implementation; no model, product profile or Push is used.
export async function preparedChatFixture(sourceOrigin,{workRecords=false,resourceMatrix=false}={}){
 const trace=[],streams=new Set(),held=new Set(),images=new Set();let revision=1,holdSend=false,holdEvents=null,holdImages=false;
 const owners=['alpha','beta','short','media'];
 const image=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==','base64');
 const messages=new Map(owners.map(owner=>[owner,Array.from({length:owner==='short'?3:owner==='media'?24:800},(_,i)=>({id:`${owner}-${i+1}`,role:i%2?'assistant':'user',text:`${owner} message ${i+1}\n\n`+('Synthetic history content. '.repeat(i%5+1))+(owner==='media'&&i%2?'\n\n```js\nconst measured = true;\n```':''),status:'complete',...(owner==='media'&&i===22?{attachments:[{id:'synthetic-image',name:'synthetic.png',mime_type:'image/png',size:image.length,width:480,height:280}]}:{})}))]));
 messages.get('alpha')[699].text='Historical prepared preview.\n\n[Synthetic report](report.md)';
 if(workRecords)for(const sequence of [720,721,722,723])messages.get('alpha')[sequence-1]={id:'alpha-'+sequence,role:'tool',text:'',status:'complete',tool:{id:'historic-'+sequence,turn_id:'historic-work',...(sequence===720?{}:{parent_id:'historic-720'}),name:'fixture_tool',phase:'completed',args:{sequence},result:{content:[{type:'text',text:'Historical native member '+sequence}]}}};
 const input={id:'active-input',request_id:'active-request',role:'user',text:'Synthetic active turn: keep one stable activity slot.',status:'accepted',...(workRecords?{references:[{message_id:'alpha-721',quote:'Historical grouped member'}]}:resourceMatrix?{references:[{message_id:'alpha-700',quote:'Historical mixed previews'}]}:{})};
 messages.get('alpha').push(input);let scenario='thinking',holdPreview=true,customText='';
 const previewHeld=new Set(),previewBytes=Buffer.from('# Rendered report\n\n```mermaid\ngraph TD\n A[Prepare] --> B[Commit]\n```\n\n'+Array.from({length:12},(_,i)=>`## Section ${i+1}\n\nSynthetic **Markdown** content.\n\n\x60\x60\x60js\nconst measured = true;\n\x60\x60\x60`).join('\n\n'));
 const resource={id:'synthetic-markdown',name:'report.md',entry:'report.md',mime_type:'text/markdown',size:previewBytes.length,download:{path:'report.md'},files:[{path:'report.md',size:previewBytes.length,mime_type:'text/markdown',digest:'synthetic-digest'}]};
 const slowResource={...resource,id:'synthetic-slow-markdown',name:'slow.md',entry:'slow.md',download:{path:'slow.md'},files:resource.files.map(file=>({...file,path:'slow.md'}))};
 const matrixFiles=[['image','matrix.png','image/png',image],['pdf','matrix.pdf','application/pdf',Buffer.from('%PDF-1.4\n1 0 obj <</Type /Catalog /Pages 2 0 R>> endobj\n2 0 obj <</Type /Pages /Kids [] /Count 0>> endobj\ntrailer <</Root 1 0 R>>\n%%EOF')],['html','matrix.html','text/html',Buffer.from('<!doctype html><p>Synthetic interactive document</p>')]].map(([kind,path,mime,bytes])=>({kind,bytes,resource:{id:'matrix-'+kind,name:path,entry:path,mime_type:mime,size:bytes.length,download:{path},files:[{path,mime_type:mime,size:bytes.length,digest:'matrix-'+kind}]}}));
 if(resourceMatrix)messages.get('alpha')[699].text+='\n\n[Decoded image](matrix.png)\n\n[PDF document](matrix.pdf)\n\n[Isolated HTML](matrix.html)';
 const answer=(text,status='streaming')=>({id:'active-answer',reply_to:input.id,role:'assistant',text,status});
 const updateScenario=value=>{scenario=value;const all=messages.get('alpha'),start=all.findIndex(row=>row.id===input.id);all.splice(start+1);
 if(value==='tool-start'||value==='tool-done')all.push({id:'active-tool',role:'tool',text:'',status:'complete',tool:{id:'call-1',turn_id:'active-request',name:'synthetic_inspect',phase:value==='tool-start'?'running':'completed',args:{},result:{content:[{type:'text',text:'Synthetic empty inspection result'}]}}});
 if(value.startsWith('round-')){
  all.push(answer('A persistent progress paragraph.','complete'));
  all.push({id:'empty-round-2',reply_to:input.id,role:'assistant',text:'',status:value==='round-empty'?'streaming':'complete'});
  if(value!=='round-empty')all.push({id:'round-tool',role:'tool',text:'',status:'complete',tool:{id:'round-call',turn_id:'active-request',name:'synthetic_inspect',phase:value==='round-tool'?'running':'completed',args:{},result:{content:[{type:'text',text:'Synthetic inspection result'}]}}});
  if(value==='round-next')all.push({id:'empty-round-3',reply_to:input.id,role:'assistant',text:'',status:'streaming'});
 }
 if(value==='empty-stream')all.push(answer(''));
 if(value==='prose')all.push(answer('A rendered **progress** paragraph.'));
 if(value==='custom')all.push(answer(customText));
 if(value==='mermaid-open')all.push(answer('\x60\x60\x60mermaid\ngraph TD\n A[Prepare] --> B[Measure]\n B --> C[Commit]'));
 if(value==='mermaid-closed')all.push(answer('\x60\x60\x60mermaid\ngraph TD\n A[Prepare] --> B[Measure]\n B --> C[Commit]\n\x60\x60\x60'));
 if(value==='preview'||value==='preview-stream')all.push(answer('[Synthetic report](report.md)',value==='preview'?'complete':'streaming'));
 };
 const caps={version:1,scope:'owner',operations:['chat.read','chat.send','chat.subscribe','chat.cancel','chat.references','task.list']};
 const log=(kind,value={})=>trace.push({at:Date.now(),kind,...value});
 const json=(res,value)=>{res.writeHead(200,{'content-type':'application/json','cache-control':'no-store'});res.end(JSON.stringify(value));};
 const envelopes=(owner,rows)=>rows.map(message=>({message,sequence:String(messages.get(owner).indexOf(message)+1),version:revision}));
 const history=(owner,rows)=>({before:String(messages.get(owner).indexOf(rows[0])+1),after:String(messages.get(owner).indexOf(rows.at(-1))+1),has_more:rows[0]!==messages.get(owner)[0],has_newer:rows.at(-1)!==messages.get(owner).at(-1)});
 const frame=owner=>{const rows=owner==='alpha'?messages.get(owner).slice(720):messages.get(owner).slice(-80);return {kind:'reset',base_cursor:0,view:{state:{cursor:revision,boot_id:'interaction-boot',conversation_id:owner+'-conversation',turn:owner==='alpha'&&scenario!=='terminal-empty'?{user_message_id:input.id}:null,agent:{phase:owner==='alpha'?'running':'ready'}},messages:envelopes(owner,rows),replies:owner==='alpha'?[{input_id:input.id,state:scenario==='terminal-empty'?'completed':'running'}]:[],history:history(owner,rows)}};};
 const publish=stream=>{if(stream.path.endsWith('/events')&&holdEvents===stream.owner)return;const payload=stream.path.endsWith('/events')?frame(stream.owner):{assistants:revision};log('sse-frame',{path:stream.path,owner:stream.owner,revision,payload});stream.res.write('event: '+(stream.path.endsWith('/events')?'conversation':'changed')+'\ndata: '+JSON.stringify(payload)+'\n\n');};
 const server=createServer(async(req,res)=>{try{
  const url=new URL(req.url,'http://fixture'),path=url.pathname,owner=url.searchParams.get('assistant_id')||'alpha';
  let body;if(req.method==='POST'){let raw='';for await(const chunk of req)raw+=chunk;body=JSON.parse(raw||'{}');}
  if(path==='/fixture/state')return json(res,{trace,revision});
  if(path==='/fixture/control'){
   if('text'in body)customText=body.text;
   if(body.scenario){revision++;updateScenario(body.scenario);log('scenario',{scenario,revision});for(const stream of streams)if(stream.path.endsWith('/events'))publish(stream);}
   if('holdPreview'in body){holdPreview=body.holdPreview;if(!holdPreview){for(const release of previewHeld)release();previewHeld.clear();}}
   if('holdSend'in body){holdSend=body.holdSend;if(!holdSend){for(const release of held)release();held.clear();}}
   if('holdEvents'in body){holdEvents=body.holdEvents;if(!holdEvents)for(const stream of streams)if(stream.path.endsWith('/events'))publish(stream);}
   if('holdImages'in body){holdImages=body.holdImages;if(!holdImages){for(const release of images)release();images.clear();}}
   if(body.close)for(const stream of streams)if(stream.path.endsWith('/'+body.close))stream.res.end();
   if(body.background){messages.get('alpha').push({id:'background-'+(++revision),role:'assistant',text:'Synthetic background update',status:'complete'});for(const stream of streams)if(stream.path.endsWith('/events'))publish(stream);}
   return json(res,{});
  }
  if(path.startsWith('/api/')||path==='/access/status')log('http-start',{path,method:req.method,owner,query:url.search,body});
  if(path==='/api/v1/message-files/active-answer'||path==='/api/v1/message-files/alpha-700')return json(res,[{id:'binding-1',href:'report.md',state:'ready',resource},{id:'binding-2',href:'slow.md',state:'ready',resource:slowResource},...(resourceMatrix?matrixFiles.map(item=>({id:item.resource.id,href:item.resource.entry,state:'ready',resource:item.resource})):[])]);
  const matrix=matrixFiles.find(item=>path.startsWith('/api/v1/resources/'+item.resource.id+'/'));
  if(resourceMatrix&&matrix){
   log('matrix-resource-response',{kind:matrix.kind,operation:path.split('/').at(-1)});
   if(path.endsWith('/chunk'))return json(res,{revision:matrix.resource.files[0].digest,offset:0,size:matrix.bytes.length,data:matrix.bytes.toString('base64')});
   if(path.endsWith('/render')){const token=JSON.stringify(url.searchParams.get('token'));res.writeHead(200,{'content-type':'text/html','cache-control':'no-store'});res.end(matrix.bytes.toString()+'<script>parent.postMessage({type:"pot-resource",token:'+token+',action:"resize",value:240},"*");parent.postMessage({type:"pot-resource",token:'+token+',action:"ready"},"*")</script>');return;}
   res.writeHead(200,{'content-type':matrix.resource.mime_type,'cache-control':'no-store'});res.end(matrix.bytes);return;
  }
  if(path==='/api/v1/resources/synthetic-markdown/chunk'||path==='/api/v1/resources/synthetic-slow-markdown/chunk'){
   if(holdPreview)await new Promise(resolve=>previewHeld.add(resolve));
   log('preview-chunk-response',{owner});return json(res,{revision:'synthetic-digest',offset:0,size:previewBytes.length,data:previewBytes.toString('base64')});
  }
  if(path==='/access/status')return json(res,{authenticated:true});
  if(path==='/api/v1/attachments/synthetic-image'){
   if(holdImages)await new Promise(resolve=>images.add(resolve));
   log('image-response',{owner});return json(res,{revision:'synthetic-image',offset:0,size:image.length,data:image.toString('base64')});
  }
  if(path==='/api/v1/events'||path==='/api/v1/changes'){
   res.writeHead(200,{'content-type':'text/event-stream','cache-control':'no-cache'});res.write(': open\n\n');
   const stream={res,path,owner};streams.add(stream);publish(stream);
   const timer=setInterval(()=>{log('heartbeat',{path,owner});res.write('event: heartbeat\ndata: {}\n\n');},15000);
   req.on('close',()=>{clearInterval(timer);streams.delete(stream);log('sse-close',{path,owner});});return;
  }
  if(path==='/api/v1/messages'&&body){
   if(holdSend)await new Promise(resolve=>held.add(resolve));
   revision++;messages.get(owner).push({id:body.request_id,request_id:body.request_id,role:'user',text:body.text,references:body.references,attachments:body.attachments,status:'complete'});
   json(res,{message_id:body.request_id,duplicate:false});log('http-response',{path,owner,request:body.request_id});
   for(const stream of streams)if(stream.path.endsWith('/events')&&stream.owner===owner)publish(stream);return;
  }
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
 return {origin:'http://127.0.0.1:'+server.address().port,trace,close:async()=>{for(const release of [...held,...images,...previewHeld])release();for(const stream of streams)stream.res.end();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}};
}
