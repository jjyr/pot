// Clipboard denial and out-of-order status responses use disposable fixture data.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);};
 const cdp=await page.context().newCDPSession(page);await cdp.send('Network.setCacheDisabled',{cacheDisabled:true});
 await page.setViewportSize({width:1024,height:700});await page.goto(new URL('/scripts/tests/fixtures/topology.html',page.url()).href);
 await page.getByRole('button',{name:'Add machine',exact:true}).click();
 await page.evaluate(()=>topologyFixture.clipboardFail=true);await page.getByRole('button',{name:'Copy join information',exact:true}).click();
 await page.getByText('Could not copy. Allow clipboard access and try again; the same invitation will be reused.',{exact:true}).waitFor();
 await page.evaluate(()=>topologyFixture.clipboardFail=false);await page.getByRole('button',{name:'Copy join information',exact:true}).click();
 await page.getByText('Waiting for the machine to join…',{exact:true}).waitFor();
 assert(await page.evaluate(()=>topologyFixture.calls.filter(call=>call.body?.action==='create_invitation').length)===1,'Clipboard retry reuses the invitation');
 await page.getByRole('button',{name:'Revoke invitation',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'Revoke invitation',exact:true}).click();
 await page.evaluate(()=>{
  const request=topologyFixture.client.request;
  topologyFixture.client.request=async(path,options={})=>{
   const action=options.body?.action;
   if(action==='invitation_status'&&topologyFixture.holdStatus){topologyFixture.holdStatus=false;return await new Promise(resolve=>topologyFixture.releaseStatus=resolve);}
   const result=await request(path,options);
   if(action==='replace_invitation'&&!topologyFixture.shortInvitationMade){topologyFixture.shortInvitationMade=true;topologyFixture.holdStatus=true;return {...result,expires_at_ms:Date.now()+250};}
   return result;
  };
 });
 await page.getByRole('button',{name:'Generate again and copy',exact:true}).click();await page.waitForFunction(()=>!!topologyFixture.releaseStatus);
 await page.getByRole('button',{name:'Copied · copy again',exact:true}).click();
 await page.waitForFunction(()=>topologyFixture.calls.filter(call=>call.body?.action==='replace_invitation').length===2);
 await page.evaluate(()=>topologyFixture.releaseStatus({state:'expired'}));
 await page.getByText('Waiting for the machine to join…',{exact:true}).waitFor();
 assert(await page.getByRole('button',{name:'Copied · copy again',exact:true}).isEnabled(),'Old expiry cannot mark a newly copied invitation expired');
 await page.getByRole('button',{name:'Return to topology',exact:true}).click();
 return {passed:true,clipboardRetryReusesInvitation:true,explicitRevoke:true,lateStatusFence:true};
}
