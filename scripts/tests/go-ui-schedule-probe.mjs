import assert from 'node:assert/strict';
import {beforeRunDraft} from '../../web/src/components/schedule-editor.mjs';
assert.equal(beforeRunDraft({before_run_command:''},'en'),null);
assert.deepEqual(beforeRunDraft({before_run_command:'["python3", "check.py", "$(literal)"]',before_run_cwd:'project',before_run_timeout:30},'en'),{command:['python3','check.py','$(literal)'],cwd:'project',timeout_seconds:30});
for(const command of ['null','{}','[]','[""]','[1]','["python3",null]','invalid','["sh","\\u0000"]'])assert.throws(()=>beforeRunDraft({before_run_command:command,before_run_timeout:30},'en'),error=>error.uiKey==='schedule.hookInvalid');
for(const timeout of [0,-1,301,1.5])assert.throws(()=>beforeRunDraft({before_run_command:'["check"]',before_run_timeout:timeout},'zh-CN'),error=>error.uiKey==='schedule.hookInvalid'&&error.message.includes('请输入'));
console.log('Schedule probe argv, removal, bounds and localized errors passed');
