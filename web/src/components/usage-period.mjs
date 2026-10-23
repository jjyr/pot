import {node as n,action} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

const date=value=>new Date(value+'T12:00:00Z');
const iso=value=>value.toISOString().slice(0,10);
export const usageToday=()=>new Intl.DateTimeFormat('en-CA',{year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date());
export function usageDateAdd(value,days){const result=date(value);result.setUTCDate(result.getUTCDate()+days);return iso(result);}
const weekStart=value=>usageDateAdd(value,-((date(value).getUTCDay()+6)%7));
export function usageRange(preset='month',anchor=usageToday()) {
  const value=date(anchor),year=value.getUTCFullYear(),month=value.getUTCMonth();
  if(preset==='year')return {preset,start:`${year}-01-01`,end:`${year}-12-31`};
  if(preset==='month')return {preset,start:iso(new Date(Date.UTC(year,month,1,12))),end:iso(new Date(Date.UTC(year,month+1,0,12)))};
  if(preset==='quarter'){const first=Math.floor(month/3)*3;return {preset,start:iso(new Date(Date.UTC(year,first,1,12))),end:iso(new Date(Date.UTC(year,first+3,0,12)))};}
  if(preset==='fortnight'){const start=usageDateAdd(weekStart(anchor),-7);return {preset,start,end:usageDateAdd(start,13)};}
  throw Error('Unknown usage preset');
}
export function usageShiftRange(range,direction) {
  if(range.preset==='month')return usageRange('month',usageDateAdd(direction<0?range.start:range.end,direction));
  if(range.preset==='year')return usageRange('year',`${Number(range.start.slice(0,4))+direction}-01-01`);
  if(range.preset==='quarter')return usageRange('quarter',usageDateAdd(direction<0?range.start:range.end,direction));
  const days=Math.round((date(range.end)-date(range.start))/86400000)+1;
  return {...range,start:usageDateAdd(range.start,days*direction),end:usageDateAdd(range.end,days*direction)};
}
export function usageRangeLabel(range,locale) {
  const format=new Intl.DateTimeFormat(locale,{dateStyle:'medium',timeZone:'UTC'});
  return range.start===range.end?format.format(date(range.start)):`${format.format(date(range.start))} — ${format.format(date(range.end))}`;
}
export function usageRangeControls(range,{locale,onRange,today=usageToday()}) {
  const t=key=>translate(locale,'usage.'+key),root=n('section','page-card usage-range'),line=n('div','usage-range-toolbar'),shortcuts=n('div','usage-period-links');
  root.dataset.start=range.start;root.dataset.end=range.end;root.dataset.preset=range.preset;
  shortcuts.setAttribute('role','group');shortcuts.setAttribute('aria-label',t('period'));
  for(const key of ['fortnight','month','quarter','year']){
    const target=usageRange(key,today),selected=range.preset===key;
    const button=action(t('period.'+key),()=>onRange(target));button.classList.add('usage-period-link');button.classList.toggle('selected',selected);button.dataset.preset=key;button.setAttribute('aria-pressed',String(selected));shortcuts.append(button);
  }
  const previous=action('‹',()=>onRange(usageShiftRange(range,-1))),next=action('›',()=>onRange(usageShiftRange(range,1)),{disabled:usageShiftRange(range,1).start>today});
  for(const [button,key]of [[previous,'previousPeriod'],[next,'nextPeriod']]){button.setAttribute('aria-label',t(key));button.title=t(key);}
  line.append(previous,n('strong','usage-range-label',usageRangeLabel(range,locale)),next);root.append(shortcuts,line);
  if(range.start<=today&&range.end>=today)root.append(n('p','hint',t('throughToday')));
  return root;
}

export function usageTrendBuckets(data,preset) {
  const buckets=data.buckets||[],kind=preset==='year'||buckets.length>180?'month':buckets.length>31?'week':'day',groups=new Map();
  for(const bucket of buckets){
    const key=kind==='month'?bucket.date.slice(0,7):kind==='week'?weekStart(bucket.date):bucket.date;
    let row=groups.get(key);if(!row){row={date:bucket.date,end:bucket.date,input:0,output:0,cache:0,requests:0};groups.set(key,row);}row.end=bucket.date;
    for(const field of ['input','output','cache','requests'])row[field]+=Number(bucket[field]||0);
    for(const field of ['input_known','output_known','cache_known'])if(bucket[field]===false)row[field]=false;
  }
  return {kind,buckets:[...groups.values()]};
}
