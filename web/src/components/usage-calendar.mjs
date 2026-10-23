import {node as n,action} from './primitives.mjs';
import {usageToday} from './usage-period.mjs';
import {usageNumbers,usageRows,usageTooltip} from './usage.mjs';
import {translate} from '../locales/messages.mjs';

const tokens=row=>Number(row.input||0)+Number(row.output||0);
const date=value=>new Date(value+'T12:00:00Z');
const agentKeys=['task','pot','summary','assistant_message'];
const sortRows=(rows,key)=>[...rows].sort((a,b)=>tokens(b)-tokens(a)||(a[key]<b[key]?-1:a[key]>b[key]?1:0));
export function usageModelColor(name) {
  let hash=2166136261;
  for(const char of name){hash^=char.codePointAt(0);hash=Math.imul(hash,16777619);}
  hash=Math.imul(hash^(hash>>>16),0x85ebca6b);hash=Math.imul(hash^(hash>>>13),0xc2b2ae35);hash^=hash>>>16;
  const hue=(hash>>>0)%360;
  return `light-dark(hsl(${hue} 52% 51%),hsl(${hue} 58% 65%))`;
}
export function usageCalendarWeeks(buckets) {
  if(!buckets.length)return [];
  const slots=[...Array(date(buckets[0].date).getUTCDay()).fill(null),...buckets];
  while(slots.length%7)slots.push(null);
  return Array.from({length:slots.length/7},(_,i)=>slots.slice(i*7,i*7+7));
}
export function usagePercent(value,total,locale) {
  const format=new Intl.NumberFormat(locale,{style:'percent',maximumFractionDigits:2});
  if(value>0&&total>0&&value/total<.0001)return translate(locale,'usage.lessThanPercent').replace('{percent}',format.format(.0001));
  return format.format(total>0?value/total:0);
}
function agentRows(rows=[]) {
  return sortRows([...rows,...agentKeys.filter(agent=>!rows.some(row=>row.agent===agent)).map(agent=>({agent,input:0,output:0}))],'agent');
}
function agentLabel(agent,locale) {
  return [...agentKeys,'unknown','recovery'].includes(agent)?translate(locale,'usage.agent.'+agent):agent;
}
export function usageCompactionRows(rows=[]) {
  return ['precompact','compact'].map(kind=>({kind,input:0,output:0,cache:0,requests:0,input_known:true,output_known:true,cache_known:true,...rows.find(row=>row.kind===kind)}));
}
export function compactionTokens(row,locale) {
  const value=tokens(row),formatted=usageNumbers(locale).compact(value);
  if(row.input_known===false||row.output_known===false)return value?translate(locale,'usage.atLeastTokens').replace('{count}',formatted):'—';
  return formatted;
}
function distribution(rows,{locale,kind='agent',exact=false}) {
  const root=n('div','usage-distribution'),bar=n('div','usage-share-bar'),list=n('div','usage-share-list'),total=rows.reduce((sum,row)=>sum+tokens(row),0),numbers=usageNumbers(locale);
  const label=row=>kind==='compaction'?translate(locale,'usage.compaction.'+row.kind):kind==='model'?row.model:agentLabel(row.agent,locale);
  bar.setAttribute('role','img');bar.setAttribute('aria-label',rows.map(row=>`${label(row)} ${usagePercent(tokens(row),total,locale)}`).join(' · ')||translate(locale,'usage.empty'));
  for(const row of rows){
    const value=tokens(row),color=kind==='compaction'?usageModelColor(row.kind==='precompact'?'precompaction':'compaction'):kind==='model'?usageModelColor(row.model):agentKeys.includes(row.agent)?`var(--usage-${row.agent})`:usageModelColor(row.agent);
    if(value){const segment=n('span');segment.style.width=`${value/total*100}%`;segment.style.background=color;bar.append(segment);}
    const line=n('div','usage-share-row'),name=n('span','usage-share-name'),swatch=n('i'),amount=n('span','hint usage-share-value',kind==='compaction'?compactionTokens(row,locale):exact?numbers.exact(value):numbers.compact(value));
    line.dataset.viewKey=kind==='compaction'?row.kind:kind==='model'?row.model:row.agent;swatch.style.background=color;swatch.setAttribute('aria-hidden','true');name.append(swatch,n('span','',label(row)));line.append(name,amount,n('span','usage-share-percent',usagePercent(value,total,locale)));
    if(kind==='compaction'){
      line.append(n('span','usage-compaction-cache',translate(locale,'usage.compactionCacheHit').replace('{rate}',numbers.hitRate(row))));
      usageTooltip(line,{title:label(row),rows:usageRows(row,locale),note:translate(locale,'usage.compactionNote')});
    }
    list.append(line);
  }
  root.append(bar,list);if(!rows.length)root.append(n('p','hint',translate(locale,'usage.empty')));return root;
}
export function usageCalendar(data,{locale,timezone,year,onYear,onDate}) {
  const t=key=>translate(locale,'usage.'+key),numbers=usageNumbers(locale),card=n('section','page-section page-card usage-calendar'),heading=n('div','usage-chart-heading'),period=n('div','usage-calendar-period');
  const buckets=data.buckets||[],weeks=usageCalendarWeeks(buckets),split=Math.ceil(weeks.length/2),maximum=Math.max(1,...buckets.map(tokens));
  card.style.setProperty('--usage-week-count',String(weeks.length||53));card.style.setProperty('--usage-first-weeks',String(split||27));
  const today=data.today||usageToday(),controls=n('div','usage-calendar-controls');
  year=year||Number(buckets.at(-1)?.date.slice(0,4)||today.slice(0,4));
  const previous=action('‹',()=>onYear?.(year-1)),next=action('›',()=>onYear?.(year+1),{disabled:year>=Number(today.slice(0,4))});
  for(const [button,key]of [[previous,'previousYear'],[next,'nextYear']]){button.setAttribute('aria-label',t(key));button.title=t(key);}
  controls.append(previous,n('span','',String(year)),next);heading.append(n('h2','',t('calendarTitle')),controls);card.append(heading);
  const formatDate=new Intl.DateTimeFormat(locale,{dateStyle:'medium',timeZone:'UTC'}),month=new Intl.DateTimeFormat(locale,{month:'short',timeZone:'UTC'});
  period.append(n('span','hint',buckets.length?`${formatDate.format(date(buckets[0].date))} — ${formatDate.format(date(buckets.at(-1).date))}`:t('empty')),n('span','usage-value',t('tokens').replace('{count}',numbers.compact(tokens(data.total||{})))));card.append(period);
  const panels=n('div','usage-calendar-panels');
  for(const [index,group]of [weeks.slice(0,split),weeks.slice(split)].entries()){
    if(!group.length)continue;
    const panel=n('div','usage-calendar-panel'),months=n('div','usage-calendar-months'),calendar=n('div','usage-calendar-matrix'),weekdays=n('div','usage-calendar-weekdays'),grid=n('div','usage-calendar-grid');
    panel.style.setProperty('--usage-weeks',String(group.length));panel.dataset.viewKey=String(index);months.setAttribute('aria-hidden','true');weekdays.setAttribute('aria-hidden','true');
    for(let i=0;i<7;i++)weekdays.append(n('span','',i%2?new Intl.DateTimeFormat(locale,{weekday:'narrow',timeZone:'UTC'}).format(new Date(Date.UTC(2026,9,18+i))):''));
    grid.setAttribute('role','group');grid.setAttribute('aria-label',t('calendarLabel'));
    for(const [column,week]of group.entries()){
      const first=week.find(bucket=>bucket?.date.endsWith('-01'));
      if(first){const label=n('span','',month.format(date(first.date)));label.style.gridColumn=`${column+1} / span ${Math.min(3,group.length-column)}`;months.append(label);}
      for(const bucket of week){
        if(!bucket){grid.append(n('span','usage-day-blank'));continue;}
        const day=n('button','usage-day'),value=tokens(bucket),title=formatDate.format(date(bucket.date)),current=bucket.date===today,rows=agentRows(bucket.agents);
        day.type='button';day.dataset.viewKey=bucket.date;day.dataset.date=bucket.date;day.dataset.level=String(value?Math.min(4,1+Math.floor(Math.sqrt(value/maximum)*4)):0);
        day.disabled=bucket.date>today;
        day.setAttribute('aria-label',t('dayLabel').replace('{date}',title).replace('{count}',numbers.exact(value))+' · '+rows.map(row=>`${agentLabel(row.agent,locale)} ${numbers.exact(tokens(row))} ${usagePercent(tokens(row),value,locale)}`).join(' · '));
        usageTooltip(day,{title:`${title} · ${t('tokens').replace('{count}',numbers.compact(value))}`,note:[current?t('throughNow'):'',timezone].filter(Boolean).join(' · '),render:()=>{
          const detail=n('div','usage-day-detail');detail.append(distribution(rows,{locale,exact:true}));const counts=n('div','usage-day-counts');
          for(const [label,value]of usageRows(bucket,locale)){const row=n('div','usage-tooltip-row');row.append(n('span','',label),n('span','',value));counts.append(row);}detail.append(counts);return detail;
        }});
        day.onclick=event=>{event.currentTarget.querySelector('.usage-tooltip')?.hidePopover();onDate?.(bucket.date);};grid.append(day);
      }
    }
    calendar.append(weekdays,grid);panel.append(months,calendar);panels.append(panel);
  }
  card.append(panels);
  const legend=n('div','usage-calendar-legend');legend.append(n('span','',t('less')));for(let level=0;level<5;level++){const swatch=n('i');swatch.dataset.level=String(level);swatch.setAttribute('aria-hidden','true');legend.append(swatch);}legend.append(n('span','',t('more')));card.append(legend);
  card.append(n('p','hint usage-calendar-note',t('calendarNote').replace('{timezone}',timezone)));return card;
}

export function usageDistributions(data,{locale}) {
  const t=key=>translate(locale,'usage.'+key),numbers=usageNumbers(locale),card=n('div','usage-distributions');
  for(const [key,rows,kind]of [['categoryShare',agentRows(data.agents),'agent'],['modelShare',sortRows(data.models||[],'model'),'model']]){
    const section=n('section','page-section page-card usage-period-share'),header=n('div','usage-share-heading');section.dataset.viewKey=key;header.append(n('h2','',t(key)),n('span','hint',t('tokens').replace('{count}',numbers.compact(tokens(data.total||{})))));section.append(header,distribution(rows,{locale,kind}));card.append(section);
  }
  const compactions=usageCompactionRows(data.compaction_totals),compactionTotal={input:0,output:0,input_known:true,output_known:true};
  for(const row of compactions){compactionTotal.input+=row.input;compactionTotal.output+=row.output;compactionTotal.input_known&&=row.input_known;compactionTotal.output_known&&=row.output_known;}
  const compactSection=n('section','page-section page-card usage-period-share'),compactHeading=n('div','usage-share-heading');compactSection.dataset.viewKey='compactionShare';
  compactHeading.append(n('h2','',t('compactionShare')),n('span','hint',t('tokens').replace('{count}',compactionTokens(compactionTotal,locale))));compactSection.append(compactHeading,distribution(compactions,{locale,kind:'compaction'}));card.append(compactSection);
  return card;
}
