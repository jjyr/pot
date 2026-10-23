// Real Usage page with synthetic date-scoped API projections. No account access.
const params=new URLSearchParams(location.search),baseline=params.has('baseline');
const prefix=baseline?'/output/pr-images/usage-date-ranges-baseline/web/src':'/web/src';
document.querySelector('#styles').href=prefix+'/styles.css';
const {createPages}=await import(prefix+'/components/pages.mjs');
const {usageToday,usageDateAdd,usageTrendBuckets}=await import('/web/src/components/usage-period.mjs');
const {usageNumbers}=await import('/web/src/components/usage.mjs');
let language=params.get('locale')||'zh-CN',scenario=params.get('case')||'complete';
document.documentElement.lang=language;document.documentElement.dataset.theme=params.get('theme')||'light';
document.body.classList.toggle('capture',params.has('capture'));
const today=usageToday(),known={input_known:true,output_known:true,cache_known:true},requests=[];
let holdNext=false,release=null,failCalendar=false;
function summary(start,end){
  const days=Math.round((new Date(end+'T12:00:00Z')-new Date(start+'T12:00:00Z'))/86400000)+1;
  const buckets=Array.from({length:days},(_,i)=>{
    const date=usageDateAdd(start,i),day=Number(date.slice(-2)),input=scenario==='empty'||date>today||day%7===0?0:(day+Number(date.slice(5,7)))*12000,output=input/4,cache=input*.75;
    return {date,input,output,cache,requests:input?1:0,agents:[{agent:'task',input:input*.7,output:output*.7,cache:cache*.7},{agent:'pot',input:input*.3,output:output*.3,cache:cache*.3}]};
  });
  const total=buckets.reduce((sum,row)=>({input:sum.input+row.input,output:sum.output+row.output,cache:sum.cache+row.cache,requests:sum.requests+row.requests}),{input:0,output:0,cache:0,requests:0});
  const portion=share=>({input:total.input*share,output:total.output*share,cache:total.cache*share,requests:total.requests});
  const compaction_totals=scenario==='empty'?[]:[{kind:'precompact',...known,...portion(.048)},{kind:'compact',...known,...portion(.052)}];
  if(scenario==='unknown')compaction_totals[0].cache_known=false;
  if(scenario==='partial'){compaction_totals[0].output_known=false;compaction_totals[0].output=0;}
  return {days,start,end,today,timezone:'Asia/Shanghai',total,lifetime:{input:999000000,output:1000000},routing_total:{},buckets,agents:[{agent:'task',...portion(.7)},{agent:'pot',...portion(.3)}],models:[{model:'fixture-model-a',...portion(.7)},{model:'fixture-model-b',...portion(.3)}],compaction_totals,quota:{policy:{period:'none'},active:false},waiting:null};
}
const client={production:true,desktop:false,spaces:[{id:'local',name:'Fixture',connected:true}],scope:()=>({space:'local',owner:'main'}),changes:()=>()=>{},request:async(path,options={})=>{
  const url=new URL(path,location.origin);
  if(url.pathname!=='/api/v1/usage/summary')return {items:[],providers:[]};
  const end=url.searchParams.get('end')||today,start=url.searchParams.get('start')||usageDateAdd(end,1-Number(url.searchParams.get('days')||365)),calendar=start.endsWith('-01-01')&&end.endsWith('-12-31');
  requests.push({start,end,scope:url.searchParams.get('scope'),owner:options.owner,calendar});
  if(calendar&&failCalendar)throw Error('Fixture calendar unavailable');
  if(!calendar&&holdNext){holdNext=false;await new Promise(resolve=>release=resolve);}
  return summary(start,end);
}};
const host=document.querySelector('#page'),result=document.querySelector('#result');
const pages=createPages({client,host,approvalHost:document.querySelector('#approvals'),chatApprovalHost:document.querySelector('#chat-approvals'),chatVisible:()=>false,locale:()=>language,notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});
function verify(){
  const checks=[],check=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
  const range=host.querySelector('.usage-range'),start=range.dataset.start,end=range.dataset.end,data=summary(start,end),numbers=usageNumbers(language),preset=host.querySelector('.usage-period-select').value;
  const calendar=host.querySelector('.usage-calendar'),trend=host.querySelector('.usage-trend'),distributions=host.querySelector('.usage-distributions'),chart=distributions.querySelector('[data-view-key="compactionShare"]');
  check(requests.some(row=>row.start===start&&row.end===end),'Page queries explicit inclusive date boundaries');
  check(host.querySelector('.usage-total .metric strong').textContent===numbers.compact(data.total.input+data.total.output),'Top totals follow selected dates even in conversation scope');
  check(trend.nextElementSibling===distributions&&(!calendar||distributions.nextElementSibling===calendar),'Date-scoped trend and all three distributions precede full-year overview');
  const expected=usageTrendBuckets(data,preset).buckets,columns=[...trend.querySelectorAll('.chart-column')],max=Math.max(1,...expected.map(row=>row.input+row.output));
  check(columns.length===expected.length&&columns.every((node,i)=>node.dataset.date===expected[i].date),'Trend retains selected dates and groups natural weeks/months');
  check(columns.every((node,i)=>Math.abs(parseFloat(node.querySelector('.chart-bar').style.height)-(expected[i].input+expected[i].output)/max*140)<.001),'Trend scales only the selected range');
  for(const key of ['categoryShare','modelShare'])check(distributions.querySelector(`[data-view-key="${key}"] .usage-share-heading .hint`).textContent.includes(numbers.compact(data.total.input+data.total.output)),'Distribution total matches selected range');
  check(chart.querySelectorAll('.usage-share-row').length===2,'Both compaction trigger classes remain visible');
  const pre=chart.querySelector('[data-view-key="precompact"]');
  if(scenario==='empty')check(chart.querySelectorAll('.usage-share-bar>span').length===0,'Empty data draws no false shares');
  else if(scenario==='unknown')check(pre.querySelector('.usage-compaction-cache').textContent.includes('—'),'Unknown cache reporting stays unknown');
  else if(scenario==='partial')check(pre.querySelector('.usage-share-value').textContent.startsWith('≥'),'Partial consumption remains a lower bound');
  else check(Math.abs(parseFloat(chart.querySelector('.usage-share-bar>span').style.width)-48)<.001,'Compaction bars measure token shares');
  if(calendar){
    const tiles=[...calendar.querySelectorAll('.usage-day')],year=tiles[0].dataset.date.slice(0,4);
    check(tiles[0].dataset.date===year+'-01-01'&&tiles.at(-1).dataset.date===year+'-12-31','Overview uses a natural calendar year');
    check(tiles.every(tile=>(tile.getAttribute('aria-pressed')==='true')===(tile.dataset.date>=start&&tile.dataset.date<=end)),'Calendar highlights exactly the selected dates');
  }
  check(host.scrollWidth<=document.documentElement.clientWidth,'Usage page fits the current viewport');
  check([...trend.querySelectorAll('.chart-column small')].filter(node=>node.textContent.trim()).every((node,i,labels)=>!i||node.getBoundingClientRect().left>=labels[i-1].getBoundingClientRect().right),'Date labels do not overlap');
  result.textContent='PASS\n'+checks.join('\n');return checks;
}
async function render(){
  if(params.has('conversation'))await pages.openUsage({space:'local',owner:'main',name:'Fixture conversation'},false);
  else await pages.show('usage',{force:true});
  if(!baseline)verify();
}
const localeSelect=document.querySelector('#fixture-locale'),themeSelect=document.querySelector('#fixture-theme'),caseSelect=document.querySelector('#fixture-case');
localeSelect.value=language;themeSelect.value=document.documentElement.dataset.theme;caseSelect.value=scenario;
localeSelect.onchange=async()=>{language=localeSelect.value;document.documentElement.lang=language;await render();};
themeSelect.onchange=()=>document.documentElement.dataset.theme=themeSelect.value;
caseSelect.onchange=async()=>{scenario=caseSelect.value;await render();};
window.usageFixture={verify,requests,hold(){holdNext=true;},release(){release?.();release=null;},failCalendar(value){failCalendar=value;}};
window.addEventListener('error',event=>{result.textContent='FAIL '+event.message;});
window.addEventListener('unhandledrejection',event=>{result.textContent='FAIL '+event.reason;});
window.addEventListener('pagehide',()=>pages.dispose());
await render();
