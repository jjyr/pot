import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdir} from 'node:fs/promises';
import {launchBrowser} from './browser-harness.mjs';
import {translate} from '../../web/src/locales/messages.mjs';

const child=spawn(process.execPath,['scripts/tests/pot-ui-settings-fixture.mjs'],{stdio:['ignore','pipe','inherit']});
const origin=await new Promise((resolve,reject)=>{child.stdout.on('data',data=>{const match=String(data).match(/http:\/\/127\.0\.0\.1:\d+/);if(match)resolve(match[0]);});child.on('exit',code=>reject(Error('Fixture exited '+code)));});
const browser=await launchBrowser(),output='output/screenshots/model-picker';await mkdir(output,{recursive:true});
const configure=async body=>(await fetch(origin+'/__fixture',body?{method:'POST',body:JSON.stringify(body)}:{})).json();
const providers={profiles:[{id:'synthetic-provider',name:'OpenAI',revision:1,model:'saved/opaque'},{id:'second',name:'Other',revision:1,model:'other-default'}],connections:[{profile_id:'synthetic-provider',type_id:'standard/openai',catalog:{models:[{id:'cached/picked',name:'Cached model'},{id:'embedding-only',output_modalities:['embedding']}]}},{profile_id:'second',type_id:'standard/other'}],provider_types:[{id:'standard/openai',directory_provider:'openai'},{id:'standard/other',directory_provider:'other'}],directory:[{id:'openai',models:[{id:'public-only',name:'Public model'},{id:'embedding-only'}]},{id:'other',models:[{id:'other-default'}]}],configured_models:{'synthetic-provider':['discovered-only']}};
try{
 for(const width of [1280,390])for(const locale of ['en','zh-CN']){
  await configure({providers,model:'saved/opaque',hold:['providers','configuration','rules'],release:'all',failSave:false,failRead:false,revoked:false});
  const context=await browser.newContext({viewport:{width,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  try{
   await page.goto(origin+'/?assistant_id=alpha&locale='+locale);
   await page.getByRole('button',{name:translate(locale,'assistantSettings'),exact:true}).click();
   await page.getByLabel(locale==='en'?'Name':'名称',{exact:true}).waitFor();
   await page.locator('[data-model-scope="assistant"] > summary').click();
   await page.locator('[data-model-scope="task"] > summary').click();
   assert(!await page.locator('.editor-form').innerText().then(text=>/Loading…|正在加载/.test(text)),'Settings reads stay silent');
   await configure({hold:[],release:'all'});
   const input=page.getByLabel(translate(locale,'agentLinks.assistantModel'),{exact:true});
   await page.waitForFunction(()=>{const input=document.querySelector('.editor-form input[role=combobox]');return input&&!input.disabled&&input.value==='saved/opaque';});
   await input.click();
   const picker=input.locator('..'),list=picker.getByRole('listbox');
   const options=await list.getByRole('option').allTextContents();
   for(const id of ['saved/opaque','Public model (public-only)','Cached model (cached/picked)','discovered-only'])assert(options.includes(id),id);
   assert(!options.includes('embedding-only'));assert(!options.includes('other-default'));
   await input.evaluate(node=>window.retainedModelInput=node);
   assert.equal(await page.locator('.model-browse').count(),0);
   await page.screenshot({path:`${output}/model-input-${width}-${locale}-after.png`});
   const bounds=await list.boundingBox();assert(bounds.x>=0&&bounds.x+bounds.width<=width,'Picker stays within viewport');
   await list.getByRole('option',{name:'Public model (public-only)',exact:true}).click();
   assert.equal(await input.inputValue(),'public-only');assert.equal(await input.getAttribute('aria-expanded'),'false');
   assert(await page.getByRole('button',{name:translate(locale,'assistant.browseModels'),exact:true}).nth(1).isDisabled());
   await input.fill('cached');assert.deepEqual(await list.getByRole('option').allTextContents(),['Cached model (cached/picked)']);
   await input.press('ArrowDown');await input.press('Enter');assert.equal(await input.inputValue(),'cached/picked');
   await picker.getByRole('button').click();assert.equal(await list.getByRole('option').count(),4);
   await input.press('Escape');assert.equal(await input.getAttribute('aria-expanded'),'false');
   await input.click();await input.dispatchEvent('compositionstart');await input.press('ArrowDown');await input.press('Enter');
   assert.equal(await input.inputValue(),'cached/picked','IME confirmation does not select another model');
   await input.dispatchEvent('compositionend');await providerOutside();
   async function providerOutside(){await page.getByLabel(locale==='en'?'Name':'名称',{exact:true}).click();assert.equal(await input.getAttribute('aria-expanded'),'false');}
   const taskProvider=page.getByLabel(translate(locale,'assistant.taskProvider'),{exact:true});
   await taskProvider.selectOption('synthetic-provider');
   const taskInput=page.getByLabel(translate(locale,'assistant.taskModel'),{exact:true});await taskInput.click();
   await taskInput.locator('..').getByRole('option',{name:'Cached model (cached/picked)',exact:true}).click();
   assert.equal(await taskInput.inputValue(),'cached/picked');await taskProvider.selectOption('');
   // Native text editing accepts both a catalog ID and an unlisted opaque ID.
   await input.fill('cached/picked');assert.equal(await input.inputValue(),'cached/picked');
   const provider=page.getByLabel(locale==='en'?'Provider':'提供商',{exact:true});await provider.selectOption('second');
   assert.equal(await input.inputValue(),'other-default');await input.click();assert.deepEqual(await input.evaluate(node=>[...node.parentElement.querySelectorAll('[role=option]')].map(option=>option.textContent)),['other-default']);

   await provider.selectOption('synthetic-provider');await input.fill('my-route/new-model');
   assert(await input.evaluate(node=>node===window.retainedModelInput),'Switching providers preserves the editable input');
   await page.getByRole('button',{name:locale==='en'?'Save':'保存',exact:true}).click();
   await page.waitForFunction(()=>!document.querySelector('.editor-form'));
   const saved=await configure();assert.equal(saved.state.model,'my-route/new-model');
   const mutations=saved.calls.filter(call=>call.method==='POST');assert(!mutations.some(call=>call.path==='/api/v1/providers/check'||call.path==='/api/v1/messages'),'Model choices must not trigger discovery or inference');
   assert.deepEqual(errors,[]);console.log(JSON.stringify({width,locale,options,customSaved:true,retainedInput:true}));
  }finally{await context.close();}
 }
}finally{await browser.close();child.kill('SIGTERM');await new Promise(resolve=>child.once('exit',resolve));}
