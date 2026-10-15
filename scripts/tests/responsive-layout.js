// Playwright CLI run-code expression; screenshots require visual review.
async (page) => {
 const runtime = await (await page.request.get('http://127.0.0.1:17908/api/v1/runtime')).json();
 if (runtime.runtime_id !== 'fixture') throw Error('Expected isolated UI fixture on port 17908');
 const cases=[
  [390,844,true,'en',1],[393,852,true,'zh-CN',2],[430,932,true,'en',2],
  [768,1024,true,'zh-CN',1],[820,1180,true,'en',1],[1024,768,true,'zh-CN',2],
  [1280,900,false,'en',1],[1440,900,false,'zh-CN',2],
  [719,900,false,'en',1],[720,900,false,'zh-CN',1],
  [899,900,false,'en',1],[900,900,false,'zh-CN',1],
  [393,460,true,'en',1],[852,393,true,'zh-CN',1]
 ];
 const errors=[];page.on('pageerror',error=>errors.push(error.message));
 const cdp=await page.context().newCDPSession(page);const results=[];
 for(const [width,height,touch,locale,appearance] of cases){
  await cdp.send('Emulation.setTouchEmulationEnabled',{enabled:touch,maxTouchPoints:touch?5:1});
  await page.setViewportSize({width,height});
  await page.evaluate(({locale,appearance})=>{localStorage.setItem('pot-locale',locale);localStorage.setItem('pot-appearance',String(appearance));},{locale,appearance});
  for(const route of ['chat','settings']){
   await page.goto('http://127.0.0.1:17908/?page='+route+'&assistant=main');await page.waitForTimeout(700);
   const bounds=await page.locator('canvas').boundingBox();
   if(!bounds||bounds.width!==width||bounds.height!==height)throw Error('Canvas viewport mismatch '+width+'x'+height+':'+JSON.stringify(bounds));
   const actualTouch=await page.evaluate(()=>navigator.maxTouchPoints>0||matchMedia('(any-pointer: coarse)').matches);
   if(actualTouch!==touch)throw Error('Touch emulation mismatch '+width+': expected '+touch+', actual '+actualTouch);
   const theme=await page.evaluate(()=>document.documentElement.dataset.theme);
   if(theme!==(appearance===2?'dark':'light'))throw Error('Theme mismatch');
   if(await page.locator('#pot-error-toast:not([hidden])').count())throw Error('Visible application error');
   await page.screenshot({path:'output/playwright/devices/matrix-'+width+'x'+height+'-'+locale+'-'+route+'.png'});
   results.push({width,height,route,touch,locale,theme});
  }
 }
 await cdp.detach();
 if(errors.length)throw Error(JSON.stringify(errors));
 return {screenshots:results.length,viewports:cases.length,errors,results};
}
