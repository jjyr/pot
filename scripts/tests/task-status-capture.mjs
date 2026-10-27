// Serve the worktree and an archived base at output/task-status-before first.
// The same records, clock, locale and viewport are used for each screenshot pair.
import {launchBrowser} from './browser-harness.mjs';
import {taskStatusBrowser} from './task-status-browser.mjs';
const [sourceOrigin='http://127.0.0.1:18892',output='output/pr-images/task-status']=process.argv.slice(2);
const browser=await launchBrowser();
try{console.log(JSON.stringify(await taskStatusBrowser(browser,{sourceOrigin,output}),null,2));}
finally{await browser.close();}
