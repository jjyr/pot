import {createServer} from 'node:http';
import {readFile} from 'node:fs/promises';
import {Readable} from 'node:stream';
export async function approvalGenerationFixture(sourceOrigin){
const trace=[],held=new Map();let revision=1,mode='fresh',evidence;
const image=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==','base64');
const log=(kind,data={})=>trace.push({at:Date.now(),kind,...data});
const server=createServer(async(req,res)=>{try{const url=new URL(req.url,'http://fixture'),path=url.pathname;let body={};if(req.method==='POST'){let raw='';for await(const c of req)raw+=c;body=JSON.parse(raw||'{}');}const json=(value,status=200)=>{res.writeHead(status,{'content-type':'application/json','cache-control':'no-store'});res.end(JSON.stringify(value));};
if(path==='/fixture/control'){if(body.mode)mode=body.mode;if(body.revision)revision=body.revision;if(body.release){log('release',{key:body.release});held.get(body.release)?.();held.delete(body.release);}return json({mode,revision,trace});}
if(path==='/fixture/evidence'){evidence=body;return json({saved:true});}
if(path==='/fixture/state')return json({mode,revision,held:[...held.keys()],trace});
if(path==='/api/v1/permissions'){log('permissions',{method:req.method,mode,revision,body});if(req.method==='POST')return json({});if(mode==='stale')return json({message:'Synthetic permission read failure'},503);return json({pending:[{id:'approval-D',revision,request:{owner:'synthetic-owner',call_id:'synthetic-call',turn:'synthetic-turn',tool:'exec_command',args:{cmd:'Synthetic only approval revision '+revision},machine_id:'synthetic-machine'}}]});}
if(path==='/api/v1/runtime')return json({runtime_id:'synthetic-control',boot_id:'synthetic-boot',host:{hostname:'Synthetic fixture'}});
if(path==='/held.png'){const key=url.searchParams.get('key');log('image-held',{key});await new Promise(resolve=>held.set(key,resolve));log('image-response',{key});res.writeHead(200,{'content-type':'image/png','cache-control':'no-store'});return res.end(image);}
if(path==='/d') {res.writeHead(200,{'content-type':'text/html'});return res.end('<!doctype html><html lang="en"><head><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"></head><body><script type="module" src="/d.mjs"></script></body></html>');}
if(path==='/d.mjs'){res.writeHead(200,{'content-type':'text/javascript'});return res.end(await readFile(new URL('fixtures/prepared-approval-generation.mjs',import.meta.url)));}
if(path.startsWith('/api/')){log('other-http',{path,method:req.method});return json({items:[]});}
const response=await fetch(sourceOrigin+req.url);res.writeHead(response.status,Object.fromEntries(response.headers));Readable.fromWeb(response.body).pipe(res);
}catch(e){res.writeHead(500);res.end(e.stack);}});
await new Promise(r=>server.listen(0,'127.0.0.1',r));
return {origin:'http://127.0.0.1:'+server.address().port,trace,get evidence(){return evidence;},async close(){for(const release of held.values())release();server.closeAllConnections();await new Promise(r=>server.close(r));}};
}
export async function preparedApprovalGeneration(browser,sourceOrigin){
 const fixture=await approvalGenerationFixture(sourceOrigin),context=await browser.newContext();
 try{
  const page=await context.newPage();await page.goto(fixture.origin+'/d');await page.locator('#run-D').click();
  await page.waitForFunction(()=>!!document.querySelector('#result-D')?.textContent,{},{timeout:12000});
  const result=fixture.evidence;if(!result||result.failure)throw Error(result?.failure||'No approval generation evidence');
  if(result.posts.length||fixture.trace.some(item=>item.kind==='permissions'&&item.method==='POST'))throw Error('Unexpected decision POST');
  return {passed:true,checkpoints:result.checkpoints};
 }finally{await context.close();await fixture.close();}
}
