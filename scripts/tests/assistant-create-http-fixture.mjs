import {createServer} from 'node:http';
import {Readable} from 'node:stream';

// A private loopback API with synthetic identities and idempotent creation.
// The browser uses the production HTTP adapter, SSE and IndexedDB, never a model.
export async function assistantCreateHTTPFixture(sourceOrigin){
  const trace=[],streams=new Set(),waiters=new Map(),receipts=new Map();
  const holds=new Set();let failCreate=false,emptyProviders=false,revision=1;
  const capabilities={version:1,operations:['chat.read','chat.send','chat.subscribe','assistant.configure']};
  const assistants=[{id:'original',name:'Original fixture',capabilities}];
  const space={id:'local',name:'Creation fixture',kind:'local',connected:true,control_id:'creation-control'};
  const links=[{id:'fixture-peer',peer:{node_id:'fixture-peer',kind:'codex',name:'Fixture peer'},status:'connected',directory:{targets:[{address:{node_id:'fixture-peer',kind:'node',agent_id:'fixture-peer'},capabilities:{version:1,operations:['assistant.management']}}]}}];
  const json=(res,value,status=200)=>{res.writeHead(status,{'content-type':'application/json','cache-control':'no-store'});res.end(JSON.stringify(value));};
  const log=(kind,data={})=>trace.push({at:Date.now(),kind,...data});
  const frame=owner=>({kind:'reset',base_cursor:0,view:{state:{cursor:revision,boot_id:'creation-boot',conversation_id:owner+'-conversation',turn:null,agent:{phase:'ready'}},messages:[{message:{id:owner+'-welcome',role:'assistant',text:'Welcome to '+owner,status:'complete'},sequence:'1',version:1}],replies:[],history:{before:'1',after:'1',has_more:false,has_newer:false}}});
  const wait=async key=>{if(holds.has(key))await new Promise(resolve=>{if(!waiters.has(key))waiters.set(key,new Set());waiters.get(key).add(resolve);});};
  const release=key=>{holds.delete(key);for(const resolve of waiters.get(key)||[])resolve();waiters.delete(key);};
  const server=createServer(async(req,res)=>{try{
    const url=new URL(req.url,'http://fixture'),path=url.pathname,owner=url.searchParams.get('assistant_id')||'original';
    let body;if(req.method==='POST'){let raw='';for await(const chunk of req)raw+=chunk;body=JSON.parse(raw||'{}');}
    if(path==='/fixture/control'){for(const key of body.hold||[])holds.add(key);for(const key of body.release||[])release(key);if('failCreate'in body)failCreate=body.failCreate;if('emptyProviders'in body)emptyProviders=body.emptyProviders;
      if(body.changed){links[0].directory.targets.push({address:{node_id:'fixture-peer',kind:'assistant',agent_id:'external-'+revision},name:'Synthetic external',capabilities:{version:1,operations:['chat.read']}});}if(body.changed)for(const {res,path}of streams)if(path.endsWith('/changes'))res.write('event: changed\ndata: '+JSON.stringify({'agent-links':++revision,assistants:revision})+'\n\n');
      return json(res,{});}
    if(path==='/fixture/state')return json(res,{trace,assistants,holds:[...holds],created:receipts.size});
    if(path.startsWith('/api/')||path==='/access/status')log('request',{path,method:req.method,owner,body});
    if(path==='/access/status')return json(res,{authenticated:true});
    if(path==='/api/v1/events'||path==='/api/v1/changes'){
      res.writeHead(200,{'content-type':'text/event-stream','cache-control':'no-cache'});res.write(': open\n\n');
      const stream={res,path,owner};streams.add(stream);if(path.endsWith('/events'))res.write('event: conversation\ndata: '+JSON.stringify(frame(owner))+'\n\n');
      const timer=setInterval(()=>res.write(': heartbeat\n\n'),15000);req.on('close',()=>{clearInterval(timer);streams.delete(stream);});return;
    }
    if(path==='/api/v1/assistants'&&req.method==='POST'){
      await wait('create');if(failCreate){failCreate=false;return json(res,{code:'assistant_model_unavailable',message:'Synthetic unavailable model'},409);}
      let item=receipts.get(body.request_id);if(!item){item={id:'created-'+(receipts.size+1),name:body.name||'Created fixture',capabilities};receipts.set(body.request_id,item);assistants.push(item);}
      log('created',{id:item.id,request_id:body.request_id});return json(res,item);
    }
    if(path==='/api/v1/agent-links/assistants'&&body?.action==='catalog'){
      await wait('catalog');return json(res,{schema_version:1,runtime:{kind:'codex',name:'Fixture peer'},actions:['create'],fields:[{name:'name',required:true}],models:[],workspaces:[],assistants:[]});
    }
    if(path==='/api/v1/providers'){await wait('providers');return json(res,{default_provider_id:'fixture',profiles:emptyProviders?[]:[{id:'fixture',revision:1,name:'Fixture API',model:'fixture-model'}],connections:[],directory:[],provider_types:[{id:'fixture-type',name:'Fixture API',available:true}]});}
    if(path==='/api/v1/agent-links'){await wait('links');log('response',{path});return json(res,{items:links});}
    if(path==='/api/v1/agent-links/mcp')return body?json(res,{code:'synthetic_authorization_unavailable'},503):json(res,[]);
    if(path.startsWith('/api/')){
      const value=path==='/api/v1/runtime'?{runtime_id:space.control_id,boot_id:'creation-boot'}:path==='/api/v1/spaces'?{active_space_id:'local',items:[space]}:path==='/api/v1/assistants'?assistants:path==='/api/v1/snapshot'?frame(owner):path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:{items:[],pending:[],policy:{},total:0};
      log('response',{path});return json(res,value);
    }
    const response=await fetch(sourceOrigin+req.url);res.writeHead(response.status,Object.fromEntries(response.headers));Readable.fromWeb(response.body).pipe(res);
  }catch(error){json(res,{message:error.stack},500);}});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  return {origin:'http://127.0.0.1:'+server.address().port,trace,close:async()=>{for(const key of holds)release(key);for(const stream of streams)stream.res.end();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}};
}
