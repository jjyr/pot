import {createServer} from 'node:http';
import {Readable} from 'node:stream';

// Production shell, HTTP adapter, SSE and IndexedDB; synthetic data only.
export async function mobileChatBackHTTPFixture(sourceOrigin,{uiSource}={}){
  const trace=[],streams=new Set();let revision=1;
  const capabilities={version:1,operations:['chat.read','chat.send','chat.subscribe','assistant.configure']};
  const assistants=[{id:'main',name:'Pot'},{id:'beta',name:'Beta'},{id:'gamma',name:'Gamma'}].map(item=>({...item,capabilities}));
  const messages=owner=>Array.from({length:60},(_,i)=>({message:{id:owner+'-'+(i+1),role:i%2?'assistant':'user',text:owner+' message '+(i+1)+'\n\n'+('Synthetic reading position. '.repeat(6))+(i===59?'\n\n```js\n'+('const horizontalContent = "synthetic"; '.repeat(15))+'\n```':''),status:'complete'},sequence:String(i+1),version:1}));
  const frame=owner=>({kind:'reset',base_cursor:0,view:{state:{cursor:revision,boot_id:'back-boot',conversation_id:owner+'-conversation',turn:null,agent:{phase:'ready'}},messages:messages(owner),replies:[],history:{before:'1',after:'60',has_more:false,has_newer:false}}});
  const json=(res,value,status=200)=>{res.writeHead(status,{'content-type':'application/json','cache-control':'no-store'});res.end(JSON.stringify(value));};
  const server=createServer(async(req,res)=>{try{
    const url=new URL(req.url,'http://fixture'),path=url.pathname,owner=url.searchParams.get('assistant_id')||'main';
    trace.push({at:Date.now(),path,method:req.method,owner});
    if(path==='/fixture/state')return json(res,{trace,assistants});
    if(path==='/fixture/refresh'){
      for(const stream of streams)if(stream.path.endsWith('/changes'))stream.res.write('event: changed\ndata: '+JSON.stringify({assistants:++revision})+'\n\n');
      return json(res,{});
    }
    if(req.method!=='GET')return json(res,{message:'Synthetic navigation fixture forbids business writes'},405);
    if(path==='/access/status')return json(res,{authenticated:true});
    if(path==='/api/v1/events'||path==='/api/v1/changes'){
      res.writeHead(200,{'content-type':'text/event-stream','cache-control':'no-cache'});res.write(': open\n\n');
      const stream={res,path,owner};streams.add(stream);
      if(path.endsWith('/events'))res.write('event: conversation\ndata: '+JSON.stringify(frame(owner))+'\n\n');
      req.on('close',()=>streams.delete(stream));return;
    }
    if(path.startsWith('/api/'))return json(res,path==='/api/v1/runtime'?{runtime_id:'back-control',boot_id:'back-boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Synthetic Space',kind:'local',connected:true,control_id:'back-control'}]}:path==='/api/v1/assistants'?assistants:path==='/api/v1/snapshot'?frame(owner):path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:{items:[],pending:[],policy:{},total:0});
    if(path==='/components/ui.js'&&uiSource){res.writeHead(200,{'content-type':'text/javascript','cache-control':'no-store'});res.end(uiSource);return;}
    const response=await fetch(sourceOrigin+req.url);res.writeHead(response.status,Object.fromEntries(response.headers));Readable.fromWeb(response.body).pipe(res);
  }catch(error){json(res,{message:error.stack},500);}});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  return {origin:'http://127.0.0.1:'+server.address().port,trace,close:async()=>{for(const stream of streams)stream.res.end();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}};
}
