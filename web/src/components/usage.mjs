import {reconcile} from './reconcile.mjs';
import {node as n,action,table} from './primitives.mjs';

export function usageNumbers(locale) {
  const exact=value=>value==null?'—':Number(value).toLocaleString(locale);
  const compact=value=>value==null?'—':value>=1e6?(value/1e6).toFixed(1)+'M':value>=1e3?(value/1e3).toFixed(1)+'K':exact(value);
  const uncached=value=>value.input==null?null:Math.max(0,value.input-(value.cache||0));
  const hitRate=value=>value.input>0?new Intl.NumberFormat(locale,{style:'percent',maximumFractionDigits:1}).format((value.cache||0)/value.input):'—';
  return {exact,compact,uncached,hitRate};
}

export function usageTooltip(target,{title,rows=[],note=''}) {
  const tip=n('div','usage-tooltip');tip.id=`usage-tip-${crypto.randomUUID()}`;tip.setAttribute('popover','auto');tip.setAttribute('role','tooltip');
  tip.append(n('strong','',title));
  for(const [label,value]of rows){const row=n('div','usage-tooltip-row');row.append(n('span','',label),n('span','',value));tip.append(row);}
  if(note)tip.append(n('p','hint',note));
  target.append(tip);target.tabIndex=0;target.setAttribute('aria-describedby',tip.id);
  const show=target=>{const tip=target.querySelector('.usage-tooltip');if(!tip?.isConnected)return;tip.showPopover();const rect=target.getBoundingClientRect(),box=tip.getBoundingClientRect();tip.style.left=`${Math.max(8,Math.min(rect.left+(rect.width-box.width)/2,innerWidth-box.width-8))}px`;tip.style.top=`${Math.max(8,rect.top>=box.height+16?rect.top-box.height-8:Math.min(innerHeight-box.height-8,rect.bottom+8))}px`;};
  const hide=target=>target.querySelector('.usage-tooltip')?.hidePopover();
  target.onpointerenter=event=>show(event.currentTarget);
  target.onpointerleave=event=>{const node=event.currentTarget;if(node.dataset.pinned!=='true'&&!node.matches(':focus-visible'))hide(node);};
  target.onfocus=event=>show(event.currentTarget);
  target.onblur=event=>{event.currentTarget.dataset.pinned='false';hide(event.currentTarget);};
  target.onclick=event=>{const node=event.currentTarget;node.dataset.pinned=String(node.dataset.pinned!=='true');if(node.dataset.pinned==='true')show(node);else hide(node);};
  target.onkeydown=event=>{const node=event.currentTarget;if(event.key==='Escape'){node.dataset.pinned='false';hide(node);}else if(event.key==='Enter'||event.key===' '){event.preventDefault();node.onclick(event);}};
  return target;
}

export function usageRows(value,locale,tr) {
  const {exact,uncached,hitRate}=usageNumbers(locale);
  return [[tr('Total tokens','总 token'),value.input==null||value.output==null?'—':exact(value.input+value.output)],
    [tr('Input (includes cache)','输入（含缓存）'),exact(value.input)],
    [tr('Cached input','缓存输入'),value.input==null?'—':exact(value.cache||0)],
    [tr('Uncached input','未缓存输入'),exact(uncached(value))],
    [tr('Output','输出'),exact(value.output)],
    [tr('Cache hit rate','缓存命中率'),hitRate(value)]];
}

export function usageTrend(data,{locale,tr,timezone,days,onDays,segments}) {
  const card=n('section','page-section page-card usage-trend'),header=n('div','usage-chart-heading'),chart=n('div','usage-chart'),legend=n('div','usage-legend');
  header.append(n('h2','',tr('Usage trend','用量趋势')),usageTooltip(n('span','hint usage-chart-period',data.bucket_days===7?tr('Weekly','按周'):tr('Daily','按日')),{title:timezone,note:tr('The current period is still in progress.','当前周期统计截至现在。')}));
  card.append(header,segments([7,30,90,365].map(value=>[value,value===365?tr('Last year','最近一年'):tr(`Last ${value} days`,`最近 ${value} 天`)]),days,onDays));
  const {uncached}=usageNumbers(locale),series=[['usage-uncached',tr('Uncached input','未缓存输入'),uncached],['usage-cached',tr('Cached input','缓存输入'),value=>value.cache||0],['usage-output',tr('Output','输出'),value=>value.output||0]];
  chart.setAttribute('role','group');chart.setAttribute('aria-label',tr('Token usage trend','Token 用量趋势'));
  const maximum=Math.max(1,...(data.buckets||[]).map(value=>value.input+value.output));
  for(const [index,bucket]of (data.buckets||[]).entries()){
    const column=n('div','chart-column'),bar=n('div','chart-bar'),last=index===data.buckets.length-1;
    const title=data.bucket_days===7?bucket.range:(bucket.range||bucket.label).split(' — ')[0];
    const rows=usageRows(bucket,locale,tr),note=[last?tr('Statistics through now','统计截至当前'):'',timezone].filter(Boolean).join(' · ');
    column.setAttribute('aria-label',`${title} · ${rows.map(row=>row.join(' ')).join(' · ')} · ${note}`);
    bar.style.height=`${Math.max(0,(bucket.input+bucket.output)/maximum*140)}px`;
    for(const [className,,value]of series){const segment=n('span',className);segment.style.flexGrow=String(value(bucket)||0);bar.append(segment);}
    column.append(bar,n('small','',last&&data.bucket_days!==7?tr('Today','今天'):bucket.label));usageTooltip(column,{title,rows,note});chart.append(column);
  }
  for(const [className,label]of series){const entry=n('span');entry.append(n('i',className),n('span','',label));legend.append(entry);}
  card.append(legend,chart);return card;
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
    else if(kind==='models')for(const model of rows){const row=n('div','usage-model-row'),line=n('div','usage-model-title');line.append(n('span','',`${model.agent==='pot'?'Pot':model.agent} · ${model.model}`),usageTooltip(n('span','usage-value',`${compact(model.input+model.output)} tokens`),{title:model.model,rows:usageRows(model,locale,tr)}));row.append(line,n('p','hint',`${tr('Input','输入')} ${compact(model.input)} · ${tr('Cached input','缓存输入')} ${compact(model.cache)} · ${tr('Output','输出')} ${compact(model.output)}`));fragment.append(row);}
    else if(kind==='requests')fragment.append(table([tr('Time','时间'),tr('Model','模型'),tr('Input','输入'),tr('Cached input','缓存输入'),tr('Output','输出'),tr('State','状态')],rows.map(record=>[record.label,record.model,usageTooltip(n('span','usage-value',exact(record.input)),{title:record.label,rows:usageRows(record,locale,tr)}),record.input==null?'—':exact(record.cache),exact(record.output),record.state])));
    else if(kind==='tools')fragment.append(table([tr('Tool','工具'),tr('Calls','调用次数'),tr('Failed','失败'),tr('Duration (ms)','耗时（毫秒）')],rows.map(tool=>[tool.name,exact(tool.calls),exact(tool.failed),exact(tool.duration_ms)])));
    else fragment.append(table([tr('Time','时间'),tr('State','状态'),tr('Before','之前'),tr('After','之后')],rows.map(record=>[record.label,record.state,exact(record.tokens_before),exact(record.tokens_after)])));
    if(offset||value.next_offset!=null){const footer=n('div','usage-pagination');footer.append(n('span','hint',`${offset+1}–${offset+rows.length}`),action(tr('Previous page','上一页'),()=>load(Math.max(0,offset-20)),{disabled:!offset||stale}),action(tr('Next page','下一页'),()=>load(value.next_offset),{disabled:value.next_offset==null||stale}));fragment.append(footer);}
    reconcile(content,fragment);status.replaceChildren();if(stale)status.append(n('p','inline-error',tr('Cached data · unavailable','缓存数据 · 当前不可用')),action(tr('Retry','重试'),()=>load(offset)));
  }
  async function load(next=offset){
    if(!current())return;const query=new URLSearchParams({...params,offset:next}),path=`/api/v1/usage/${kind}?${query}`,cacheKey=JSON.stringify([key,next]),attempt=++sequence,buttons=[...content.querySelectorAll('button')].map(button=>[button,button.disabled]);
    region.setAttribute('aria-busy','true');for(const button of region.querySelectorAll('button'))button.disabled=true;
    try{const value=await request(path,{...owner,signal:token.signal});if(!current()||attempt!==sequence)return;offset=next;offsets.set(key,offset);cache.set(cacheKey,value);render(value);}
    catch(reason){if(!current()||attempt!==sequence)return;const saved=cache.get(cacheKey);if(saved){offset=next;offsets.set(key,offset);render(saved,true);}else {content.querySelector('.usage-list-skeleton')?.remove();status.replaceChildren(n('p','inline-error',reason.message),action(tr('Retry','重试'),()=>load(next)));for(const [button,disabled]of buttons)button.disabled=disabled;}}
    finally{if(current()&&attempt===sequence)region.removeAttribute('aria-busy');}
  }
  const ready=load();ready.refresh=()=>load();return ready;
}
