import {node as n,action} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

const date=value=>new Date(value+'T12:00:00Z');
const iso=value=>value.toISOString().slice(0,10);
export const usageToday=()=>new Intl.DateTimeFormat('en-CA',{year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date());
export function usageDateAdd(value,days){const result=date(value);result.setUTCDate(result.getUTCDate()+days);return iso(result);}
export function usageRange(preset='month',anchor=usageToday()) {
  const value=date(anchor),year=value.getUTCFullYear(),month=value.getUTCMonth();
  if(preset==='year')return {preset,start:`${year}-01-01`,end:`${year}-12-31`};
  if(preset==='month')return {preset,start:iso(new Date(Date.UTC(year,month,1,12))),end:iso(new Date(Date.UTC(year,month+1,0,12)))};
  if(preset==='week'){const start=usageDateAdd(anchor,-((value.getUTCDay()+6)%7));return {preset,start,end:usageDateAdd(start,6)};}
  return {preset:'recent',start:usageDateAdd(anchor,-29),end:anchor};
}
export function usageShiftRange(range,direction) {
  if(range.preset==='month')return usageRange('month',usageDateAdd(direction<0?range.start:range.end,direction));
  if(range.preset==='year')return usageRange('year',`${Number(range.start.slice(0,4))+direction}-01-01`);
  const days=Math.round((date(range.end)-date(range.start))/86400000)+1;
  return {...range,start:usageDateAdd(range.start,days*direction),end:usageDateAdd(range.end,days*direction)};
}
export function usageValidRange(start,end) {
  return /^\d{4}-\d{2}-\d{2}$/.test(start)&&/^\d{4}-\d{2}-\d{2}$/.test(end)&&Number.isFinite(+date(start))&&Number.isFinite(+date(end))&&iso(date(start))===start&&iso(date(end))===end&&end>=start&&(date(end)-date(start))/86400000<3660;
}
export function usageRangeLabel(range,locale) {
  const format=new Intl.DateTimeFormat(locale,{dateStyle:'medium',timeZone:'UTC'});
  return range.start===range.end?format.format(date(range.start)):`${format.format(date(range.start))} — ${format.format(date(range.end))}`;
}
export function usageRangeControls(range,{locale,onRange,onDraft=()=>{},draft,today=usageToday()}) {
  const t=key=>translate(locale,'usage.'+key),root=n('section','page-card usage-range'),line=n('div','usage-range-toolbar'),select=n('select','field-input usage-period-select');
  root.dataset.start=range.start;root.dataset.end=range.end;
  select.setAttribute('aria-label',t('period'));
  for(const key of ['week','month','year','recent','custom']){const option=n('option','',t('period.'+key));option.value=key;select.append(option);}select.value=range.preset;
  select.onchange=event=>onRange(event.currentTarget.value==='custom'?{...range,preset:'custom'}:usageRange(event.currentTarget.value,today));
  const previous=action('‹',()=>onRange(usageShiftRange(range,-1))),next=action('›',()=>onRange(usageShiftRange(range,1)),{disabled:usageShiftRange(range,1).start>today});
  for(const [button,key]of [[previous,'previousPeriod'],[next,'nextPeriod']]){button.setAttribute('aria-label',t(key));button.title=t(key);}
  line.append(select,previous,n('strong','usage-range-label',usageRangeLabel(range,locale)),next,action(t('currentPeriod'),()=>onRange(usageRange(range.preset==='custom'?'month':range.preset,today))));root.append(line);
  if(range.preset==='custom'){
    const form=n('form','usage-custom-range'),start=n('input','field-input'),end=n('input','field-input'),error=n('p','inline-error');
    start.type=end.type='date';start.value=draft?.start??range.start;end.value=draft?.end??range.end;start.required=end.required=true;
    start.setAttribute('aria-label',t('startDate'));end.setAttribute('aria-label',t('endDate'));error.setAttribute('role','alert');
    for(const [input,key]of [[start,'startDate'],[end,'endDate']]){const label=n('label');label.append(n('span','hint',t(key)),input);form.append(label);}
    const apply=action(t('applyRange'));apply.type='submit';form.append(apply,error);
    for(const input of [start,end])input.oninput=event=>{const [start,end]=[...event.currentTarget.form.querySelectorAll('input')].map(input=>input.value);onDraft({start,end});};
    form.onsubmit=event=>{event.preventDefault();const inputs=event.currentTarget.querySelectorAll('input'),[start,end]=[...inputs].map(input=>input.value);if(!usageValidRange(start,end)){event.currentTarget.querySelector('[role="alert"]').textContent=t('invalidRange');return;}onRange({preset:'custom',start,end});};root.append(form);
  }
  if(range.start<=today&&range.end>=today)root.append(n('p','hint',t('throughToday')));
  return root;
}

export function usageTrendBuckets(data,preset) {
  const buckets=data.buckets||[],kind=preset==='year'||buckets.length>180?'month':buckets.length>31?'week':'day',groups=new Map();
  for(const bucket of buckets){
    const key=kind==='month'?bucket.date.slice(0,7):kind==='week'?usageRange('week',bucket.date).start:bucket.date;
    let row=groups.get(key);if(!row){row={date:bucket.date,end:bucket.date,input:0,output:0,cache:0,requests:0};groups.set(key,row);}row.end=bucket.date;
    for(const field of ['input','output','cache','requests'])row[field]+=Number(bucket[field]||0);
    for(const field of ['input_known','output_known','cache_known'])if(bucket[field]===false)row[field]=false;
  }
  return {kind,buckets:[...groups.values()]};
}
