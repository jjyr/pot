import assert from 'node:assert/strict';
import {usageCalendarWeeks,usageModelColor,usagePercent,usageCompactionRows,compactionTokens} from '../../web/src/components/usage-calendar.mjs';
import {usageNumbers,usageRows} from '../../web/src/components/usage.mjs';
import {combineUsage} from '../../web/src/state/aggregate.mjs';
import {usageRange,usageShiftRange,usageTrendBuckets} from '../../web/src/components/usage-period.mjs';

assert.deepEqual(usageRange('month','2024-02-29'),{preset:'month',start:'2024-02-01',end:'2024-02-29'});
assert.deepEqual(usageShiftRange(usageRange('month','2024-03-31'),-1),usageRange('month','2024-02-15'),'Moving from March uses a whole natural February');
assert.deepEqual(usageRange(),usageRange('month'),'First entry defaults to the current calendar month');
for(const removed of ['fortnight','week','all','7','14','',null,'unknown'])assert.deepEqual(usageRange(removed,'2024-02-29'),usageRange('month','2024-02-29'),'Removed or invalid presets fall back to one month');
assert.deepEqual(usageRange('halfyear','2024-02-29'),{preset:'halfyear',start:'2024-01-01',end:'2024-06-30'});
assert.deepEqual(usageRange('halfyear','2024-06-30'),usageRange('halfyear','2024-02-29'));
assert.deepEqual(usageRange('halfyear','2024-07-01'),{preset:'halfyear',start:'2024-07-01',end:'2024-12-31'});
assert.deepEqual(usageShiftRange(usageRange('halfyear','2024-07-31'),-1),usageRange('halfyear','2024-02-29'));
assert.deepEqual(usageShiftRange(usageRange('halfyear','2024-01-31'),-1),usageRange('halfyear','2023-12-31'),'Half-year navigation crosses years without month-end overflow');
assert.deepEqual(usageRange('quarter','2024-02-29'),{preset:'quarter',start:'2024-01-01',end:'2024-03-31'});
assert.deepEqual(usageShiftRange(usageRange('quarter','2026-01-01'),-1),usageRange('quarter','2025-12-31'),'Quarter navigation crosses year boundaries');
assert.deepEqual(usageShiftRange(usageRange('year','2024-02-29'),1),usageRange('year','2025-01-01'));
for(const preset of ['month','quarter','halfyear','year']){
  const range=usageRange(preset,'2024-03-01'),previous=usageShiftRange(range,-1);
  assert.equal((new Date(range.start)-new Date(previous.end))/86400000,1,'Previous period is adjacent without duplicating a date');
  assert.deepEqual(usageShiftRange(previous,1),range,'Next period returns to the same natural dates');
}
const halfyearDays=Array.from({length:182},(_,i)=>({date:new Date(Date.UTC(2024,0,1+i)).toISOString().slice(0,10),input:100}));
assert.equal(usageTrendBuckets({buckets:halfyearDays},'halfyear').buckets.length,6,'Leap half-year retains all six calendar months');
const daily=Array.from({length:91},(_,i)=>({date:new Date(Date.UTC(2024,0,1+i)).toISOString().slice(0,10),input:100,output:20,cache:75}));
const weekly=usageTrendBuckets({buckets:daily},'quarter');
assert.equal(weekly.kind,'week');assert.equal(weekly.buckets.length,13);assert.equal(weekly.buckets[0].date,'2024-01-01');assert.equal(weekly.buckets[0].end,'2024-01-07');assert.equal(weekly.buckets[0].cache,525);
const monthly=usageTrendBuckets({buckets:daily},'year');
assert.deepEqual(monthly.buckets.map(row=>[row.date,row.end,row.input]),[['2024-01-01','2024-01-31',3100],['2024-02-01','2024-02-29',2900],['2024-03-01','2024-03-31',3100]]);
assert.equal(monthly.buckets.reduce((sum,row)=>sum+row.input+row.output,0),10920,'Grouping preserves totals without duplicating cache or boundary dates');

for(let weekday=0;weekday<7;weekday++){
  const buckets=Array.from({length:365},(_,i)=>({date:new Date(Date.UTC(2024,1,25+weekday+i)).toISOString().slice(0,10),input:i}));
  const weeks=usageCalendarWeeks(buckets);
  assert.equal(weeks.length,53);
  assert.deepEqual(weeks.flat().filter(Boolean),buckets,'Calendar retains every date, including leap day and empty usage');
  assert.equal(weeks[0].filter(value=>!value).length,new Date(buckets[0].date+'T12:00:00Z').getUTCDay());
  assert.ok(weeks.every(week=>week.length===7));
}
assert.deepEqual(usageCalendarWeeks([]),[]);
const color=usageModelColor('provider/model-a');
assert.equal(usageModelColor('provider/model-a'),color);
for(const name of ['provider/model-b','other/model-a','provider/<model-a>'])assert.notEqual(usageModelColor(name),color);
assert.equal(usagePercent(0,0,'en'),'0%');
assert.equal(usagePercent(1,4,'en'),'25%');
assert.equal(usagePercent(1,100000,'en'),'<0.01%');
assert.equal(usagePercent(1,100000,'zh-CN'),'＜0.01%');
assert.equal(usagePercent(25000,208025000,'en'),'0.01%','Small correspondence shares are not rounded to zero');

const row=(agent,input,output=0)=>({agent,input,output,cache:0,requests:1});
const a={days:365,timezone:'Asia/Shanghai',total:row('',100),buckets:[{date:'2026-10-18',input:100,agents:[row('task',100)]},{date:'2026-10-19',input:0,agents:[]}],agents:[row('task',100)],models:[{model:'shared',input:100}]};
const b={days:365,timezone:'Asia/Shanghai',total:row('',200,20),buckets:[{date:'2026-10-19',input:200,output:20,agents:[row('task',120,10),row('pot',80,10)]}],agents:[row('task',120,10),row('pot',80,10)],models:[{model:'shared',input:120,output:10},{model:'other',input:80,output:10}]};
const combined=combineUsage([{space:{id:'local'},value:a},{space:{id:'remote'},value:b,stale:true}]);
assert.equal(combined.total.input+combined.total.output,320);
assert.deepEqual(combined.buckets.map(row=>row.date),['2026-10-18','2026-10-19']);
assert.equal(combined.buckets[1].agents[0].input,120);
assert.equal(combined.agents.find(row=>row.agent==='task').input,220);
assert.equal(combined.models.find(row=>row.model==='shared').input,220,'The same model is merged across Spaces and categories');
assert.equal(combined.models.reduce((n,row)=>n+row.input+(row.output||0),0),320,'Complete model shares reconcile with annual totals');
assert.equal(combined.spaces[1].stale,true);
assert.equal(usageModelColor(combined.models[0].model),usageModelColor('shared'),'Aggregation order never affects model colors');
const compactRow=(input,cache,known=true)=>({kind:'precompact',input,output:10,cache,requests:1,input_known:true,output_known:true,cache_known:known});
const compacted=combineUsage([{space:{id:'a'},value:{compaction_totals:[compactRow(100,90)]}},{space:{id:'b'},value:{compaction_totals:[compactRow(900,90)]}}]);
const precompact=compacted.compaction_totals[0];
assert.equal(usageNumbers('en').hitRate(precompact),'18%','Cache rate weights by input tokens across Spaces, never averaging request percentages');
assert.equal(precompact.requests,2);
assert.equal(compacted.total.input,0,'Compaction subset is never added to ordinary totals');
const unknown=combineUsage([{value:{compaction_totals:[compactRow(100,90)]}},{value:{compaction_totals:[compactRow(100,0,false)]}}]).compaction_totals[0];
assert.equal(usageNumbers('en').hitRate(unknown),'—','Unknown cache reporting must not become a zero hit rate');
assert.equal(usageNumbers('en').uncached(unknown),null);
assert.equal(usageRows(unknown,'en').find(([label])=>label==='Cached input')[1],'—');
assert.equal(usageNumbers('en').hitRate(compactRow(100,0)),'0%','Reported zero is distinct from missing cache usage');
assert.equal(compactionTokens({...precompact,output_known:false},'en'),'≥ 1.0K');
assert.equal(compactionTokens({...precompact,input:0,output:0,input_known:false},'en'),'—');
assert.deepEqual(usageCompactionRows([precompact]).map(row=>row.kind),['precompact','compact']);
assert.equal(usageCompactionRows([]).reduce((sum,row)=>sum+row.input+row.output,0),0);
console.log('Usage calendar dates, stable colors, small shares and cross-Space aggregation passed');
