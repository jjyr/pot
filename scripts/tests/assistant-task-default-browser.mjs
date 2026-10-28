import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdir} from 'node:fs/promises';
import {launchBrowser} from './browser-harness.mjs';
import {translate} from '../../web/src/locales/messages.mjs';

const child=spawn(process.execPath,['scripts/tests/pot-ui-settings-fixture.mjs'],{stdio:['ignore','pipe','inherit']});
const origin=await new Promise((resolve,reject)=>{child.stdout.on('data',data=>{const match=String(data).match(/http:\/\/127\.0\.0\.1:\d+/);if(match)resolve(match[0]);});child.on('exit',code=>reject(Error('Fixture exited '+code)));});
const browser=await launchBrowser(),output='output/pr-images/task-default-model';await mkdir(output,{recursive:true});
const configure=async body=>(await fetch(origin+'/__fixture',body?{method:'POST',body:JSON.stringify(body)}:{})).json();
const providers={profiles:[{id:'synthetic-provider',name:'OpenAI',revision:1,model:'saved/opaque'},{id:'second',name:'Other',revision:1,model:'other-default'}],connections:[{profile_id:'synthetic-provider',type_id:'standard/openai',catalog:{models:[{id:'cached/picked',name:'Cached model'},{id:'embedding-only',output_modalities:['embedding']}]}},{profile_id:'second',type_id:'standard/other'}],provider_types:[{id:'standard/openai',directory_provider:'openai'},{id:'standard/other',directory_provider:'other'}],directory:[{id:'openai',models:[{id:'public-only',name:'Public model'},{id:'embedding-only'}]},{id:'other',models:[{id:'other-default'}]}],configured_models:{'synthetic-provider':['discovered-only']}};
try{
 for(const width of [1280,390])for(const locale of ['en','zh-CN']){
  await configure({providers,model:'saved/opaque',task_default:null,pending:false,hold:['providers','configuration','rules'],release:'all',failSave:false,failRead:false,revoked:false,configRevision:0,revision:1,name:'Synthetic Assistant',rules:'Synthetic fixture rules',phase:'running',cursor:1});
  const priorCalls=(await configure()).calls.length;
  const context=await browser.newContext({viewport:{width,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  try{
   await page.goto(origin+'/?assistant_id=alpha&locale='+locale);
   const settingsButton=()=>page.getByRole('button',{name:translate(locale,'assistantSettings'),exact:true});
   const nameField=()=>page.getByLabel(locale==='en'?'Name':'名称',{exact:true});
   const openSettings=async()=>{await settingsButton().click();await nameField().waitFor();};
   const mainModel=()=>page.getByLabel(translate(locale,'agentLinks.assistantModel'),{exact:true});
   const mainProvider=()=>page.getByLabel(locale==='en'?'Provider':'提供商',{exact:true});
   const taskProvider=()=>page.getByLabel(translate(locale,'assistant.taskProvider'),{exact:true});
   const taskModel=()=>page.getByLabel(translate(locale,'assistant.taskModel'),{exact:true});
   const save=async()=>{await page.getByRole('button',{name:locale==='en'?'Save':'保存',exact:true}).click();};
   const waitClosed=()=>page.waitForFunction(()=>!document.querySelector('.editor-form'));
   const modelOptions=input=>input.evaluate(node=>[...node.list.options].map(option=>option.value));
   const waitFollow=()=>page.waitForFunction(()=>{const inputs=[...document.querySelectorAll('.editor-form input[list]')];return inputs.length>=2&&!inputs[0].disabled&&inputs[1].disabled;});
   const waitBoth=()=>page.waitForFunction(()=>{const inputs=[...document.querySelectorAll('.editor-form input[list]')];return inputs.length>=2&&inputs.every(node=>!node.disabled);});
   const agentPosts=async()=>{const snapshot=await configure();return snapshot.calls.slice(priorCalls).filter(call=>call.path==='/api/v1/agent'&&call.method==='POST');};
   const allMutations=async()=>{const snapshot=await configure();return snapshot.calls.slice(priorCalls).filter(call=>call.method==='POST');};

   // 1. Held initial reads stay silent and disabled.
   await openSettings();
   assert(!await page.locator('.editor-form').innerText().then(text=>/Loading…|正在加载/.test(text)),'Settings reads stay silent');
   assert(await mainModel().isDisabled(),'Main model is disabled until configuration reads resolve');
   assert.equal(await taskProvider().inputValue(),'');
   assert(await taskModel().isDisabled(),'Task model is disabled while following');
   await configure({hold:[],release:'all'});
   await page.waitForFunction(()=>{const input=document.querySelector('.editor-form input[list]');return input&&!input.disabled&&input.value==='saved/opaque';});
   await waitFollow();

   // 2. Follow leaves the optional task model unset.
   assert.equal(await taskProvider().inputValue(),'');
   assert(await taskModel().isDisabled());
   assert.equal(await taskModel().inputValue(),'');
   await page.screenshot({path:`${output}/task-default-${width}-${locale}-after.png`});

   // Main model catalog keys remain unchanged.
   const mainOptions=await modelOptions(await mainModel());
   for(const id of ['saved/opaque','public-only','cached/picked','discovered-only'])assert(mainOptions.includes(id),id);
   assert(!mainOptions.includes('embedding-only'));assert(!mainOptions.includes('other-default'));

   // 3. Selecting a task provider seeds provider.model and suggestions from cached/catalog sources.
   await taskProvider().selectOption('second');
   await page.waitForFunction(()=>{const inputs=[...document.querySelectorAll('.editor-form input[list]')];return inputs.length>=2&&inputs[1].value==='other-default';});
   assert.equal(await taskModel().inputValue(),'other-default');
   assert.deepEqual(await modelOptions(await taskModel()),['other-default']);
   await taskModel().fill('my-task/route');
   assert.equal(await taskModel().inputValue(),'my-task/route');

   const otherLocale=locale==='en'?'zh-CN':'en';
   await taskModel().evaluate((node,next)=>{
    window.taskField=node;node.focus();node.setSelectionRange(2,5);
    window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:next}}));
   },otherLocale);
   await page.getByLabel(translate(otherLocale,'assistant.taskModel'),{exact:true}).waitFor();
   assert.deepEqual(await page.evaluate(()=>({connected:window.taskField.isConnected,focused:document.activeElement===window.taskField,value:window.taskField.value,start:window.taskField.selectionStart,end:window.taskField.selectionEnd})),{connected:true,focused:true,value:'my-task/route',start:2,end:5});
   await page.evaluate(value=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value}})),locale);
   assert(await taskModel().evaluate(node=>node===window.taskField),'Locale updates retain the task model input');

   // 4. Simultaneous main + task edit must use ONE config POST.
   await mainModel().fill('cached/picked');
   assert.equal(await mainModel().inputValue(),'cached/picked');
   await save();
   await waitClosed();
   let posts=await agentPosts();
   assert.equal(posts.length,1,'Simultaneous main+task edit must use one config POST');
   assert.equal(posts[0].body.selection.model,'cached/picked');
   assert.deepEqual(posts[0].body.selection.task_default,{profile_id:'second',model:'my-task/route'});
   let mutations=await allMutations();
   assert(!mutations.some(call=>call.path==='/api/v1/providers/check'||call.path==='/api/v1/messages'),'Model choices must not trigger discovery or inference');

   // Reopen persists both selections.
   await openSettings();
   await waitBoth();
   assert.equal(await mainModel().inputValue(),'cached/picked');
   assert.equal(await taskProvider().inputValue(),'second');
   assert.equal(await taskModel().inputValue(),'my-task/route');
   assert(!await taskModel().isDisabled());

   // 5. Main provider change must NOT change task default/custom model.
   await mainProvider().selectOption('second');
   await page.waitForFunction(()=>{const inputs=[...document.querySelectorAll('.editor-form input[list]')];return inputs.length>=2&&inputs[0].value==='other-default';});
   assert.equal(await taskProvider().inputValue(),'second');
   assert.equal(await taskModel().inputValue(),'my-task/route');
   await save();
   await waitClosed();
   posts=await agentPosts();
   assert.equal(posts.length,2,'Main-only edit adds exactly one more config POST');
   const afterMain=(await configure()).state;
   assert.equal(afterMain.model,'other-default');
   assert.deepEqual(afterMain.task_default,{profile_id:'second',model:'my-task/route'},'Main provider change preserves the task default');
   await openSettings();
   await waitBoth();
   assert.equal(await mainModel().inputValue(),'other-default');
   assert.equal(await taskProvider().inputValue(),'second');
   assert.equal(await taskModel().inputValue(),'my-task/route');

   // 6. Follow clears task_model and removes the default.
   await taskProvider().selectOption('');
   await waitFollow();
   assert(await taskModel().isDisabled());
   await save();
   await waitClosed();
   posts=await agentPosts();
   assert.equal(posts.length,3);
   assert.equal(posts[2].body.selection.task_default,null);
   const afterFollow=(await configure()).state;
   assert.equal(afterFollow.task_default,null);
   await openSettings();
   await waitFollow();
   assert.equal(await taskProvider().inputValue(),'');
   assert(await taskModel().isDisabled());

   // 7. Failed save preserves drafts.
   await configure({failSave:true});
   await taskProvider().selectOption('second');
   await page.waitForFunction(()=>{const inputs=[...document.querySelectorAll('.editor-form input[list]')];return inputs.length>=2&&inputs[1].value==='other-default';});
   await taskModel().fill('draft-route');
   await save();
   await page.locator('.editor-form [data-error-key="settings.saveFailed"]').waitFor({state:'visible'});
   assert(await page.locator('.editor-form').isVisible(),'Editor remains open after a failed save');
   assert.equal(await taskModel().inputValue(),'draft-route','Failed save preserves the typed draft');
   await configure({failSave:false});

   // 8. Missing task provider surfaces as the ID rather than clearing.
   await configure({task_default:{profile_id:'missing-provider',model:'custom-route'}});
   await page.reload();
   await openSettings();
   await waitBoth();
   assert(await taskProvider().evaluate(node=>[...node.options].some(option=>option.value==='missing-provider')),'Missing provider is present as an option ID');
   assert.equal(await taskProvider().inputValue(),'missing-provider');
   assert(!await taskModel().isDisabled());
   assert.equal(await taskModel().inputValue(),'custom-route');

   assert.deepEqual(errors,[]);
   console.log(JSON.stringify({width,locale,silent:true,followInherit:true,taskSeeded:true,singleConfigPost:true,mainPreservesTask:true,followCleared:true,draftPreserved:true,missingProviderVisible:true}));
  }finally{await context.close();}
 }
}finally{await browser.close();child.kill('SIGTERM');await new Promise(resolve=>child.once('exit',resolve));}
