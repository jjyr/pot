import {reconcile} from './reconcile.mjs';
import {node as n,action,table} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';
import {usageTrendBuckets,usageRangeLabel,usageToday} from './usage-period.mjs';

export function usageNumbers(locale) {
  const exact=value=>value==null?'—':Number(value).toLocaleString(locale);
  const compact=value=>value==null?'—':value>=1e6?(value/1e6).toFixed(1)+'M':value>=1e3?(value/1e3).toFixed(1)+'K':exact(value);
  const uncached=value=>value.input==null||value.input_known===false||value.cache_known===false?null:Math.max(0,value.input-(value.cache||0));
  const hitRate=value=>value.input>0&&value.input_known!==false&&value.cache_known!==false?new Intl.NumberFormat(locale,{style:'percent',maximumFractionDigits:1}).format((value.cache||0)/value.input):'—';
  return {exact,compact,uncached,hitRate};
}

export function usageTooltip(target,{title,rows=[],note='',render}) {
  const tip=n('div','usage-tooltip');tip.id=`usage-tip-${crypto.randomUUID()}`;tip.setAttribute('popover','auto');tip.setAttribute('role','tooltip');
  tip.append(n('strong','',title));
  for(const [label,value]of rows){const row=n('div','usage-tooltip-row');row.append(n('span','',label),n('span','',value));tip.append(row);}
  if(note)tip.append(n('p','hint',note));
  target.append(tip);target.tabIndex=0;target.setAttribute('aria-describedby',tip.id);
  const show=target=>{const tip=target.querySelector('.usage-tooltip');if(!tip?.isConnected)return;if(render){tip.replaceChildren(n('strong','',title),render());if(note)tip.append(n('p','hint',note));}tip.showPopover();const rect=target.getBoundingClientRect(),box=tip.getBoundingClientRect();tip.style.left=`${Math.max(8,Math.min(rect.left+(rect.width-box.width)/2,innerWidth-box.width-8))}px`;tip.style.top=`${Math.max(8,rect.top>=box.height+16?rect.top-box.height-8:Math.min(innerHeight-box.height-8,rect.bottom+8))}px`;};
  const hide=target=>target.querySelector('.usage-tooltip')?.hidePopover();
  target.onpointerenter=event=>show(event.currentTarget);
  target.onpointerleave=event=>{const node=event.currentTarget;if(node.dataset.pinned!=='true'&&!node.matches(':focus-visible'))hide(node);};
  target.onfocus=event=>show(event.currentTarget);
  target.onblur=event=>{event.currentTarget.dataset.pinned='false';hide(event.currentTarget);};
  target.onclick=event=>{const node=event.currentTarget;node.dataset.pinned=String(node.dataset.pinned!=='true');if(node.dataset.pinned==='true')show(node);else hide(node);};
  target.onkeydown=event=>{const node=event.currentTarget;if(event.key==='Escape'){node.dataset.pinned='false';hide(node);}else if(event.key==='Enter'||event.key===' '){event.preventDefault();node.onclick(event);}};
  return target;
}

export function usageRows(value,locale) {
  const {exact,uncached,hitRate}=usageNumbers(locale);
  const t=key=>translate(locale,'usage.'+key);
  const total=value.input==null||value.output==null?'—':value.input_known===false||value.output_known===false?value.input+value.output?translate(locale,'usage.atLeastTokens').replace('{count}',exact(value.input+value.output)):'—':exact(value.input+value.output);
  return [[t('total'),total],
    [t('input'),value.input_known===false?'—':exact(value.input)],
    [t('cached'),value.input==null||value.cache_known===false?'—':exact(value.cache||0)],
    [t('uncached'),exact(uncached(value))],
    [t('output'),value.output_known===false?'—':exact(value.output)],
    [t('hitRate'),hitRate(value)]];
}

export function usageTrend(data,{locale,timezone,preset}) {
  const t=key=>translate(locale,'usage.'+key),numbers=usageNumbers(locale),{buckets,kind}=usageTrendBuckets(data,preset),today=data.today||usageToday();
  const card=n('section','page-section page-card usage-trend'),heading=n('div','usage-chart-heading'),legend=n('div','usage-legend'),chart=n('div','usage-chart');
  chart.style.setProperty('--usage-bars',String(Math.max(1,buckets.length)));
  heading.append(n('h2','',t('trendTitle')),n('span','hint',t('granularity.'+kind)));card.append(heading);
  const series=[['usage-uncached',t('uncached'),value=>numbers.uncached(value)],['usage-cached',t('cached'),value=>value.cache||0],['usage-output',t('output'),value=>value.output||0]];
  for(const [className,label]of series){const entry=n('span');entry.append(n('i',className),n('span','',label));legend.append(entry);}
  chart.setAttribute('role','group');chart.setAttribute('aria-label',t('trendLabel'));
  const multipleYears=buckets[0]?.date.slice(0,4)!==buckets.at(-1)?.end.slice(0,4);
  const maximum=Math.max(1,...buckets.map(value=>(value.input||0)+(value.output||0))),shortDate=new Intl.DateTimeFormat(locale,{...(kind==='month'?{month:'short',...(multipleYears?{year:'2-digit'}:{})}:{month:'numeric',day:'numeric'}),timeZone:'UTC'}),labelStep=Math.max(1,Math.ceil(buckets.length/6));
  for(const [index,bucket]of buckets.entries()){
    const column=n('div','chart-column'),bar=n('div','chart-bar'),date=new Date(bucket.date+'T12:00:00Z'),title=usageRangeLabel({start:bucket.date,end:bucket.end},locale),rows=usageRows(bucket,locale),current=bucket.date<=today&&bucket.end>=today,value=(bucket.input||0)+(bucket.output||0);
    column.dataset.viewKey=bucket.date;column.dataset.date=bucket.date;
    column.setAttribute('aria-label',`${title} · ${rows.map(row=>row.join(' ')).join(' · ')}`);
    bar.style.height=`${value/maximum*140}px`;
    for(const [className,,amount]of series){const segment=n('span',className);segment.style.flexGrow=String(amount(bucket)||0);bar.append(segment);}
    column.append(bar,n('small','',index%labelStep===0?shortDate.format(date):'\u00a0'));
    usageTooltip(column,{title,rows,note:[current?t('throughNow'):'',timezone].filter(Boolean).join(' · ')});chart.append(column);
  }
  card.append(legend,chart);if(!buckets.length)card.append(n('p','hint',t('empty')));
  card.append(n('p','hint usage-trend-note',t('trendNote').replace('{timezone}',timezone)));return card;
}

// Keep each resource's page, failures and pending state local to its own region.
// Requests capture both the outer view token and their own sequence number.
export function createUsageList({root,kind,title,params,owner,request,token,current,offsets,cache,locale,tr,spaceName=''}) {
  const key=JSON.stringify([owner.space,owner.owner,kind,params]),region=n('section',`page-section page-card usage-list usage-list-${kind}`),heading=n('div','usage-list-heading'),content=n('div'),status=n('div');
  heading.append(n('h2','',title));if(spaceName)heading.append(n('span','hint',spaceName));content.append(n('div','usage-list-skeleton'));region.append(heading,content,status);root.append(region);
  let offset=offsets.get(key)||0,sequence=0;
  const {exact,compact}=usageNumbers(locale);
  function render(value,stale=false){
    const rows=value.items||[],fragment=n('div');
    if(!rows.length)fragment.append(n('p','hint',tr('No records yet','暂无记录')));
    else if(kind==='models')for(const model of rows){const row=n('div','usage-model-row'),line=n('div','usage-model-title');line.append(n('span','',`${model.agent==='pot'?'Pot':model.agent} · ${model.model}`),usageTooltip(n('span','usage-value',`${compact(model.input+model.output)} tokens`),{title:model.model,rows:usageRows(model,locale)}));row.append(line,n('p','hint',`${tr('Input','输入')} ${compact(model.input)} · ${tr('Cached input','缓存输入')} ${compact(model.cache)} · ${tr('Output','输出')} ${compact(model.output)}`));fragment.append(row);}
    else if(kind==='requests')fragment.append(table([tr('Time','时间'),tr('Model','模型'),tr('Input','输入'),tr('Cached input','缓存输入'),tr('Output','输出'),tr('State','状态')],rows.map(record=>[record.label,record.model,usageTooltip(n('span','usage-value',exact(record.input)),{title:record.label,rows:usageRows(record,locale)}),record.input==null?'—':exact(record.cache),exact(record.output),record.state])));
    else if(kind==='tools')fragment.append(table([tr('Tool','工具'),tr('Calls','调用次数'),tr('Failed','失败'),tr('Duration (ms)','耗时（毫秒）')],rows.map(tool=>[tool.name,exact(tool.calls),exact(tool.failed),exact(tool.duration_ms)])));
    else fragment.append(table([tr('Time','时间'),tr('State','状态'),tr('Before','之前'),tr('After','之后')],rows.map(record=>[record.label,record.state,exact(record.tokens_before),exact(record.tokens_after)])));
    if(offset||value.next_offset!=null){const footer=n('div','usage-pagination');footer.append(n('span','hint',`${offset+1}–${offset+rows.length}`),action(tr('Previous page','上一页'),()=>load(Math.max(0,offset-20)),{disabled:!offset||stale}),action(tr('Next page','下一页'),()=>load(value.next_offset),{disabled:value.next_offset==null||stale}));fragment.append(footer);}
    reconcile(content,fragment);status.replaceChildren();if(stale)status.append(n('p','inline-error',tr('Cached data · unavailable','缓存数据 · 当前不可用')),action(tr('Retry','重试'),()=>load(offset)));
  }
  async function load(next=offset){
    if(!current())return;const query=new URLSearchParams({...params,offset:next}),path=`/api/v1/usage/${kind}?${query}`,cacheKey=JSON.stringify([key,next]),attempt=++sequence,buttons=[...content.querySelectorAll('button')].map(button=>[button,button.disabled]);
    const saved=cache.get(cacheKey);if(saved){offset=next;offsets.set(key,offset);render(saved);}
    region.setAttribute('aria-busy','true');for(const button of region.querySelectorAll('button'))button.disabled=true;
    try{const value=await request(path,{...owner,signal:token.signal});if(!current()||attempt!==sequence)return;offset=next;offsets.set(key,offset);cache.set(cacheKey,value);render(value);}
    catch(reason){if(!current()||attempt!==sequence)return;const saved=cache.get(cacheKey);if(saved){offset=next;offsets.set(key,offset);render(saved,true);}else {content.querySelector('.usage-list-skeleton')?.remove();status.replaceChildren(n('p','inline-error',reason.message),action(tr('Retry','重试'),()=>load(next)));for(const [button,disabled]of buttons)button.disabled=disabled;}}
    finally{if(current()&&attempt===sequence)region.removeAttribute('aria-busy');}
  }
  const ready=load();ready.refresh=()=>load();return ready;
}
