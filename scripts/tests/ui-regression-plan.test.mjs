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
    assert.equal((await fetch(server.origin+'/scripts/tests/..%2f..%2f.env')).status,404);
  }finally{await server.close();}
  await assert.rejects(fetch(server.origin));
});

test('standard smoke retains build/authentication first and stops on the first failing stage',()=>{
  const listed=spawnSync('python',['scripts/tests/ui-http-suite.py','--list'],{encoding:'utf8'});
  assert.equal(listed.status,0);assert.deepEqual(JSON.parse(listed.stdout),[['http','pot-ui-http-integration.py'],['source','ui-regressions.mjs']]);
  const probe=spawnSync('python',['-c',`import importlib.util,io,json
from contextlib import redirect_stdout
from unittest.mock import patch
from types import SimpleNamespace
s=importlib.util.spec_from_file_location('suite','scripts/tests/ui-http-suite.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
for codes,expected in [([0,0],0),([7],7)]:
 out=io.StringIO()
 with patch.object(m,'browser_environment',return_value={}),patch.object(m.subprocess,'run',side_effect=[SimpleNamespace(returncode=c) for c in codes]) as run,redirect_stdout(out):
  actual=m.run_suite()
 assert actual==expected and run.call_count==len(codes)
 summary=json.loads(out.getvalue().splitlines()[-1]);assert summary['status']==('passed' if not expected else 'incomplete')
`],{encoding:'utf8'});
  assert.equal(probe.status,0,probe.stderr);
});
