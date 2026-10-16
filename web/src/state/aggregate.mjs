export function ownedItems(value, space, stale = false, owner = '') {
  const items=Array.isArray(value)?value:value?.items||[];
  return items.map(item=>({...item,_assistant_id:item.assistant_id||owner,_space_id:space.id,_space_name:space.name,_stale:stale}));
}
export function combineTasks(pages,{offset=0,limit=20}={}) {
  const items=pages.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner)).sort((a,b)=>b.updated_at_ms-a.updated_at_ms||a.id.localeCompare(b.id));
  const counts={active:0,attention:0,archived:0};for(const page of pages)for(const key of Object.keys(counts))counts[key]+=page.value.counts?.[key]||0;
  return {items:items.slice(offset,offset+limit),total:items.length,next_offset:offset+limit<items.length?offset+limit:null,counts};
}
function sumRows(rows,keys) {const result={};for(const key of keys)result[key]=rows.reduce((sum,row)=>sum+Number(row?.[key]||0),0);return result;}
export function combineUsage(pages) {
  const numeric=['input','output','cache','requests','unknown'];
  const result={total:sumRows(pages.map(page=>page.value.total),numeric),buckets:[],requests:[],tools:[],compactions:[],spaces:pages};
  const buckets=new Map(),tools=new Map();
  for(const {value,space,stale,owner=''}of pages) {
    for(const bucket of value.buckets||[]){const old=buckets.get(bucket.label);buckets.set(bucket.label,{...bucket,...sumRows([old,bucket],numeric)});}
    for(const tool of value.tools||[]){const old=tools.get(tool.name);tools.set(tool.name,{...tool,...sumRows([old,tool],['calls','failed','bytes','duration_ms','unfinished'])});}
    result.requests.push(...(value.requests||[]).map(item=>({...item,_assistant_id:item.assistant_id||owner,_space_id:space.id,_space_name:space.name,_stale:stale})));
    result.compactions.push(...(value.compactions||[]).map(item=>({...item,_assistant_id:item.assistant_id||owner,_space_id:space.id,_space_name:space.name,_stale:stale})));
  }
  result.buckets=[...buckets.values()];result.tools=[...tools.values()];result.requests.sort((a,b)=>b.at-a.at);result.next_offset=null;
  return result;
}
