// Automated regression of the production settings UI against isolated HTTP/SSE.
// No product service, credentials, model, persistent user profile or Push calls.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdir,mkdtemp,rm,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {translate} from '../../web/src/locales/messages.mjs';
const output=resolve(process.env.POT_SETTINGS_OUTPUT||'.pot-artifacts/settings-browser');await mkdir(output,{recursive:true});
const images=resolve(process.env.POT_SETTINGS_IMAGES||'output/pr-images/fix-settings-independent-status');await mkdir(images,{recursive:true});
const beforeRoot=process.env.POT_SETTINGS_BEFORE;assert(beforeRoot,'POT_SETTINGS_BEFORE must name the exact baseline source tree');
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE||'playwright');
const children=[],origins=[],report={passed:false,checks:[],errors:[],observations:[]};let context,profile;
const check=(condition,message)=>{assert(condition,message);report.checks.push(message);};
async function server(source){const child=spawn(process.execPath,[resolve('scripts/tests/pot-ui-settings-fixture.mjs')],{env:{...process.env,...(source?{POT_SETTINGS_SOURCE:source}:{})},stdio:['ignore','pipe','pipe']});children.push(child);child.stderr.on('data',data=>report.errors.push(String(data)));const origin=await new Promise((done,reject)=>{let text='';child.stdout.on('data',data=>{text+=data;const match=text.match(/http:\/\/127\.0\.0\.1:\d+/);if(match)done(match[0]);});child.on('exit',code=>reject(Error('fixture exited '+code)));});origins.push(origin);return origin;}
const config=async(origin,body)=>{const response=await fetch(origin+'/__fixture',body?{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}:{});return response.json();};
async function until(read,message){for(let i=0;i<300;i++){if(await read())return;await new Promise(done=>setTimeout(done,20));}throw Error('Timed out: '+message);}
async function launch(viewport){profile=await mkdtemp('/dev/shm/ps87052-');context=await chromium.launchPersistentContext(profile,{headless:true,executablePath:process.env.POT_CHROMIUM_EXECUTABLE,viewport,args:['--no-sandbox']});const page=context.pages()[0];page.setDefaultTimeout(10000);page.on('pageerror',error=>report.errors.push(error.message));page.on('dialog',dialog=>dialog.type()==='beforeunload'?dialog.accept():dialog.dismiss());return page;}
async function close(){if(context){await context.close();context=null;}if(profile){await rm(profile,{recursive:true,force:true});profile=null;}}
async function record(page,origin,name){const value=await page.evaluate(()=>({text:document.body.innerText,route:document.querySelector('[data-route]')?.dataset.route,inert:document.querySelector('.page-host')?.inert,fields:[...document.querySelectorAll('.editor-form [aria-label]')].filter(node=>['INPUT','TEXTAREA','SELECT'].includes(node.tagName)).map(node=>({label:node.getAttribute('aria-label'),value:node.value,disabled:node.disabled})),sameName:!window.settingsNameNode||window.settingsNameNode===document.querySelector('input[aria-label="Name"]')}));report.observations.push({name,value,backend:await config(origin)});}
try{
 const before=await server(beforeRoot),after=await server();
 for(const viewport of (process.env.POT_SETTINGS_PROFILE_ONLY?[]:[{width:1280,height:800},{width:390,height:844}])){
  for(const [version,origin]of [['before',before],['after',after]]){
   const locale=viewport.width<500?'zh-CN':'en',t=key=>translate(locale,key);
   await config(origin,{hold:[],release:'all',phase:'running',snapshot:true,revoked:false,failRead:false,failSave:false,name:'Synthetic Assistant',control:'settings-control',boot:'settings-boot',revision:1,configRevision:0,model:'synthetic-original'});
   if(version==='after')await config(origin,{hold:['configuration','rules']});
   const page=await launch(viewport);await page.goto(origin+'/?assistant_id=alpha&locale='+locale);await page.getByRole('button',{name:t('assistantSettings'),exact:true}).click();
   const name=page.getByLabel(locale==='en'?'Name':'名称',{exact:true});await name.waitFor();
   if(version==='after'){check(!await name.isDisabled(),'known name is editable before model/rules reads '+viewport.width);check(await page.getByLabel(locale==='en'?'Model':'模型',{exact:true}).isDisabled(),'only loading model fields are disabled '+viewport.width);await name.fill('Name saved before config load');await page.getByRole('button',{name:locale==='en'?'Save':'保存',exact:true}).click();await page.locator('.editor-form').waitFor({state:'hidden'});check((await config(origin)).state.name==='Name saved before config load','known name saves independently of slow configuration '+viewport.width);await config(origin,{hold:[],release:'all'});await page.getByRole('button',{name:t('assistantSettings'),exact:true}).click();}
   await page.waitForFunction(()=>{const node=document.querySelector('input[list][aria-label="Model"],input[list][aria-label="模型"],select[aria-label="Model"],select[aria-label="模型"]');return node&&!node.disabled&&node.value==='synthetic-original';});
   const notice=page.getByRole('button',{name:t('notifications.later'),exact:true});if(await notice.count())await notice.click();
   await name.fill('Synthetic edited name');await page.evaluate(()=>{window.settingsNameNode=document.querySelector('input[aria-label="Name"],input[aria-label="名称"]');});
   await config(origin,{hold:['directory'],phase:'offline',disconnect:'all'});await until(async()=> (await config(origin)).held.includes('directory'),'held directory');
   await page.waitForFunction(()=>document.querySelector('.is-local-startup'));
   check(await page.locator('.page-host').evaluate(node=>node.inert)===(version==='before'),version+' '+viewport.width+' exact baseline/settings inert contract');
   await page.screenshot({path:resolve(images,'assistant-settings-'+(viewport.width<500?'phone':'desktop')+'-'+version+'.png')});
   const count=(await config(origin)).calls.filter(call=>call.method==='POST').length;
   if(version==='before'){
    const box=await page.getByRole('button',{name:locale==='en'?'Save':'保存',exact:true}).boundingBox();await page.mouse.click(box.x+box.width/2,box.y+box.height/2);
    await record(page,origin,'baseline-inert-'+viewport.width);check((await config(origin)).calls.filter(call=>call.method==='POST').length===count,'baseline trusted Save produces no request '+viewport.width);await close();continue;
   }
   check(await page.evaluate(()=>window.settingsNameNode===document.querySelector('input[aria-label="Name"],input[aria-label="名称"]')),'revalidation keeps original native field '+viewport.width);
   const save=page.getByRole('button',{name:locale==='en'?'Save':'保存',exact:true});await save.dblclick();await name.fill('Newer unsaved draft');check(await save.isDisabled(),'only current Save deduplicates while authority is pending '+viewport.width);check(!await name.isDisabled(),'fields stay editable during submission '+viewport.width);
   await config(origin,{hold:[],release:'all',phase:'running'});await page.getByText(t('settings.savedDraftRemaining'),{exact:true}).waitFor();
   check((await config(origin)).state.name==='Synthetic edited name','backend stores submitted value '+viewport.width);check(await name.inputValue()==='Newer unsaved draft','new typing remains dirty after success '+viewport.width);
   check(await page.evaluate(()=>window.settingsNameNode===document.querySelector('input[aria-label="Name"],input[aria-label="名称"]')),'successful save does not rebuild fields '+viewport.width);
   await page.getByRole('button',{name:locale==='en'?'Cancel':'取消',exact:true}).click();await page.getByRole('button',{name:locale==='en'?'Keep editing':'继续编辑',exact:true}).click();check(await name.inputValue()==='Newer unsaved draft','dirty-leave guard retains draft '+viewport.width);
   await config(origin,{failSave:true});await save.click();await page.getByText(t('settings.saveFailed'),{exact:true}).waitFor();check(await name.inputValue()==='Newer unsaved draft','network error retains input '+viewport.width);check(!await page.getByText(t('settings.savedDraftRemaining'),{exact:true}).count(),'network failure never claims success '+viewport.width);
   await config(origin,{failSave:false,revoked:true,change:true});await save.click();await page.getByText(t('agentLinks.capabilityUnavailable'),{exact:true}).waitFor();check((await config(origin)).state.name==='Synthetic edited name','revocation never mutates backend '+viewport.width);await record(page,origin,'after-revoked-'+viewport.width);
   await close();
  }
  // A fresh client never receives a conversation snapshot, yet reads/saves settings.
  const locale=viewport.width<500?'zh-CN':'en',t=key=>translate(locale,key);await config(after,{hold:[],release:'all',snapshot:false,phase:'running',revoked:false,boot:'settings-boot',control:'settings-control',name:'No snapshot assistant',revision:1,configRevision:0,model:'synthetic-original'});
  const page=await launch(viewport);await page.goto(after+'/?assistant_id=alpha&locale='+locale);await page.getByRole('button',{name:t('assistantSettings'),exact:true}).click();await page.waitForFunction(()=>{const node=document.querySelector('input[list][aria-label="Model"],input[list][aria-label="模型"]');return node&&!node.disabled&&node.value==='synthetic-original';});
  await page.locator('[data-model-scope="assistant"] > summary').click();
  await page.getByLabel(locale==='en'?'Model':'模型',{exact:true}).fill('synthetic-next');const noSnapshotSave=page.getByRole('button',{name:locale==='en'?'Save':'保存',exact:true});
  for(const identity of [{boot:'replacement-boot'},{boot:'settings-boot',control:'replacement-control'}]){await config(after,identity);await noSnapshotSave.click();await page.getByText(t('identityChanged'),{exact:true}).waitFor();check((await config(after)).state.model==='synthetic-original','stale Control/boot request cannot mutate replacement '+viewport.width+' '+JSON.stringify(identity));}
  await config(after,{boot:'settings-boot',control:'settings-control'});await noSnapshotSave.click();await until(async()=> (await config(after)).state.model==='synthetic-next','no snapshot model save');
  const result=await config(after);check(result.state.pending&&result.state.phase==='running','no-snapshot save persists pending model without stopping execution '+viewport.width);check(!result.calls.some(call=>['/api/v1/messages','/api/v1/turn/cancel','/api/v1/turn/control'].includes(call.path)&&call.method==='POST'),'settings never start/stop model work '+viewport.width);await close();
  await config(after,{hold:['directory'],snapshot:false});const general=await launch(viewport);await general.goto(after+'/?locale='+locale);if(viewport.width<500)await general.getByRole('button',{name:t('menu'),exact:true}).click();const settings=general.getByRole('button',{name:t('settings'),exact:true});check(!await settings.isDisabled(),'Settings navigation works before directory confirmation '+viewport.width);await settings.click();const language=general.getByLabel(locale==='en'?'Language':'语言',{exact:true});await language.waitFor();check(!await language.isDisabled()&&!await general.locator('.page-host').evaluate(node=>node.inert),'local preferences remain interactive with unconfirmed directory '+viewport.width);await close();await config(after,{hold:[],release:'all'});
 }
 const page=await launch({width:1280,height:800});await page.goto(after+'/scripts/tests/fixtures/assistant-settings.html?locale=en');await page.waitForFunction(()=>window.assistantSettingsResults?.passed!==undefined);const profileResult=await page.evaluate(()=>window.assistantSettingsResults);report.observations.push({name:'profile-field-retry',profileResult});check(profileResult.passed,'profile-only loading/retry/locale/native-field/late-read contracts'+(profileResult.error?': '+profileResult.error:''));await close();
 check(!report.errors.length,'no unexpected browser runtime errors');report.passed=true;
}catch(error){report.failure=error.stack;process.exitCode=1;}
finally{await close();for(const child of children){child.kill('SIGTERM');await new Promise(done=>child.once('exit',done));}report.origins=origins;await writeFile(resolve(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,failure:report.failure,output,origins}));}
