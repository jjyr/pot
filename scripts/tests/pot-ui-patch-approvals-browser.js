// Run with Playwright CLI against the repository's loopback fixture server.
async parentPage => {
  const page=await parentPage.context().newPage(),origin=new URL(parentPage.url()).origin;
  try {
    await page.goto(`${origin}/scripts/tests/fixtures/linked-approvals.html`);
    await page.evaluate(async()=>{
      const {createApprovalCard}=await import('/web/src/components/approval-card.mjs');
      const request={owner:'amber-otter',turn:'turn',call_id:'patch',machine_id:'local',tool:'apply_patch',args:{patch:'*** Begin Patch\n*** Update File: /tmp/other-assistant/AGENTS.md\n@@\n-old\n+requested rules\n*** End Patch',request_unsandboxed:true,justification:'Update the requested external file'}};
      const approval={id:'exact-patch',request},decisions=[];
      let locale='en',fail=true;
      const card=createApprovalCard({locale:()=>locale,machineLabel:()=> 'Local machine',onDecision:async(original,action)=>{if(fail)throw Error('Temporary decision failure');decisions.push({id:original.id,owner:original.request.owner,action});}});
      document.body.replaceChildren(card.element);card.update(approval);
      window.patchReview={decisions,language:value=>{locale=value;card.update(approval);},succeed:()=>{fail=false;}};
    });
    const card=page.locator('.approval-card');
    await card.locator('summary').click();
    if(await card.locator('.approval-actions button').count()!==2)throw Error('Patch offered a broad grant');
    await card.getByText('Update the requested external file',{exact:false}).first().waitFor();
    if(!(await card.locator('.approval-arguments').last().innerText()).includes('/tmp/other-assistant/AGENTS.md'))throw Error('Missing exact external patch');
    await card.getByRole('button',{name:'Allow once',exact:true}).click();
    await card.getByText('Temporary decision failure',{exact:true}).waitFor();
    if(await card.getByRole('button',{name:'Deny',exact:true}).isDisabled())throw Error('Failed review disabled rejection');
    await page.evaluate(()=>{window.patchReview.succeed();window.patchReview.language('zh-CN');});
    await card.getByRole('button',{name:'拒绝',exact:true}).click();
    await page.evaluate(()=>window.patchReview.language('en'));
    await card.getByRole('button',{name:'Allow once',exact:true}).click();
    const decisions=await page.evaluate(()=>window.patchReview.decisions);
    if(JSON.stringify(decisions)!==JSON.stringify([{id:'exact-patch',owner:'amber-otter',action:'deny'},{id:'exact-patch',owner:'amber-otter',action:'allow_once'}]))throw Error(JSON.stringify(decisions));
    return {checks:'exact external patch, one-time decisions, failure recovery, bilingual labels and original owner binding'};
  } finally { await page.close(); }
}
