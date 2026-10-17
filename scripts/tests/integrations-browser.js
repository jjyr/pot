async page=>{
 const state=FIXTURE_STATE,assert=(value,message)=>{if(!value)throw Error(message)},errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 await page.context().route('**/*',route=>{
  const url=new URL(route.request().url());
  if(url.href==='https://github.com/login/device')return route.fulfill({contentType:'text/html',body:'<h1>GitHub authorization fixture</h1>'});
  return ['127.0.0.1','localhost'].includes(url.hostname)?route.continue():route.abort();
 });
 await page.setViewportSize({width:1200,height:820});
 const login=await page.request.post(state.origin+'/access/token',{headers:{Authorization:'Bearer '+state.token}});assert(login.status()===200,'Browser authentication');
 await page.goto(state.origin+'/?theme=light&locale=en');await page.getByRole('button',{name:'Settings',exact:true}).click();await page.getByRole('button',{name:'Third-party accounts',exact:true}).click();await page.getByRole('heading',{name:'GitHub',exact:true}).waitFor();
 assert(await page.getByText('Not logged in',{exact:true}).isVisible(),'Supported service visible before login');assert(await page.getByRole('button',{name:'Add account',exact:true}).count()===0,'No multiple-account entry');
 if(state.screenshots)await page.screenshot({path:state.screenshots+'/third-party-accounts-disconnected.png'});
 let popupPromise=page.waitForEvent('popup');await page.getByRole('button',{name:'Log in',exact:true}).click();let popup=await popupPromise;await popup.getByRole('heading',{name:'GitHub authorization fixture'}).waitFor();await page.getByText('TEST-CODE',{exact:true}).waitFor();
 assert((await page.request.post(state.origin+'/api/v1/integrations/github/login',{data:{}})).status()===400,'Duplicate pending login rejected');
 await page.getByRole('button',{name:'Cancel',exact:true}).click();await page.getByRole('button',{name:'Log in',exact:true}).waitFor();await page.request.post(state.coord,{headers:{Authorization:'Bearer fixture-coordinator'}});await popup.close();
 const cancelled=await(await page.request.get(state.origin+'/api/v1/integrations')).json();assert(cancelled.items[0].status==='disconnected','Cancelled authorization did not bind account');
 popupPromise=page.waitForEvent('popup');await page.getByRole('button',{name:'Log in',exact:true}).click();popup=await popupPromise;await popup.getByRole('heading',{name:'GitHub authorization fixture'}).waitFor();await page.getByText('TEST-CODE',{exact:true}).waitFor();
 await page.request.post(state.coord,{headers:{Authorization:'Bearer fixture-coordinator'}});await page.getByText('@fixture-user',{exact:true}).waitFor();await page.getByText('Logged in',{exact:true}).waitFor();await popup.close();
 assert(await page.getByRole('button',{name:'Log in',exact:true}).count()===0,'Single account has only logout');assert(await page.getByRole('button',{name:'Log out',exact:true}).count()===1,'Exactly one logout');
 const catalog=await(await page.request.get(state.origin+'/api/v1/integrations')).json();assert(!JSON.stringify(catalog).includes('browser-fixture-secret'),'Token omitted from catalog');
 if(state.screenshots)await page.screenshot({path:state.screenshots+'/third-party-accounts-after.png'});
 const mobile=await page.context().newPage();await mobile.setViewportSize({width:393,height:852});await mobile.goto(state.origin+'/?theme=light&locale=en');await mobile.getByRole('button',{name:'Open navigation',exact:true}).click();await mobile.getByRole('button',{name:'Settings',exact:true}).click();await mobile.getByRole('button',{name:'Third-party accounts',exact:true}).click();await mobile.getByText('@fixture-user',{exact:true}).waitFor();assert(await mobile.getByRole('button',{name:'Log out',exact:true}).isVisible(),'Mobile logout remains reachable');assert(await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'No narrow viewport overflow');if(state.screenshots)await mobile.screenshot({path:state.screenshots+'/third-party-accounts-mobile-after.png'});await mobile.close();
 assert((await page.request.delete(state.origin+'/api/v1/integrations/github/connection',{headers:{Origin:'https://untrusted.invalid'},data:{expected_revision:catalog.items[0].account.revision}})).status()===403,'Cross-origin logout denied');
 await page.getByRole('button',{name:'Log out',exact:true}).click();await page.getByText('Not logged in',{exact:true}).waitFor();assert(await page.getByRole('button',{name:'Log in',exact:true}).isVisible(),'Logout restores login entry');
 assert(errors.length===0,errors.join('\n'));return {passed:true,real_integration_api:true,upstream_authorization_fixture:true,errors};
}
