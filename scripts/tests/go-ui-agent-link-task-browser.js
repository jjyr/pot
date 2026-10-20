async page=>{
 const assert=(value,message)=>{if(!value)throw Error(message);},errors=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto(new URL('/scripts/tests/fixtures/agent-link-task.html',page.url()).href);
 await page.getByText('This task runs on Codex. Updates and results appear here.',{exact:true}).waitFor();
 assert(!await page.locator('.task-composer-area form').isVisible(),'Linked tasks do not offer unsupported live steering');
 await page.evaluate(()=>linkedTaskFixture.ui.openMenu());
 assert(!await page.getByRole('button',{name:'Pause task',exact:true}).count(),'Pause is not advertised by a linked task');
 assert(!await page.getByRole('button',{name:'Usage',exact:true}).count(),'Local model usage is not offered for the other node');
 await page.getByRole('button',{name:'Cancel task',exact:true}).click();await page.getByRole('button',{name:'Cancel task',exact:true}).click();
 assert(await page.evaluate(()=>linkedTaskFixture.calls.join(','))==='cancel','Cancellation is routed through the supported task action');
 await page.evaluate(()=>{linkedTaskFixture.record.info.phase='failed';linkedTaskFixture.record.error='codex_approval_requires_local_review';linkedTaskFixture.update();});
 await page.getByText('Codex needs approval. Review this action in its Codex session.',{exact:true}).waitFor();
 await page.evaluate(()=>linkedTaskFixture.ui.openMenu());await page.getByRole('button',{name:'Archive task',exact:true}).click();
 assert(await page.evaluate(()=>linkedTaskFixture.calls.join(','))==='cancel,archive','Archive remains a supported local disposition');
 await page.evaluate(()=>{linkedTaskFixture.language='zh-CN';linkedTaskFixture.update();});
 await page.getByText('Codex 需要审批，请在对应的 Codex 会话中查看并处理。',{exact:true}).waitFor();
 assert(await page.locator('.composer-input').inputValue()==='Retained earlier draft','Capability changes preserve any retained draft');
 assert(!errors.length,'No browser errors: '+errors.join('; '));
 return {passed:true,supportedCancelArchive:true,noLocalExecutionControls:true,localizedApproval:true,preservedDraft:true};
}
