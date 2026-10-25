import assert from 'node:assert/strict';
import {taskHistoryBrowser,taskHistoryEdges} from './task-history-browser.mjs';
import {mkdir,readFile} from 'node:fs/promises';
import {createServer} from 'node:http';
import {resolve,extname} from 'node:path';
import {pathToFileURL} from 'node:url';
const module=process.env.POT_PLAYWRIGHT_MODULE;
if(!module)throw Error('Set POT_PLAYWRIGHT_MODULE to the existing Playwright module; this runner never installs dependencies');
const {chromium}=await import(pathToFileURL(module).href);
let origin=process.argv[2],server;
if(!origin||origin==='auto'){const root=resolve('.');server=createServer(async(req,res)=>{try{const path=resolve(root,'.'+decodeURIComponent(new URL(req.url,'http://localhost').pathname));if(!path.startsWith(root+'/'))throw Error('outside source root');const data=await readFile(path);res.writeHead(200,{'Cache-Control':'no-store','Content-Type':({'.json':'application/json','.html':'text/html','.js':'text/javascript','.mjs':'text/javascript','.css':'text/css','.png':'image/png','.ttf':'font/ttf'})[extname(path)]||'application/octet-stream'});res.end(data);}catch{res.writeHead(404);res.end();}});await new Promise(done=>server.listen(0,'127.0.0.1',done));origin='http://127.0.0.1:'+server.address().port;}
if(!/^http:\/\/(127\.0\.0\.1|localhost):\d+$/.test(origin||''))throw Error('A loopback static source origin is required');
const browser=await chromium.launch({executablePath:process.env.POT_CHROMIUM_EXECUTABLE,headless:true,args:['--no-sandbox']});
const directory=process.env.POT_DB_SCREENSHOTS;
try{
  for(const [path,done]of [['domain-repositories.html','DONE directory storage UI contract'],['chat-cache-read-volume.html','PASS bounded chat reads'],['chat-cache.html','DONE cache contract'],['projection-store.html','DONE projection store contract'],['chat-cache-ui.html','DONE'],['cached-loading.html','ALL CHECKS PASSED']]){
    if(process.env.POT_DB_FIXTURE&&path!==process.env.POT_DB_FIXTURE)continue;
    const context=await browser.newContext({serviceWorkers:'block',viewport:{width:1280,height:800}}),page=await context.newPage();
    try{
      await context.route('**/*',route=>new URL(route.request().url()).origin===origin?route.continue():route.abort());
      const errors=[];
      page.on('console',message=>console.log(path+': '+message.text()));
      page.on('pageerror',error=>{errors.push(error.message);console.error(path+': '+error.stack);});
      await page.goto(origin+'/scripts/tests/fixtures/'+path);
      if(path==='cached-loading.html')await page.getByRole('button',{name:'Run regression checks'}).click();
      await page.waitForFunction(text=>document.querySelector('#results')?.textContent.includes(text)||document.querySelector('#results')?.textContent.includes('FAIL'),done,{timeout:120000}).catch(async error=>{throw Error(path+': '+await page.locator('#results').textContent()+'\n'+error.message);});
      const result=await page.locator('#results').textContent();assert.ok(!result.includes('FAIL'),result);assert.ok(result.includes(done),result);assert.ok(!result.includes('ERROR '),result);assert.deepEqual(errors,[],path+' page errors');
      if(path==='chat-cache-read-volume.html')console.log(result);
      console.log(path+': '+result.split('\n').filter(line=>line.startsWith('PASS')).length+' assertions passed');
    }finally{await context.close();}
  }
  if(!process.env.POT_DB_FIXTURE||process.env.POT_DB_FIXTURE==='task-history'){
    if(directory)await mkdir(directory,{recursive:true});
    for(const before of [true,false]){
      const context=await browser.newContext({serviceWorkers:'block',viewport:{width:1280,height:800}}),page=await context.newPage();
      try{await taskHistoryBrowser(page,{origin,before,screenshot:directory?directory+'/task-history-1280-light-'+(before?'before':'after')+'.png':undefined});if(!before)await taskHistoryEdges(page,{origin});}
      catch(error){console.error(await page.evaluate(()=>({errors:document.querySelector('#results')?.textContent,earlier:window.taskFixture?.earlier,counts:window.taskFixture?.counts,history:document.querySelector('.task-history')?.scrollTop,text:document.querySelector('.task-transcript')?.textContent?.slice(0,400)})));throw error;}
      finally{await context.close();}
    }
  }
}finally{await browser.close();if(server)await new Promise(done=>server.close(done));}
