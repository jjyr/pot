import assert from 'node:assert/strict';

// Run against an already ready, synthetic production ResourceView. The caller
// owns the browser/fixture port. Every URL is absolute and under that origin;
// all probe routes are fulfilled locally before reaching a real application API.
async function issue(_body,{origin,phase,nonce,expectPolicy}){
 const path=kind=>new URL(`/api/v1/__resource-boundary/${kind}?phase=${phase}&nonce=${nonce}`,origin).href;
 const violations=[],nodes=[];let finish;
 const policy=new Promise(resolve=>finish=resolve),required=['connect-src','img-src','frame-src'];
 const listen=event=>{if(event.blockedURI.startsWith(origin+'/api/v1/__resource-boundary/')){violations.push({directive:event.effectiveDirective,blocked:event.blockedURI});if(required.every(value=>violations.some(event=>event.directive===value)))finish();}};
 document.addEventListener('securitypolicyviolation',listen);
 const timer=expectPolicy?setTimeout(()=>finish(),2000):null;
 const image=new Image(),imageResult=new Promise(resolve=>{image.onload=()=>resolve('loaded');image.onerror=()=>resolve('error');});image.alt='';nodes.push(image);image.src=path('image');document.body.append(image);
 const nested=document.createElement('iframe');nodes.push(nested);nested.src=path('frame');document.body.append(nested);
 const sink=document.createElement('iframe');sink.name='resource-form-'+nonce;nodes.push(sink);document.body.append(sink);
 const form=document.createElement('form');nodes.push(form);form.method='POST';form.action=path('form');form.target=sink.name;
 const input=document.createElement('input');input.name='probe';input.value='synthetic';form.append(input);document.body.append(form);
 const action=form.action,method=form.method;let formError='';try{HTMLFormElement.prototype.submit.call(form);}catch(error){formError=error.name+': '+error.message;}
 let fetchRejected=false,fetchError='';try{await fetch(path('fetch'),{credentials:'omit',cache:'no-store'});}catch(error){fetchRejected=true;fetchError=error.name+': '+error.message;}
 const imageOutcome=await imageResult;if(expectPolicy)await policy;
 clearTimeout(timer);document.removeEventListener('securitypolicyviolation',listen);for(const node of nodes)node.remove();
 return {action,method,formSubmitCalled:true,formError,fetchURL:path('fetch'),fetchRejected,fetchError,imageOutcome,violations,origin:window.origin};
}

export async function resourceDocumentNetworkBoundary(browser,page,origin,{selector='iframe.resource-interactive'}={}){
 assert.equal(new URL(page.url()).origin,origin,'Only the authorized synthetic origin is permitted');
 const active=page.context();await active.setOffline(false);
 const reachable=await page.request.get(origin+'/scripts/tests/fixtures/prepared-empty.html');assert(reachable.ok(),'The synthetic HTTP server must actually be reachable online');
 const control=await browser.newContext({serviceWorkers:'block'}),positive=await control.newPage(),counts={control:{fetch:0,image:0,frame:0,form:0},author:{fetch:0,image:0,frame:0,form:0}},nonce=crypto.randomUUID(),consoleMessages=[];
 let allControl,formBlocked;const seenAll=new Promise(resolve=>allControl=resolve),blockedForm=new Promise(resolve=>formBlocked=resolve);
 const capture=message=>{const text=message.text();if(text.includes(nonce)){consoleMessages.push(text);if(/form submission|form-action|allow-forms/i.test(text))formBlocked();}};page.on('console',capture);
 const route=async route=>{
  const url=new URL(route.request().url()),phase=url.searchParams.get('phase'),kind=url.pathname.split('/').at(-1);if(counts[phase]?.[kind]!==undefined)counts[phase][kind]++;
  if(Object.values(counts.control).every(value=>value>0))allControl();
  if(kind==='image')await route.fulfill({status:200,contentType:'image/png',body:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==','base64')});
  else await route.fulfill({status:200,contentType:kind==='fetch'?'application/json':'text/html',body:kind==='fetch'?'{}':'<!doctype html><p>Synthetic route reached</p>',headers:{'cache-control':'no-store'}});
 };
 const pattern=origin+'/api/v1/__resource-boundary/**';
 await control.route(pattern,route);await active.route(pattern,route);
 const controlURL=origin+'/__resource-boundary-positive';await control.route(controlURL,route=>route.fulfill({status:200,contentType:'text/html',body:'<!doctype html><body>Positive control without CSP</body>',headers:{'cache-control':'no-store'}}));
 try{
  const response=await positive.goto(controlURL);assert(!response.headers()['content-security-policy'],'Positive control must have no CSP');
  const positiveResult=await positive.locator('body').evaluate(issue,{origin,phase:'control',nonce,expectPolicy:false});
  let timer;try{await Promise.race([seenAll,new Promise((_,reject)=>timer=setTimeout(()=>reject(Error('Positive control did not reach every synthetic route')),3000))]);}finally{clearTimeout(timer);}
  assert(Object.values(counts.control).every(value=>value>0),'Valid fetch/image/nested-frame/form must all reach their synthetic route without CSP');assert(!positiveResult.fetchRejected&&positiveResult.imageOutcome==='loaded');assert.equal(positiveResult.method,'post');assert(positiveResult.action.startsWith(origin+'/api/v1/__resource-boundary/form?'));
  const negative=await page.frameLocator(selector).locator('body').evaluate(issue,{origin,phase:'author',nonce,expectPolicy:true});
  assert.equal(negative.origin,'null');assert(negative.fetchRejected&&!/parse.*URL|Invalid URL/i.test(negative.fetchError),'Absolute fetch must fail at the policy boundary, not URL parsing');assert.equal(negative.imageOutcome,'error');assert(['connect-src','img-src','frame-src'].every(value=>negative.violations.some(event=>event.directive===value)),'Observe actual CSP violations for valid absolute probes');
  if(!negative.violations.some(event=>event.directive==='form-action')){let timer;try{await Promise.race([blockedForm,new Promise((_,reject)=>timer=setTimeout(()=>reject(Error('Missing observable sandbox/form-action denial')),1000))]);}finally{clearTimeout(timer);}}
  assert(negative.formSubmitCalled&&negative.action.startsWith(origin+'/api/v1/__resource-boundary/form?'));assert(Object.values(counts.author).every(value=>value===0),'No author probe may reach the physical browser route');
  return {online:true,counts,positive:positiveResult,author:negative,consoleMessages};
 }finally{page.off('console',capture);await active.unroute(pattern,route);await control.close();}
}
