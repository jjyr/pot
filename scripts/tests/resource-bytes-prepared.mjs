import assert from 'node:assert/strict';
// Run after the production resource fixture has committed its immutable bundle.
// This uses the real window preparation/reframe path, not a direct style change.
export async function persistentHTMLPrepared(page){
 await page.evaluate(async()=>{
  const {createMessageWindow}=await import('/components/message-window.mjs'),{createReplyBubble}=await import('/components/reply.mjs'),{createPreparationSurface,createPreparedRender,withPreparedRender,settlePreparedTree,retirePreparedTree}=await import('/components/prepared-render.mjs');
  const scroller=document.createElement('div');scroller.id='resource-prepared-host';scroller.className='history';scroller.style.cssText='width:700px;height:600px;flex:none';const column=document.createElement('div');column.className='message-column';const host=document.createElement('div');host.className='message-window';column.append(host);scroller.append(column);document.querySelector('main').append(scroller);
  const f=window.resourcePrepared={scroller,host,acquired:0,released:0,retirePreparedTree,createPreparationSurface,createPreparedRender,withPreparedRender,settlePreparedTree};
  const rows=[{id:'offline-html',viewKey:'offline-html',role:'assistant',text:'',phase:'complete',status:'complete',presentation:{resource:resourceFixture.data.resources.at(-1),mode:'preview'}}];
  const options={t:key=>key,acquireResource:resource=>{f.acquired++;const lease=resourceFixture.client.acquireResource(resource,resourceFixture.scope);return{...lease,release(){f.released++;lease.release();}};},downloadResource:()=>{}};
  f.render=(message,old)=>{if(!old){const row=document.createElement('article');row.className='message role-assistant';row.dataset.messageId=message.id;const body=document.createElement('div');body.className='message-body';row.append(body);old={row,reply:createReplyBubble(body)};}old.reply.update(message,options);return old;};
  f.draw=()=>f.ui.setMessages(rows);f.ui=createMessageWindow({scroller,host,render:f.render,dispose:record=>retirePreparedTree(record.row),requestRender:()=>f.draw()});f.rows=rows;f.draw();
 });
 await page.waitForFunction(()=>resourcePrepared.ui.prepared&&resourcePrepared.host.querySelector('pot-resource')?.dataset.state==='ready');
 const child=page.frameLocator('#resource-prepared-host iframe.resource-interactive');await child.locator('#value').fill('prepared-kept-value');await child.locator('#value').focus();await child.locator('body').evaluate(()=>{window.retainedDocument=document;window.retainedIdentity=window.documentIdentity;});
 await page.evaluate(()=>{const f=resourcePrepared;f.original=f.host.querySelector('pot-resource');f.frame=f.original.querySelector('iframe');f.window=f.frame.contentWindow;f.before=f.acquired;});
 const results=[];
 for(const [width,font]of [[340,'19px'],[700,'16px']]){
  await page.evaluate(({width,font})=>{const f=resourcePrepared;f.scroller.style.width=width+'px';f.scroller.style.fontSize=font;f.draw();},{width,font});
  await page.waitForFunction(()=>resourcePrepared.ui.prepared);
  const same=await child.locator('body').evaluate(()=>retainedDocument===document&&retainedIdentity===window.documentIdentity&&document.querySelector('#value').value==='prepared-kept-value'&&document.activeElement.id==='value');
  const metrics=await page.evaluate(async()=>{const f=resourcePrepared,sameNode=f.host.querySelector('pot-resource')===f.original&&f.original.querySelector('iframe')===f.frame&&f.window===f.frame.contentWindow,leases=f.acquired-f.before;const surface=f.createPreparationSurface(f.scroller,f.host),context=f.createPreparedRender(),fresh=f.withPreparedRender(context,'offline-html',()=>f.render(f.rows[0]));surface.host.append(fresh.row);await f.settlePreparedTree(fresh.row,context).promise;const live=f.host.querySelector('article').getBoundingClientRect().height,independent=fresh.row.getBoundingClientRect().height;surface.close();f.before=f.acquired;return{sameNode,leases,live,independent,delta:independent-live};});
  assert(same&&metrics.sameNode,'Prepared width/font reframe retains HTML document/window/state/focus');assert.equal(metrics.leases,0,'Reframe does not acquire another live resource lease');assert(Math.abs(metrics.delta)<.6,'Prepared HTML geometry equals independently prepared current-width DOM');results.push({width,font,...metrics,sameDocumentAndFocus:same});
 }
 await page.evaluate(()=>{const f=resourcePrepared;f.ui.close();f.scroller.remove();});return results;
}
