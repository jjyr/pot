import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
import {spawnSync} from 'node:child_process';
import {groups,select,faults,injectFault} from './ui-regression-plan.mjs';
import {sourceServer} from './browser-harness.mjs';

test('behavior groups partition the full plan; invalid selections fail before launching',()=>{
  const all=select('all');assert.equal(all.length,new Set(all).size);
  for(const group of Object.keys(groups))assert.ok(select(group).length);
  const result=spawnSync(process.execPath,['scripts/tests/ui-regressions.mjs','--group','typo'],{encoding:'utf8'});
  assert.equal(result.status,1);assert.match(result.stderr,/Unknown group/);
  const listed=spawnSync(process.execPath,['scripts/tests/ui-regressions.mjs','--list'],{encoding:'utf8'});
  assert.equal(listed.status,0);assert.deepEqual(JSON.parse(listed.stdout).scenarios,all);
});
test('faults affect exactly one production source location and fail closed on stale probes',async()=>{
  for(const [fault,spec]of Object.entries(faults)){
    const source=await readFile('.'+spec.path,'utf8');
    assert.notEqual(injectFault(spec.path,source,fault),source);
    assert.equal(injectFault('/unrelated',source,fault),source);
    assert.throws(()=>injectFault(spec.path,'changed source',fault),/exactly once/);
  }
});
test('source fixture maps installed UI imports, forbids traversal and closes its listener',async()=>{
  const server=await sourceServer();
  try{
    const response=await fetch(server.origin+'/components/message-window.mjs');
    assert.equal(response.status,200);assert.match(await response.text(),/createMessageWindow/);
    assert.equal((await fetch(server.origin+'/..%2f..%2fetc/passwd')).status,404);
  }finally{await server.close();}
  await assert.rejects(fetch(server.origin));
});
