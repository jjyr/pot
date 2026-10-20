async page=>{
 const assert=(value,message)=>{if(!value)throw Error(message);},errors=[];page.on('pageerror',error=>errors.push(error.message));
 await page.setViewportSize({width:1280,height:900});await page.goto(new URL('/scripts/tests/fixtures/agent-link-chat.html?locale=en',page.url()).href);
 await page.locator('.assistant-row').filter({hasText:'Code review'}).waitFor();
 assert(await page.locator('.assistant-row .agent-source[aria-label="Codex"]').count()===1,'Codex source appears inside the existing assistant row');
 assert(await page.locator('.assistant-row .agent-source[aria-label="Cloud Pot"]').count()===1,'Cloud Pot has a distinct source icon');
 await page.locator('.background-task .agent-source[aria-label="Codex"]').waitFor();
 await page.screenshot({path:'docs/pr-images/codex-agent-link-ui/chat-delegation-desktop-after.png'});
 await page.locator('.assistant-row').filter({hasText:'Code review'}).click();
 await page.locator('.titlebar .agent-source[aria-label="Codex"]').waitFor();
 assert(await page.getByRole('button',{name:'Assistant settings',exact:true}).count()===0,'Linked assistants do not expose unsupported local model settings');
 await page.screenshot({path:'docs/pr-images/codex-agent-link-ui/chat-source-desktop-after.png'});
 await page.locator('#pot-composer').fill('Keep this draft');
 await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value:'zh-CN'}})));
 assert(await page.locator('#pot-composer').inputValue()==='Keep this draft','Source localization preserves the existing composer draft');
 await page.setViewportSize({width:393,height:852});
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Linked chat retains the narrow chat layout');
 assert(!errors.length,'No browser errors: '+errors.join('; '));
 return {passed:true,codexSource:true,cloudSource:true,taskSource:true,existingComposer:true,mobileWidth:393};
}
