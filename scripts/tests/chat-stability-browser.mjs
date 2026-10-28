import {sourceServer,launchBrowser,contract} from './browser-harness.mjs';
const source=await sourceServer(),browser=await launchBrowser();
try{
 const page=await browser.newPage();
 console.log(JSON.stringify(await contract('./background-tasks-regression.mjs',page,{origin:source.origin})));
}finally{await browser.close();await source.close();}
