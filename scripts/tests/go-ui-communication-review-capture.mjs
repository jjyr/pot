// Uses Playwright's public file-output API in an independent disposable browser.
// POT_PLAYWRIGHT_MODULE may point to a workspace-local playwright package.
import {mkdir} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
const moduleName=process.env.POT_PLAYWRIGHT_MODULE;
const {chromium}=await import(moduleName?pathToFileURL(resolve(moduleName)).href:'playwright');
const [origin='http://127.0.0.1:17723',directory='output/pr-images/communication-review']=process.argv.slice(2);
await mkdir(directory,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
try{
  for(const [name,width,height,theme,scenario]of [
    ['chat-desktop-light-collapsed',1280,1000,'light','collapsed'],
    ['chat-mobile-dark-receive',390,1000,'dark','receive'],
    ['chat-desktop-light-send',1280,1000,'light','send'],
    ['chat-desktop-light-receive',1280,1000,'light','receive'],
    ['chat-desktop-light-details',1280,1400,'light','details'],
    ['chat-mobile-light-receive',390,1000,'light','receive'],
    ['chat-desktop-dark-send',1280,1000,'dark','send'],
  ])for(const version of ['before','after']){
    const page=await browser.newPage({viewport:{width,height},deviceScaleFactor:1,colorScheme:theme,reducedMotion:'reduce'});
    const errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    await page.goto(`${origin}/${version}/scripts/tests/fixtures/communication-review.html?locale=en&theme=${theme}&scenario=${scenario}`);
    if(width<720)await page.locator('.assistant-row').filter({hasText:'Pot'}).click();
    await page.evaluate(()=>window.communicationReview.preparation||window.communicationReview.prepare());
    await page.evaluate(()=>Promise.all([...document.images].map(image=>image.decode())));
    if(errors.length)throw Error(errors.join('; '));
    const state=await page.evaluate(()=>{
      const fixture=window.communicationReview;
      const card=document.querySelector(`[data-call-id="${fixture.scenario==='receive'?'review-receive':'review-send'}"]`);
      return {width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth,theme:document.documentElement.dataset.theme,ready:fixture.ready,unsafe:!!window.reviewInjected,primaryOpen:card.open,rawOpen:card.querySelector('.tool-raw').open,cardTop:Math.round(card.getBoundingClientRect().top),directions:[...document.querySelectorAll('.communication-summary .communication-flow')].map(flow=>[...flow.querySelectorAll('.communication-name')].map(name=>name.textContent)),markdown:card.querySelector('.communication-text strong')?.textContent};
    });
    if(!state.ready||state.unsafe||state.overflow||state.theme!==theme)throw Error(JSON.stringify(state));
    if(state.primaryOpen!==(scenario!=='collapsed')||state.rawOpen!==(scenario==='details')||state.cardTop<50)throw Error('Clipped or incorrect disclosure: '+JSON.stringify(state));
    if(version==='after'&&((scenario!=='collapsed'&&state.markdown!=='Review summary')||JSON.stringify(state.directions)!=='[["Pot","Code reviewer"],["Code reviewer","Pot"]]'))throw Error('Incorrect Markdown or direction: '+JSON.stringify(state));
    if(version==='after'){
      const presentation=await page.evaluate(()=>{
        const card=document.querySelector(`[data-call-id="${window.communicationReview.scenario==='receive'?'review-receive':'review-send'}"]`);
        return {graphHidden:card.querySelector('.communication-summary').hidden,channels:card.querySelectorAll('.communication-channel').length,identity:!!card.querySelector('.communication-identity'),codeWhitespace:card.querySelector('.communication-text pre')?getComputedStyle(card.querySelector('.communication-text pre')).whiteSpace:null,primaryText:card.querySelector('.communication-body').innerText};
      });
      if(presentation.graphHidden!==(scenario!=='collapsed')||presentation.channels||scenario!=='collapsed'&&!presentation.identity||presentation.primaryText.includes('node_id')||scenario!=='collapsed'&&presentation.codeWhitespace!=='pre')throw Error('Incorrect A presentation: '+JSON.stringify(presentation));
    }
    await page.screenshot({path:resolve(directory,`${name}-${version}.png`)});
    console.log(`${name}-${version}.png ${JSON.stringify(state)}`);
    await page.close();
  }
}finally{await browser.close();}
