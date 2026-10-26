// Browser lifecycle contract with a real child/CDP pipe; no website action retries.
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { mkdtemp, writeFile, readFile, readdir, rm, access } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createInterface } from 'node:readline';
import { browserAvailability } from '../../plugins/computer-use/browser.mjs';
import { createPlugin } from '../../plugins/computer-use/index.mjs';

const originalSpawn=childProcess.spawn,children=[];
childProcess.spawn=(...args)=>{const child=originalSpawn(...args);children.push(child);return child;};
syncBuiltinESMExports();
const root = await mkdtemp(join(tmpdir(), 'pot-browser-close-test-'));
const previous = {TMPDIR: process.env.TMPDIR, POT_BROWSER_EXECUTABLE: process.env.POT_BROWSER_EXECUTABLE, POT_BROWSER_TEST_TRACE: process.env.POT_BROWSER_TEST_TRACE, POT_BROWSER_TEST_MODE: process.env.POT_BROWSER_TEST_MODE};
const executable = join(root, 'fake-chrome');
await writeFile(executable, `#!${process.execPath}
const fs = require('node:fs');
const profile=process.argv.find(value=>value.startsWith('--user-data-dir=')).split('=').slice(1).join('=');
const trace=process.env.POT_BROWSER_TEST_TRACE;
fs.appendFileSync(trace, JSON.stringify({event:'spawn',pid:process.pid,profile})+'\\n');
const Socket=require('node:net').Socket;const input=new Socket({fd:3,readable:true,writable:false}),output=new Socket({fd:4,readable:false,writable:true});let buffer='';
const writer=setInterval(()=>fs.writeFileSync(profile+'/last-write',String(Date.now())),10);
input.on('data',chunk=>{buffer+=chunk.toString();let end;while((end=buffer.indexOf('\\0'))!==-1){const request=JSON.parse(buffer.slice(0,end));buffer=buffer.slice(end+1);let result={};
fs.appendFileSync(trace,JSON.stringify({event:'command',method:request.method,params:request.params})+'\\n');
if(request.method==='Browser.close'){fs.appendFileSync(trace,JSON.stringify({event:'Browser.close'})+'\\n');if(process.env.POT_BROWSER_TEST_MODE==='hang')continue;clearInterval(writer);output.write(JSON.stringify({id:request.id,result:{}})+'\\0',()=>process.exit(0));continue;}
if(request.method==='Target.createTarget')result={targetId:'target'};
if(request.method==='Target.attachToTarget')result={sessionId:'session'};
if(request.method==='Accessibility.getFullAXTree')result={nodes:[{backendDOMNodeId:1,role:{value:'textbox'},name:{value:'Fixture input'}}]};
if(request.method==='DOM.resolveNode')result={object:{objectId:'fixture-input'}};
if(request.method==='Runtime.evaluate')result={result:{value:JSON.stringify({url:'about:blank',title:'Lifecycle fixture',canvas:0})}};
output.write(JSON.stringify({id:request.id,result})+'\\0');}});
`, {mode: 0o700});

async function argumentContract() {
  const trace = join(root, 'trace-contract');
  process.env.TMPDIR = root; process.env.POT_BROWSER_EXECUTABLE = executable;
  process.env.POT_BROWSER_TEST_TRACE = trace; process.env.POT_BROWSER_TEST_MODE = 'graceful';
  // Use the actual plugin transport so errors include the uncertain-effects warning.
  const host = childProcess.spawn(process.execPath, ['--input-type=module', '-e',
    await readFile('pkg/pluginhost/assets/plugin-host.mjs', 'utf8'), resolve('plugins/computer-use/index.mjs')], {stdio:['pipe','pipe','inherit']});
  const exited = new Promise((resolve, reject) => { host.once('exit', (code, signal) => code === 0 ? resolve() : reject(new Error(`Plugin host exited: ${code}, ${signal}`))); });
  const lines = createInterface({input:host.stdout})[Symbol.asyncIterator]();
  const execute = async code => {
    host.stdin.write(JSON.stringify({mode:'text',tool:'cua_repl',input:{code}})+'\n');
    const line = await lines.next();
    assert(!line.done, 'Plugin host must return a response');
    return JSON.parse(line.value);
  };
  const commands = async () => (await readFile(trace,'utf8')).trim().split('\n').map(JSON.parse).filter(item=>item.event==='command');
  const expectFailure = async (code, message) => {
    const before = await commands();
    const result = await execute(code);
    assert.equal(result.isError, true);
    assert.equal(result.error, `${message}; prior effects may have occurred`);
    assert(!('execution_status' in result), 'Argument rejection must not claim no prior effects');
    return (await commands()).slice(before.length);
  };
  try {
    const first = await execute();
    const docs = first.content[0].text;
    assert.match(docs, /tab.scroll: delta_y must be an integer number in \[-2000, 2000\] CSS pixels; positive scrolls down, negative scrolls up/);
    assert.match(docs, /tab.press: key must be one of these exact strings: Enter, Tab, Escape, Backspace, ArrowDown, ArrowUp\./);
    assert.match(docs, /click, fill, press and scroll require the latest sequence; element actions also require an observed element ID/);
    assert.match(docs, /JS errors preserve the REPL and any completed page effects/);
    assert(!(await readdir(root)).some(name=>name.startsWith('pot-browser-')), 'Documentation must not start a browser');
    assert(!(await execute('let tab = await cua.open(); let state = await tab.observe();')).isError);
    for (const delta of [-2000, -1, 0, 1, 2000]) {
      const before = (await commands()).length;
      assert(!(await execute(`state = await tab.scroll({sequence:state.sequence,delta_y:${delta}});`)).isError);
      const dispatched = (await commands()).slice(before).filter(item=>item.method==='Input.dispatchMouseEvent');
      assert.deepEqual(dispatched.map(item=>item.params), [{type:'mouseWheel',x:640,y:400,deltaX:0,deltaY:delta}]);
    }
    for (const value of ['-2001', '2001', '-5000', '-100000', '0.5', '-0.5', 'NaN', 'Infinity', '-Infinity', '"500"', 'null', 'undefined', 'true', '{}', '[]', '2000n', 'new Number(500)', 'Symbol("500")']) {
      const sent = await expectFailure(`await tab.scroll({sequence:state.sequence,delta_y:${value}});`, 'Invalid scroll distance; delta_y must be an integer number in [-2000, 2000] CSS pixels');
      assert.deepEqual(sent, [], `Invalid delta_y ${value} must not reach CDP`);
    }
    const partial = await expectFailure('state = await tab.scroll({sequence:state.sequence,delta_y:1}); await tab.scroll({sequence:state.sequence,delta_y:2001});', 'Invalid scroll distance; delta_y must be an integer number in [-2000, 2000] CSS pixels');
    assert.equal(partial.filter(item=>item.method==='Input.dispatchMouseEvent').length, 1, 'A later rejection does not roll back or repeat an earlier action');
    for (const [key, keyCode] of [['Enter',13],['Tab',9],['Escape',27],['Backspace',8],['ArrowDown',40],['ArrowUp',38]]) {
      const before = (await commands()).length;
      assert(!(await execute(`state = await tab.press({element:"1",sequence:state.sequence,key:${JSON.stringify(key)}});`)).isError);
      const dispatched = (await commands()).slice(before).filter(item=>item.method==='Input.dispatchKeyEvent');
      assert.deepEqual(dispatched.map(item=>item.params), [
        {type:'keyDown',key,code:key,windowsVirtualKeyCode:keyCode,...(key==='Enter'?{text:'\r'}:{})},
        {type:'keyUp',key,code:key,windowsVirtualKeyCode:keyCode},
      ]);
    }
    for (const value of ['"Home"', '"Control+Home"', '"enter"', '""', '"toString"', '"__proto__"', 'null', 'undefined', '13', 'true', '[]', '["Enter"]', '{}', 'new String("Enter")', 'Symbol("Enter")']) {
      const sent = await expectFailure(`await tab.press({element:"1",sequence:state.sequence,key:${value}});`, 'Unsupported press key; key must be one of: Enter, Tab, Escape, Backspace, ArrowDown, ArrowUp');
      assert(sent.some(item=>item.method==='DOM.focus'), 'Key validation retains the existing focus-before-rejection behavior');
      assert(!sent.some(item=>item.method==='Input.dispatchKeyEvent'), 'Unsupported key must not dispatch a key event');
    }
    for (const action of ['scroll({sequence:state.sequence-1,delta_y:1})', 'press({element:"1",sequence:state.sequence-1,key:"Enter"})', 'click({element:"1",sequence:state.sequence-1})', 'fill({element:"1",sequence:state.sequence-1,text:"x"})']) {
      assert.deepEqual(await expectFailure(`await tab.${action};`, 'Use the latest observation sequence'), []);
    }
    assert.deepEqual(await expectFailure('await tab.press({sequence:state.sequence,key:"Enter"});', 'Observed element required'), []);
    const sent = await expectFailure('await tab.press({element:"missing",sequence:state.sequence,key:"Enter"});', 'Element unavailable; observe again');
    assert(!sent.some(item=>item.method==='Input.dispatchKeyEvent'));
    // Recovery uses a fresh observation in the same retained REPL; no action replay.
    assert(!(await execute('state = await tab.observe(); state = await tab.press({element:"1",sequence:state.sequence,key:"Enter"}); await tab.close();')).isError);
  } finally {
    host.stdin.end();
    await exited;
  }
  console.log('Browser argument contract: returned docs, scroll bounds/types, supported/unknown keys, CDP dispatch, fresh observations and prior-effects warning passed');
}

async function scenario(mode, round) {
  const trace = join(root, `trace-${mode}-${round}`);
  process.env.TMPDIR = root; process.env.POT_BROWSER_EXECUTABLE = executable;
  process.env.POT_BROWSER_TEST_TRACE = trace; process.env.POT_BROWSER_TEST_MODE = mode;
  const backend = await import(pathToFileURL(resolve('plugins/computer-use/browser.mjs')).href + `?mode=${mode}&round=${round}`);
  const opened = await backend.operation({action:'open',execution_target:'host'}),originalChild=children.at(-1);
  assert.equal(JSON.parse(opened.content[0].text).title,'Lifecycle fixture');
  for (const action of ['scroll', 'press']) {
    await assert.rejects(backend.operation({action,session_id:opened.session_id,sequence:opened.sequence-1,delta_y:1,element:'1',key:'Enter'}), /Stale observation sequence; observe before acting/);
  }
  const first = JSON.parse((await readFile(trace,'utf8')).split('\n')[0]);
  const start = Date.now();
  if (mode === 'crash') {
    process.kill(first.pid,'SIGKILL');
    await new Promise(resolve=>setTimeout(resolve,40));
  }
  if (mode !== 'graceful') {
    const outcomes = await Promise.allSettled([backend.close(),backend.close(),backend.close()]);
    assert(outcomes.every(value=>value.status==='rejected' && /cleanup unconfirmed.*profile retained/.test(value.reason.message)));
    await access(first.profile); // Only authoritative outer containment may remove it.
    assert.throws(()=>process.kill(first.pid,0),{code:'ESRCH'});
    await assert.rejects(backend.close(),/cleanup unconfirmed/);
    await assert.rejects(backend.operation({action:'open'}),/cleanup must complete|already open/);
  } else {
    await Promise.all([backend.close(),backend.close(),backend.close()]);
    await backend.close();
    await assert.rejects(access(first.profile),{code:'ENOENT'});
    assert.throws(()=>process.kill(first.pid,0),{code:'ESRCH'});
    // A new browser in the same backend gets a fresh CDP state and can close too.
    const reopened=await backend.operation({action:'open',execution_target:'host'});
    const observation=backend.operation({action:'observe',session_id:reopened.session_id});
    originalChild.stdio[3].emit('error',new Error('Late old pipe error'));
    originalChild.emit('error',new Error('Late old process error'));
    assert.equal(JSON.parse((await observation).content[0].text).title,'Lifecycle fixture','Late old errors cannot invalidate the new browser');
    await backend.close();
  }
  assert(Date.now()-start<4500,'Shutdown must fit inside the outer five-second dispose budget');
  const events=(await readFile(trace,'utf8')).trim().split('\n').map(JSON.parse);
  assert.equal(events.filter(value=>value.event==='Browser.close').length,mode==='hang'?1:mode==='crash'?0:2,'Concurrent close sends one CDP close per browser');
}
try {
  for (const candidate of ['missing-chrome', 'not-executable']) {
    process.env.TMPDIR = root;
    process.env.POT_BROWSER_EXECUTABLE = join(root, candidate);
    if (candidate === 'not-executable') await writeFile(process.env.POT_BROWSER_EXECUTABLE, 'not executable', {mode:0o600});
    assert.equal(browserAvailability().available, false);
    const content = [], count = children.length;
    const plugin = createPlugin({mode:'text',emit:item=>content.push(item)});
    const failed = await plugin.execute({tool:'cua_repl',input:{code:"let forbiddenEffect = 1; nodeRepl.write('UNEXPECTED');"}});
    assert.equal(failed.code, 'browser_unavailable');
    assert.equal(failed.execution_status, 'not_started');
    assert.equal(failed.isError, true);
    assert.match(failed.error, /No browser operation or supplied JavaScript was started/);
    assert.equal(children.length, count, 'Missing browser must not launch a process');
    assert.deepEqual(content, [], 'Unavailable browser must not execute supplied code or advertise readiness');
    assert(!(await readdir(root)).some(name=>name.startsWith('pot-browser-')), 'Availability check must not create browser profiles');
    process.env.POT_BROWSER_EXECUTABLE = executable;
    await plugin.execute({tool:'cua_repl',input:{code:'nodeRepl.write(typeof forbiddenEffect);'}});
    assert.equal(content.at(-1).text, 'undefined', 'Rejected JavaScript must not change retained state');
    await assert.rejects(plugin.execute({tool:'cua_repl',input:{code:"throw Object.assign(new Error('ordinary error'), {code:'browser_unavailable',execution_status:'not_started'});"}}), /ordinary error/);
    await plugin.dispose();
  }
  await argumentContract();
  for(let round=0;round<3;round++)await scenario('graceful',round);
  await scenario('hang',0);
  await scenario('crash',0);
  assert((await readdir(root)).filter(name=>name.startsWith('pot-browser-')).length===2,'Forced and unexpected exits retain profiles for authoritative cleanup');
  console.log('Browser lifecycle: graceful/repeated/concurrent close, bounded forced/unexpected exits and retained uncertain cleanup passed');
} catch(error) {
  for(const name of await readdir(root)){if(name.startsWith('trace-'))console.error(name,await readFile(join(root,name),'utf8'));}
  throw error;
} finally {
  childProcess.spawn=originalSpawn;syncBuiltinESMExports();
  for(const [name,value]of Object.entries(previous)){if(value===undefined)delete process.env[name];else process.env[name]=value;}
  await rm(root,{recursive:true,force:true});
}
