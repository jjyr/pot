import {readFile} from 'node:fs/promises';
import {createServer} from 'node:http';
import {resolve,extname,sep} from 'node:path';
import {pathToFileURL} from 'node:url';
import {faults,injectFault} from './ui-regression-plan.mjs';

export async function sourceServer({fault}={}) {
  const root=resolve('.');
  const server=createServer(async(req,res)=>{
    try {
      let path=decodeURIComponent(new URL(req.url,'http://localhost').pathname);
      if(path.split('/').includes('..'))throw Error('Outside fixture source paths');
      if(path==='/')path='/web/src/index.html';
      if(!path.startsWith('/web/')&&!path.startsWith('/scripts/')&&!path.startsWith('/pkg/'))path='/web/src'+path;
      const file=resolve(root,'.'+path);
      if(!file.startsWith(root+sep))throw Error('Outside fixture source root');
      let data=await readFile(file);
      if(faults[fault]?.path===path)data=injectFault(path,data.toString(),fault);
      res.writeHead(200,{'Cache-Control':'no-store','Content-Type':({'.html':'text/html','.js':'text/javascript','.mjs':'text/javascript','.css':'text/css','.json':'application/json','.png':'image/png','.ttf':'font/ttf'})[extname(file)]||'application/octet-stream'});
      res.end(data);
    }catch(error){res.writeHead(404);res.end(error.message);}
  });
  await new Promise(done=>server.listen(0,'127.0.0.1',done));
  return {origin:'http://127.0.0.1:'+server.address().port,close:()=>new Promise((done,reject)=>server.close(error=>error?reject(error):done()))};
}
export async function launchBrowser() {
  if(!process.env.POT_PLAYWRIGHT_MODULE)throw Error('Set POT_PLAYWRIGHT_MODULE to an existing Playwright module; no dependencies are installed');
  const {chromium}=await import(pathToFileURL(process.env.POT_PLAYWRIGHT_MODULE).href);
  return chromium.launch({executablePath:process.env.POT_CHROMIUM_EXECUTABLE,headless:true,args:['--no-sandbox']});
}
export async function isolatedPage(browser,origin,run) {
  const context=await browser.newContext({locale:'en-US',serviceWorkers:'block',viewport:{width:1280,height:800}});
  const page=await context.newPage(),network=new Map(),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  page.on('request',request=>network.set(request.url(),request.method()));
  page.on('requestfinished',request=>network.delete(request.url()));
  page.on('requestfailed',request=>network.delete(request.url()));
  await context.route('**/*',route=>new URL(route.request().url()).origin===origin?route.continue():route.abort());
  try {const result=await run(page);if(errors.length)throw Error('Browser exceptions: '+errors.join('; '));return result;}
  catch(error){
    const state=await page.evaluate(()=>({result:document.querySelector('#results')?.textContent?.slice(-2200),scroll:window.continuity?.sample(),last:window.continuity?.logs?.slice(-2),reads:window.continuity?.reads,history:window.historyEdges&&{calls:historyEdges.calls.map(c=>({before:c.before,aborted:c.signal.aborted})),inflight:historyEdges.inflight},directory:window.directoryFixture?.measure()})).catch(()=>({}));
    throw Error(error.message+'\n'+JSON.stringify({state,errors,pending:[...network.keys()].map(url=>new URL(url).pathname)}),{cause:error});
  }finally{await context.close();}
}
export async function contract(script,page,options) {
  // Repository-owned CLI expressions, never website content.
  const source=(await readFile(new URL(script,import.meta.url),'utf8')).replaceAll('STATE',JSON.stringify({origin:options.origin}));
  return new Function('return ('+source+'\n)')()(page,options);
}
export async function fixture(page,origin,path,done) {
  await page.goto(origin+'/scripts/tests/fixtures/'+path);
  if(path.startsWith('chat-scroll.html'))await page.getByRole('button',{name:'Run scrolling checks'}).click();
  if(path==='cached-loading.html')await page.getByRole('button',{name:'Run regression checks'}).click();
  await page.waitForFunction(marker=>{const result=document.querySelector('#results')?.textContent||'';return result.includes(marker)||/FAIL|STOP|ERROR /.test(result);},done);
  const result=await page.locator('#results').textContent();
  if(!result.includes(done)||/FAIL|STOP|ERROR /.test(result))throw Error(path+': '+result);
  return {assertions:result.split('\n').filter(line=>line.startsWith('PASS')).length};
}
