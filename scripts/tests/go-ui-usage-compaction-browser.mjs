// Real Usage page with synthetic API projections. No model or account access.
const params=new URLSearchParams(location.search),baseline=params.has('baseline');
const prefix=baseline?'/output/compaction-baseline/web/src':'/web/src';
document.querySelector('#styles').href=prefix+'/styles.css';
const {createPages}=await import(prefix+'/components/pages.mjs');
let language=params.get('locale')||'zh-CN',scenario=params.get('case')||'complete';
document.documentElement.lang=language;document.documentElement.dataset.theme=params.get('theme')||'light';
document.body.classList.toggle('capture',params.has('capture'));
const known={input_known:true,output_known:true,cache_known:true};
function summary(){
  const empty=scenario==='empty',input=empty?0:24000000,output=empty?0:800000;
  const total={input,output,cache:empty?0:21600000,requests:empty?0:1280};
  const buckets=Array.from({length:365},(_,i)=>({date:new Date(Date.UTC(2025,9,22+i)).toISOString().slice(0,10),input:empty?0:i===364?input:0,output:empty?0:i===364?output:0,cache:empty?0:i===364?total.cache:0,agents:i===364?[{agent:'pot',...total}]:[]}));
  const compaction_totals=empty?[]:[{kind:'precompact',...known,input:1200000,output:24000,cache:1176000,requests:18},{kind:'compact',...known,input:1300000,output:26000,cache:1170000,requests:12}];
  if(scenario==='unknown')compaction_totals[0].cache_known=false;
  if(scenario==='partial'){compaction_totals[0].output_known=false;compaction_totals[0].output=0;}
  return {days:365,timezone:'Asia/Shanghai',total,lifetime:total,routing_total:{},buckets,agents:empty?[]:[{agent:'task',input:15400000,output:600000},{agent:'pot',input:4850000,output:150000},{agent:'summary',input:2960000,output:40000},{agent:'assistant_message',input:790000,output:10000}],models:empty?[]:[{model:'deepseek-v4-flash',input:17100000,output:660000},{model:'deepseek-v4-pro',input:6900000,output:140000}],compaction_totals,quota:{policy:{period:'none'},active:false},waiting:null};
}
const client={production:true,desktop:false,spaces:[{id:'local',name:'Fixture',connected:true}],scope:()=>({space:'local',owner:'main'}),changes:()=>()=>{},request:async path=>{
  if(new URL(path,location.origin).pathname==='/api/v1/usage/summary')return summary();
  return {items:[],providers:[]};
}};
const host=document.querySelector('#page'),result=document.querySelector('#result');
const pages=createPages({client,host,approvalHost:document.querySelector('#approvals'),chatApprovalHost:document.querySelector('#chat-approvals'),chatVisible:()=>false,locale:()=>language,notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});
const checks=[];
function check(value,message){if(!value)throw Error(message);checks.push(message);}
async function render(){
  await pages.show('usage',{force:true});
  if(baseline)return;
  const calendar=host.querySelector('.usage-calendar'),chart=calendar.querySelector('[data-view-key="annualCompaction"]');
  check(!!chart,'Annual compaction chart renders in the existing Usage page');
  check(chart.previousElementSibling.dataset.viewKey==='annualModels','Compaction chart follows annual model share');
  check(!host.querySelector('.usage-total').textContent.includes(language==='en'?'Pre-compaction':'预压缩'),'Top totals do not repeat the compaction subset');
  check(chart.querySelectorAll('.usage-share-row').length===2,'Both trigger classes are visible by default');
  const pre=chart.querySelector('[data-view-key="precompact"]'),normal=chart.querySelector('[data-view-key="compact"]');
  if(scenario==='complete'){
    check(chart.querySelectorAll('.usage-share-bar>span').length===2,'Stacked bar has both consumption segments');
    check(chart.querySelector('.usage-share-bar>span').style.width==='48%','Bar measures token consumption, not cache hit rate');
    check(pre.querySelector('.usage-compaction-cache').textContent.includes('98%')&&normal.querySelector('.usage-compaction-cache').textContent.includes('90%'),'Per-trigger cache rates remain visible');
  }else if(scenario==='unknown')check(pre.querySelector('.usage-compaction-cache').textContent.includes('—'),'Missing cache report remains unknown');
  else if(scenario==='empty')check(chart.querySelectorAll('.usage-share-bar>span').length===0,'Empty data draws no false shares');
  else if(scenario==='partial')check(pre.querySelector('.usage-share-value').textContent.startsWith('≥'),'Partial consumption is explicitly a lower bound');
  check(host.scrollWidth<=document.documentElement.clientWidth,'Usage page fits the current viewport');
  result.textContent='PASS\n'+checks.join('\n');
}
const localeSelect=document.querySelector('#fixture-locale'),themeSelect=document.querySelector('#fixture-theme'),caseSelect=document.querySelector('#fixture-case');
localeSelect.value=language;themeSelect.value=document.documentElement.dataset.theme;caseSelect.value=scenario;
localeSelect.onchange=async()=>{language=localeSelect.value;document.documentElement.lang=language;await render();};
themeSelect.onchange=()=>document.documentElement.dataset.theme=themeSelect.value;
caseSelect.onchange=async()=>{scenario=caseSelect.value;await render();};
window.addEventListener('error',event=>{result.textContent='FAIL '+event.message;});
window.addEventListener('unhandledrejection',event=>{result.textContent='FAIL '+event.reason;});
window.addEventListener('pagehide',()=>pages.dispose());
await render();
