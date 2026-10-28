import {preparedIndex} from './prepared-index-browser.mjs';
import {preparedFocusedPins} from './prepared-focus-pins-browser.mjs';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {sourceServer,launchBrowser,isolatedPage,contract} from './browser-harness.mjs';

const server=await sourceServer(),browser=await launchBrowser(),results=[];
try{
 for(const native of [true,false])results.push(await isolatedPage(browser,server.origin,async page=>{
  if(!native)await page.route('**/components/message-window.mjs',async route=>{
   const source=await readFile('web/src/components/message-window.mjs','utf8'),probe="supportsScrollend='onscrollend' in view";
   assert(source.includes(probe),'Fallback injection must still select the product capability branch');
   await route.fulfill({contentType:'text/javascript',body:source.replace(probe,'supportsScrollend=false')});
  });
  const result=await contract('pot-ui-conversation-controller-browser.js',page,{origin:server.origin});
  return {nativeScrollend:native,capability:await page.evaluate(()=>({available:'onscrollend' in window,userAgent:navigator.userAgent})),...result};
 }));
 const index=await preparedIndex(browser,server.origin),focusedPins=await preparedFocusedPins(browser,server.origin);console.log(JSON.stringify({passed:true,results,index,focusedPins}));
}finally{await browser.close();await server.close();}
