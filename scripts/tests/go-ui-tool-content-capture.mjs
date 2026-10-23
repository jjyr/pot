// Independent browser; requires a loopback repository server and cached Playwright.
import {mkdir,readFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(resolve(process.env.POT_PLAYWRIGHT_MODULE)).href:'playwright');
const [origin='http://127.0.0.1:17736',before='',directory='output/pr-images/tool-activity',integrationOrigin='']=process.argv.slice(2);
const browser=await chromium.launch({headless:true,args:['--disable-renderer-backgrounding','--disable-backgrounding-occluded-windows'],...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
await mkdir(directory,{recursive:true});
try{
  const checkPage=await browser.newPage();
  for(const [width,locale,theme]of [[1100,'en','light'],[1100,'zh-CN','dark'],[390,'en','light'],[390,'zh-CN','dark']]){
    const page=checkPage,errors=[],onError=error=>errors.push(error.message);page.on('pageerror',onError);await page.setViewportSize({width,height:1000});await page.emulateMedia({colorScheme:theme,reducedMotion:'reduce'});
    try{
      await page.goto(`${origin}/scripts/tests/fixtures/tool-content.html?locale=${locale}&theme=${theme}`);await page.bringToFront();
      await page.waitForFunction(()=>window.toolContentFixture?.ready,{},{timeout:60000,polling:100});
      const result=await page.evaluate(()=>window.toolContentFixture);if(!result.passed||errors.length)throw Error(JSON.stringify({result,errors}));
      console.log(`PASS tool content ${width}px ${locale} ${theme}`);
    }finally{page.off('pageerror',onError);}
  }
  for(const locale of ['en','zh-CN']){
    const page=checkPage;await page.setViewportSize({width:1100,height:1000});
    await page.goto(`${origin}/scripts/tests/fixtures/communication.html?locale=${locale}`);await page.waitForFunction(()=>window.communicationResults,{},{timeout:60000,polling:100});const result=await page.evaluate(()=>window.communicationResults);if(!result.passed)throw Error(JSON.stringify(result));console.log(`PASS existing communication ${locale}`);
  }
  await checkPage.close();
  if(integrationOrigin){
    const parent=await browser.newPage();await parent.goto(integrationOrigin);
    try{for(const script of ['go-ui-process-runs-browser.js','go-ui-tools-browser.js']){
      const source=await readFile(new URL(script,import.meta.url),'utf8');const run=new Function(`return (${source}\n)` )();
      console.log(`${script}: ${JSON.stringify(await run(parent))}`);
    }}finally{await parent.close();}
  }
  for(const [name,width,height,locale,theme,scenario]of [
    ['terminal-desktop-light',1100,1200,'en','light','terminal'],
    ['terminal-mobile-dark',390,1300,'zh-CN','dark','terminal'],
    ['details-desktop-light',1100,1500,'en','light','details'],
  ])for(const [version,prefix]of [...(before?[['before',before]]:[]),['after','']]){
    const page=await browser.newPage({viewport:{width,height},colorScheme:theme,deviceScaleFactor:1,reducedMotion:'reduce'}),errors=[];page.on('pageerror',error=>errors.push(error.message));
    try{
      await page.goto(`${origin}${prefix}/scripts/tests/fixtures/tool-content.html?review=1&locale=${locale}&theme=${theme}&scenario=${scenario}`);
      await page.waitForFunction(()=>window.toolContentFixture?.ready,{},{timeout:60000,polling:100});
      if(errors.length||await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error(JSON.stringify({name,version,errors,overflow:true}));
      await page.screenshot({path:resolve(directory,`${name}-${version}.png`)});console.log(`Captured ${name}-${version}`);
    }finally{await page.close();}
  }
}finally{await browser.close();}
