// Focused refresh-control check against the disposable authenticated HTTP fixture.
// No package installation, product root or model connection is used.
import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';

const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(process.env.POT_PLAYWRIGHT_MODULE).href:'playwright');
const origin=process.argv[2];
if(!origin)throw Error('Pass the isolated update browser fixture origin');
const output=process.env.POT_UPDATE_CHECK_IMAGES;
if(output)await mkdir(output,{recursive:true});
const browser=await chromium.launch({executablePath:process.env.POT_CHROMIUM_EXECUTABLE,headless:true});
const status={current:'0.1.0',version:'0.2.0',phase:'available',release_url:'https://github.com/jjyr/pot/releases/tag/v0.2.0',discovery:'verified',auto_install:true,supported:true,work:{tasks:0,replies:0,sessions:0}};
const messages={en:{settings:'Settings',menu:'Open navigation',chat:'Chat',check:'Check for updates',checking:'Checking for updates…',found:'A new version is available. Open the update action to view the Release.',current:'You’re up to date.',failed:'Could not check for updates. Previously discovered releases are kept.'},'zh-CN':{settings:'设置',menu:'打开导航',chat:'聊天',check:'检查新版本',checking:'正在检查新版本…',found:'发现新版本，请打开更新入口查看 Release。',current:'已是最新版本。',failed:'检查新版本失败，已发现的版本信息仍会保留。'}};
async function setStatus(value){const reply=await fetch(origin+'/fixture/update',{method:'POST',body:JSON.stringify(value)});assert(reply.ok);}
async function navigate(page,label,small,words){if(small)await page.getByRole('button',{name:words.menu,exact:true}).click();await page.getByRole('button',{name:label,exact:true}).first().click();}
async function sample(button){return button.evaluate(element=>{const svg=element.querySelector('svg'),text=element.parentElement.querySelector('span'),style=getComputedStyle(element),box=element.getBoundingClientRect(),icon=svg.getBoundingClientRect(),label=text.getBoundingClientRect();return {border:style.borderTopWidth,background:style.backgroundColor,width:box.width,height:box.height,center:[box.x+box.width/2,box.y+box.height/2],iconCenter:[icon.x+icon.width/2,icon.y+icon.height/2],label:[label.x,label.y,label.width,label.height],animation:getComputedStyle(svg).animationName,transform:getComputedStyle(svg).transform,buttonAnimations:element.getAnimations().filter(animation=>animation instanceof CSSAnimation).length,svgAnimations:svg.getAnimations().length};});}
try{
  for(const [device,width,height,locale]of [['desktop',1280,800,'en'],['phone',390,844,'zh-CN']]){
    const words=messages[locale],small=width<700;
    for(const theme of ['light','dark']){
      await setStatus(status);
      // Baseline is the actual previous PR head, extracted into output/update-before.
      if(output){
        const context=await browser.newContext({viewport:{width,height},reducedMotion:'reduce'}),page=await context.newPage();
        await page.goto(`${origin}/scripts/tests/fixtures/updates.html?http=1&before=1&locale=${locale}&theme=${theme}`);
        await page.waitForFunction(()=>!!window.fixture);assert.equal(await page.locator('html').getAttribute('data-theme'),theme);await navigate(page,words.settings,small,words);
        const check=page.getByRole('button',{name:words.check,exact:true});await check.scrollIntoViewIfNeeded();await page.evaluate(()=>document.fonts.ready);
        assert.notEqual((await sample(check)).border,'0px','Baseline must show the original border');
        await page.screenshot({path:`${output}/${device}-settings${theme==='dark'?'-dark':''}-before.png`});await context.close();
      }
      const context=await browser.newContext({viewport:{width,height},reducedMotion:'no-preference'}),page=await context.newPage(),errors=[];
      page.on('pageerror',error=>errors.push(error.message));
      await page.goto(`${origin}/scripts/tests/fixtures/updates.html?http=1&locale=${locale}&theme=${theme}`);
      await page.waitForFunction(()=>!!window.fixture);assert.equal(await page.locator('html').getAttribute('data-theme'),theme);await navigate(page,words.settings,small,words);
      const check=page.getByRole('button',{name:words.check,exact:true});await check.scrollIntoViewIfNeeded();await page.evaluate(()=>document.fonts.ready);
      const idle=await sample(check);assert.equal(idle.border,'0px');assert.equal(idle.background,'rgba(0, 0, 0, 0)');assert(idle.width>=44&&idle.height>=44);assert.equal(idle.animation,'none');
      assert.equal(await check.getAttribute('title'),words.check);assert.equal(await check.getAttribute('type'),'button');assert.equal(await check.locator('svg').getAttribute('viewBox'),'0 0 24 24');
      if(output)await page.screenshot({path:`${output}/${device}-settings${theme==='dark'?'-dark':''}-after.png`});
      let pending=null;
      await page.route('**/api/v1/updates',async route=>{
        if(route.request().method()!=='POST')return route.continue();
        const command=route.request().postDataJSON();assert.equal(command.action,'check');assert.equal(command.interrupt,false);
        const response=await route.fetch(); // The real authenticated route receives the check.
        const outcome=await new Promise(resolve=>{pending={finish:resolve};});pending=null;
        if(outcome==='abort')return route.abort('aborted');
        if(outcome==='fail')return route.fulfill({status:503,body:'fixture network failure'});
        return route.fulfill({response});
      });
      const calls=async()=>await(await fetch(origin+'/fixture/calls')).json();
      async function begin(){const count=(await calls()).length;await check.click();await page.waitForFunction(()=>document.querySelector('.update-check')?.getAttribute('aria-busy')==='true');const deadline=Date.now()+10000;while(!pending){assert(Date.now()<deadline,"Check did not reach the authenticated route");await new Promise(resolve=>setTimeout(resolve,5));}assert.equal((await calls()).length,count+1);return count;}
      async function finish(outcome,feedback){pending.finish(outcome);await page.waitForFunction(()=>document.querySelector('.update-check')?.getAttribute('aria-busy')==='false');assert.equal(await check.isDisabled(),false);await page.getByText(feedback,{exact:true}).waitFor();const restored=await sample(check);assert.equal(restored.animation,'none');assert.equal(restored.transform,'none');}
      const count=await begin(),running=await sample(check);
      assert.equal(await check.isDisabled(),true);assert.equal(running.animation,'update-check-spin');assert.equal(running.buttonAnimations,0);assert.equal(running.svgAnimations,1);assert.deepEqual(running.label,idle.label);
      assert(Math.abs(running.center[0]-running.iconCenter[0])<0.5&&Math.abs(running.center[1]-running.iconCenter[1])<0.5,'SVG must rotate around the hit target center');
      await page.waitForFunction(previous=>getComputedStyle(document.querySelector('.update-check svg')).transform!==previous,running.transform);
      await check.evaluate(element=>element.click());assert.equal((await calls()).length,count+1,'Disabled control must not check twice');
      if(output&&device==='desktop'&&theme==='light')await page.screenshot({path:`${output}/desktop-settings-checking-after.png`});
      await finish('success',words.found);
      await setStatus({...status,version:'',phase:'current',discovery:'current'});await begin();await finish('success',words.current);
      await begin();await finish('fail',words.failed);
      await begin();await finish('abort',words.failed);
      await page.keyboard.press('Tab');await check.focus();assert(await check.evaluate(element=>element.matches(':focus-visible')));assert.notEqual(await check.evaluate(element=>getComputedStyle(element).outlineStyle),'none');
      await page.emulateMedia({reducedMotion:'reduce'});await begin();assert.equal((await sample(check)).animation,'none');await finish('success',words.current);await page.emulateMedia({reducedMotion:'no-preference'});
      await begin();const retired=await check.elementHandle();await navigate(page,words.chat,small,words);
      assert.deepEqual(await retired.evaluate(element=>({connected:element.isConnected,busy:element.getAttribute('aria-busy'),disabled:element.disabled,animations:element.querySelector('svg').getAnimations().length})),{connected:false,busy:'false',disabled:false,animations:0});
      const completed=page.waitForResponse(response=>response.url().endsWith('/api/v1/updates')&&response.request().method()==='POST');pending.finish('success');await completed;
      await navigate(page,words.settings,small,words);await check.waitFor();assert.equal(await check.isDisabled(),false);assert.equal((await sample(check)).animation,'none');assert.deepEqual(errors,[]);
      await context.close();console.log(`PASS ${device}/${theme}: transparent icon, centered SVG animation, real single check, success/latest/failure/abort/leave recovery, focus, reduced motion`);
    }
  }
}finally{await browser.close();}
