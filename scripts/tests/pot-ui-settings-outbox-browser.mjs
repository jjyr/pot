// Combined production UI/HTTP/SSE/IDB contract. The fixture owns only synthetic
// messages/configuration; observation properties below never replace modules.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {launchBrowser} from './browser-harness.mjs';
import {waitForOutbox} from './outbox-persistence-browser.mjs';
import {translate} from '../../web/src/locales/messages.mjs';

const output=resolve(process.env.POT_COMBINED_OUTPUT||'.pot-artifacts/settings-outbox');
await mkdir(output,{recursive:true});
const report={passed:false,checks:[],errors:[],source:process.env.POT_SETTINGS_SOURCE};
const child=spawn(process.execPath,[resolve('scripts/tests/pot-ui-settings-fixture.mjs')],{
  env:{...process.env,POT_SETTINGS_OUTBOX:'1'},stdio:['ignore','pipe','pipe'],
});
child.stderr.on('data',data=>report.errors.push(String(data)));
const origin=await new Promise((done,reject)=>{
  let text='';child.stdout.on('data',data=>{text+=data;const match=text.match(/http:\/\/127\.0\.0\.1:\d+/);if(match)done(match[0]);});
  child.on('exit',code=>reject(Error('Fixture exited '+code)));
});
console.log('OWN_FIXTURE '+origin);report.origin=origin;
const config=async body=>(await fetch(origin+'/__fixture',body?{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}:{})).json();
const check=(condition,label)=>{assert(condition,label);report.checks.push(label);};
let browser;
try{
  browser=await launchBrowser();
  for(const [width,locale]of [[1280,'en'],[390,'zh-CN']]){
    const t=key=>translate(locale,key),context=await browser.newContext({viewport:{width,height:844},serviceWorkers:'block'});
    const page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push({message:error.message,stack:error.stack}));
    try{
      await config({phase:'ready',historyCount:48,hold:['configuration','rules'],release:'all',failSave:false,name:'Synthetic Assistant',model:'synthetic-original',revision:1,configRevision:0,revoked:false});
      const callStart=(await config()).calls.length;
      await page.goto(origin+'/?assistant_id=alpha&locale='+locale);
      await page.waitForFunction(()=>!!document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
      await page.waitForFunction(()=>document.querySelector('.connection-status.is-connected'));
      await config({hold:['configuration','rules','snapshot']});
      await context.setOffline(true);
      const send=async text=>{await page.locator('#pot-composer').fill(text);await page.locator('.send-button').click();};
      await send('First fixed submission '+width);
      await waitForOutbox(page,([rows])=>rows.length===1&&rows[0].state==='queued');
      await send('Second fixed submission '+width);
      const queued=await waitForOutbox(page,([rows])=>rows.length===2&&rows.every(row=>row.state==='queued'));
      const target={host:'settings-control',control:'settings-control',space:'local',owner:'alpha',conversation:'synthetic-conversation'};
      for(const row of queued.rows){assert.deepEqual(row.target,{...target,key:JSON.stringify(Object.values(target))});assert.equal(row.request.conversation_id,target.conversation);}
      assert.equal(new Set(queued.rows.map(row=>row.request.request_id)).size,2);
      check(true,'local queue captures complete immutable target and distinct request IDs '+width);
      const identities=queued.rows.map(row=>({target:row.target,request:row.request,sequence:row.sequence}));
      await page.locator('#pot-composer').fill('Ordinary unsent draft '+width);
      await waitForOutbox(page,([rows,drafts])=>drafts.some(row=>row.text.startsWith('Ordinary unsent draft')));
      const history=page.locator('.workspace > .history:not([data-preparation-surface])');await history.waitFor();
      await page.waitForFunction(()=>{const h=document.querySelector('.workspace > .history:not([data-preparation-surface])');return h&&!h.hasAttribute('aria-busy')&&h.scrollHeight>h.clientHeight;});
      const box=await history.boundingBox();await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.wheel(0,-350);
      await page.waitForFunction(()=>{const h=document.querySelector('.workspace > .history:not([data-preparation-surface])');return h&&h.scrollTop+h.clientHeight<h.scrollHeight-100;});
      const reading=await history.evaluate(h=>{const edge=h.getBoundingClientRect().top;const row=[...h.querySelectorAll('article[data-message-id]')].find(row=>row.getBoundingClientRect().top>=edge);return {id:row.dataset.messageId,top:row.getBoundingClientRect().top-edge};});
      await page.getByRole('button',{name:t('assistantSettings'),exact:true}).click();
      const name=page.getByLabel(locale==='en'?'Name':'名称',{exact:true});await name.waitFor();
      check(!await name.isDisabled()&&!await page.locator('.page-host').evaluate(node=>node.inert),'offline settings editable while outbox waits '+width);
      check(await page.getByLabel(locale==='en'?'Model':'模型',{exact:true}).isDisabled(),'unavailable settings section alone is disabled '+width);
      await name.fill('Submitted settings '+width);
      await page.evaluate(()=>{window.combinedNameNode=document.querySelector('.editor-form input[aria-label]');});
      const save=page.getByRole('button',{name:locale==='en'?'Save':'保存',exact:true});await save.click();
      await page.waitForFunction(()=>{const status=document.querySelector('.editor-actions + .inline-error,[role="alert"][data-error-key]');return status&&status.textContent.trim();});
      check(await name.inputValue()==='Submitted settings '+width,'offline save retains form without queuing configuration '+width);
      await config({hold:['configuration','rules','snapshot','save']});await context.setOffline(false);
      // Failed sections retain explicit retries; a successful connection does
      // not replace the name control or discard its edits.
      await page.locator('[data-section="instructions"] > summary').click();
      for(const retry of await page.locator('[data-section-state="error"] button').elementHandles())await retry.click();
      check(await page.locator('[data-section-state="loading"]').count()===2,'configuration and instructions retry independently while held '+width);
      await save.click();
      await name.fill('Newer settings edit '+width);
      check(!await name.isDisabled()&&await save.isDisabled(),'pending save permits new editing and deduplicates '+width);
      await config({release:'save',hold:['configuration','rules','snapshot']});
      await page.getByText(t('settings.savedDraftRemaining'),{exact:true}).waitFor();
      check((await config()).state.name==='Submitted settings '+width&&await name.inputValue()==='Newer settings edit '+width,'save acknowledges only captured settings snapshot '+width);
      check(await page.evaluate(()=>window.combinedNameNode===document.querySelector('.editor-form input[aria-label]')),'settings keeps original native field '+width);
      await config({failSave:true});await save.click();await page.getByText(t('settings.saveFailed'),{exact:true}).waitFor();
      check(await name.inputValue()==='Newer settings edit '+width,'unreachable Control retains settings input '+width);
      await config({failSave:false,hold:['snapshot'],release:'all',phase:'running',change:true});
      await page.waitForFunction(()=>{const input=document.querySelector('input[list][aria-label="Model"],input[list][aria-label="模型"]');return input&&!input.disabled;});
      await page.getByLabel(locale==='en'?'Model':'模型',{exact:true}).fill('synthetic-next');
      await save.click();await page.locator('.editor-form').waitFor({state:'hidden'});
      const saved=await config();check(saved.state.model==='synthetic-next'&&saved.state.pending,'running saves next configuration without ending execution '+width);
      // Snapshot reads are held at the network; cached chat and local input
      // remain usable. The queued payloads must not acquire settings fields.
      await page.waitForFunction(()=>document.querySelector('#pot-composer')?.value.startsWith('Ordinary unsent draft'));
      check(await page.locator('#pot-composer').inputValue()==='Ordinary unsent draft '+width,'returning from settings preserves ordinary draft '+width);
      await page.waitForFunction(reading=>{const h=document.querySelector('.workspace > .history:not([data-preparation-surface])'),row=h&&[...h.querySelectorAll('article[data-message-id]')].find(row=>row.dataset.messageId===reading.id);return row&&Math.abs(row.getBoundingClientRect().top-h.getBoundingClientRect().top-reading.top)<=24;},reading);
      check(true,'settings return preserves the latest reading anchor within 24px '+width);
      const unchanged=await waitForOutbox(page,([rows])=>rows.length===2);
      assert.deepEqual(unchanged.rows.map(row=>({target:row.target,request:row.request,sequence:row.sequence})),identities);
      check(!saved.calls.slice(callStart).some(call=>call.method==='POST'&&['/api/v1/messages','/api/v1/turn/steer'].includes(call.path)),'held snapshot prevents POST but preserves queue identity/order '+width);
      await send('Third submission while network reads held '+width);
      await waitForOutbox(page,([rows])=>rows.length===3);
      await history.locator('article.message:visible').filter({hasText:'Third submission while network reads held '+width}).waitFor();
      await page.screenshot({path:resolve(output,'combined-'+width+'.png')});
      await config({hold:[],release:'all'});
      const accepted=await waitForOutbox(page,([rows])=>rows.length===3&&rows.every(row=>row.state==='accepted'&&!row.projection_pending),{timeout:45000});
      const posts=(await config()).calls.slice(callStart).filter(call=>call.method==='POST'&&['/api/v1/messages','/api/v1/turn/steer'].includes(call.path)).slice(-3);
      assert.deepEqual(posts.map(call=>call.body.request_id),accepted.rows.map(row=>row.request.request_id));
      check(posts.every(call=>call.path==='/api/v1/turn/steer'&&call.body.user_message_id==='synthetic-active-turn'&&call.query.assistant_id==='alpha'&&call.query.space_id==='local'),'FIFO uses original target and existing active turn after settings save '+width);
      check(!posts.some(call=>call.body.text.startsWith('Ordinary unsent draft')),'ordinary draft sends only when explicitly clicked '+width);
      report.snapshots??=[];report.snapshots.push({width,identities,accepted:accepted.rows,posts});
    }finally{await context.close();}
  }
  check(report.errors.length===0,'no unexpected browser errors');report.passed=true;
}catch(error){report.failure=error.stack;throw error;}
finally{
  await browser?.close();child.kill('SIGTERM');await new Promise(done=>child.exitCode!==null?done():child.once('exit',done));
  await writeFile(resolve(output,'report.json'),JSON.stringify(report,null,2));
}
