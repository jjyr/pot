import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
export async function preparedIndex(browser,origin){
 const output=resolve(process.env.POT_INDEX_OUTPUT||'.pot-artifacts/prepared-index');await mkdir(output,{recursive:true});const results=[];
 for(const phone of [false,true]){
  const context=await browser.newContext({viewport:phone?{width:390,height:852}:{width:1280,height:800},isMobile:phone,hasTouch:phone}),page=await context.newPage();
  try{await page.goto(origin+'/scripts/tests/fixtures/prepared-index.html');await page.locator('#run').click();await page.waitForFunction(()=>!!window.preparedIndexResult,{},{timeout:20000});const result=await page.evaluate(()=>preparedIndexResult);await writeFile(output+'/'+(phone?'phone':'desktop')+'.json',JSON.stringify(result,null,2));assert.equal(result.failure,undefined);results.push({phone,passed:true,maxCandidates:result.maxCandidates});}
  finally{await context.close();}
 }
 return results;
}
