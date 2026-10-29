import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {launchBrowser,sourceServer} from './browser-harness.mjs';
import {translate} from '../../web/src/locales/messages.mjs';

const child=spawn(process.execPath,['scripts/tests/pot-ui-settings-fixture.mjs'],{stdio:['ignore','pipe','inherit']});
const origin=await new Promise((resolve,reject)=>{child.stdout.on('data',data=>{const found=String(data).match(/http:\/\/127\.0\.0\.1:\d+/);if(found)resolve(found[0]);});child.on('exit',code=>reject(Error('Fixture exited '+code)));});
const configure=async body=>(await fetch(origin+'/__fixture',body?{method:'POST',body:JSON.stringify(body)}:{})).json();
const browser=await launchBrowser();
try{
 for(const locale of ['en','zh-CN']){
  const context=await browser.newContext({serviceWorkers:'block',viewport:{width:locale==='en'?1280:390,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  try{
   await configure({hold:[],release:'all',failRead:false,failSave:false,revoked:false,configRevision:0,profile_id:'synthetic-provider',providers:{profiles:[{id:'synthetic-provider',name:'Original Provider',revision:1,model:'synthetic-original'},{id:'changed-provider',name:'Changed Provider',revision:1,model:'remote-new-model'}]},task_default:null,model:'synthetic-original',rules:'Cached personality'});
   const open=async()=>{await page.getByRole('button',{name:translate(locale,'assistantSettings'),exact:true}).click();await page.locator('.editor-form').waitFor();await page.locator('[data-model-scope="assistant"] > summary').click();};
   const model=()=>page.getByLabel(translate(locale,'agentLinks.assistantModel'),{exact:true});
   const personality=()=>page.getByLabel(translate(locale,'assistant.personality'),{exact:true});
   const save=()=>page.getByRole('button',{name:locale==='en'?'Save':'保存',exact:true}).click();
   await page.goto(origin+'/?assistant_id=alpha&locale='+locale);await open();
   await page.waitForFunction(()=>{const input=document.querySelector('.editor-form input[list]');return input&&!input.disabled&&input.value==='synthetic-original';});
   await personality().waitFor({state:'attached'});
   await page.waitForFunction(()=>document.querySelector('.editor-form textarea')?.value==='Cached personality');
   await configure({hold:['configuration','providers','rules'],profile_id:'changed-provider',model:'remote-new-model',rules:'Fresh personality',configRevision:1,task_default:{profile_id:'synthetic-provider',model:'fresh-task-model'}});
   await page.reload();await open();
   await page.waitForFunction(()=>{const input=document.querySelector('.editor-form input[list]');return input&&!input.disabled&&input.value==='synthetic-original';});
   assert.equal(await personality().inputValue(),'Cached personality');
   assert(!/Loading…|正在加载/.test(await page.locator('.editor-form').innerText()));
   await model().fill('my-unsaved-model');
   await model().evaluate(node=>{window.retainedModel=node;node.focus();node.setSelectionRange(2,5);});
   const before=(await configure()).calls.filter(call=>call.method==='POST').length;
   await save();
   await page.locator('.editor-form [data-error-key="assistant.settingsLoadFailed"]').waitFor();
   assert.equal((await configure()).calls.filter(call=>call.method==='POST').length,before,'Retained display cannot supply write authority');
   assert.equal(await model().inputValue(),'my-unsaved-model');
   await model().evaluate(node=>{node.focus();node.setSelectionRange(2,5);});
   await configure({hold:[],release:'all'});
   await page.waitForFunction(()=>document.querySelector('.editor-form textarea')?.value==='Fresh personality');
   await page.waitForFunction(()=>[...document.querySelectorAll('.editor-form input[list]')][1]?.value==='fresh-task-model');
   assert.deepEqual(await page.evaluate(()=>({same:document.querySelector('.editor-form input[list]')===window.retainedModel,focused:document.activeElement===window.retainedModel,value:window.retainedModel.value,start:window.retainedModel.selectionStart,end:window.retainedModel.selectionEnd})),{same:true,focused:true,value:'my-unsaved-model',start:2,end:5});
   await save();await page.waitForFunction(()=>!document.querySelector('.editor-form'));
   const post=(await configure()).calls.filter(call=>call.path==='/api/v1/agent'&&call.method==='POST').at(-1);
   assert.equal(post.body.expected_revision,1);assert.equal(post.body.selection.profile_id,'synthetic-provider','A model draft stays bound to its original provider');assert.equal(post.body.selection.model,'my-unsaved-model');
   assert.deepEqual(post.body.selection.task_default,{profile_id:'synthetic-provider',model:'fresh-task-model',context_window:null,max_output_tokens:null,reasoning_effort:null});
   assert.deepEqual(errors,[]);
   console.log(JSON.stringify({locale,cachedBeforeTransport:true,staleSaveBlocked:true,dirtyFocusPreserved:true,cleanFieldsRefreshed:true,freshRevisionUsed:true}));
  }finally{await context.close();}
 }
 const source=await sourceServer(),context=await browser.newContext({serviceWorkers:'block'}),page=await context.newPage();
 try{
  await page.route('**/__empty',route=>route.fulfill({contentType:'text/html',body:'<!doctype html>'}));await page.goto(source.origin+'/__empty');
  const result=await page.evaluate(async()=>{
   const {createResourceReads}=await import('/web/src/state/resource-cache.mjs'),{createQueryRegistry}=await import('/web/src/state/domains/query.mjs'),{createExtensionsDomain}=await import('/web/src/state/domains/extensions.mjs');
   const name='extensions-local-first-'+crypto.randomUUID(),scope={space:'s',owner:'o'},catalog={items:[{id:'plugin',kind:'plugins',name:'Cached plugin',revision:1,enabled:true,authorization_url:'https://secret.invalid',credentials:{token:'secret'}}],mcp:[]};
   let release,hold=false,calls=0,revision=1;
   const client={production:true,chatCacheName:name,runtime:{runtime_id:'r',boot_id:'b'},spaces:[{id:'s',control_id:'r'}],scope:()=>scope,transport:async()=>{calls++;if(hold)await new Promise(resolve=>release=resolve);return {...catalog,items:catalog.items.map(item=>({...item,revision}))};}};
   let resources=createResourceReads(client),queries=createQueryRegistry(resources),domain=createExtensionsDomain(client,{resources,queries});
   await domain.catalog(scope).value();queries.dispose();domain.dispose();resources.clear();
   hold=true;resources=createResourceReads(client);queries=createQueryRegistry(resources);domain=createExtensionsDomain(client,{resources,queries});
   const displayed=await domain.catalog(scope).value();
   const safe=!JSON.stringify(displayed).includes('secret');
   let confirmed=false;const pending=domain.confirm(scope,{id:'plugin',kind:'plugins',revision:1}).then(()=>confirmed=true,error=>error.message);
   await new Promise(resolve=>setTimeout(resolve,30));const blocked=!confirmed;
   revision=2;hold=false;release();const error=await pending;
   queries.dispose();resources.clear();
   return {displayed:displayed.items[0].name,safe,blocked,error,calls};
  });
  assert.equal(result.displayed,'Cached plugin');assert(result.safe);assert(result.blocked);assert.equal(result.error,'Extension revision changed');
  console.log(JSON.stringify(result));
 }finally{await context.close();await source.close();}
}finally{await browser.close();child.kill('SIGTERM');await new Promise(resolve=>child.once('exit',resolve));}
