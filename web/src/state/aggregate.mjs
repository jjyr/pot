export function ownedItems(value, space, stale = false, owner = '') {
  const items=Array.isArray(value)?value:value?.items||[];
  return items.map(item=>({...item,_assistant_id:item.assistant_id||owner,_space_id:space.id,_space_name:space.name,_stale:stale}));
}
export function combineTasks(pages,{offset=0,limit=20}={}) {
  const items=pages.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner)).sort((a,b)=>b.updated_at_ms-a.updated_at_ms||(a.id<b.id?-1:a.id>b.id?1:0)||a._space_id.localeCompare(b._space_id)||a._assistant_id.localeCompare(b._assistant_id));
  const counts={active:0,attention:0,archived:0};for(const page of pages)for(const key of Object.keys(counts))counts[key]+=page.value.counts?.[key]||0;
  const total=pages.reduce((sum,page)=>sum+(page.value.total??page.value.items?.length??0),0);
  return {items:items.slice(offset,offset+limit),total,next_offset:offset+limit<total?offset+limit:null,counts};
}
function sumRows(rows,keys) {const result={};for(const key of keys)result[key]=rows.reduce((sum,row)=>sum+Number(row?.[key]||0),0);return result;}
// Combine complete summary aggregates, never paginated detail rows.
export function combineUsage(pages) {
  const numeric=['input','output','cache','requests'],buckets=new Map();
  const groups=(rows,key)=>{const values=new Map();for(const row of rows){const old=values.get(row[key]);values.set(row[key],{[key]:row[key],...sumRows([old,row],numeric)});}return [...values.values()];};
  for(const {value}of pages)for(const bucket of value.buckets||[]){const old=buckets.get(bucket.date);buckets.set(bucket.date,{date:bucket.date,...sumRows([old,bucket],numeric),agents:groups([...(old?.agents||[]),...(bucket.agents||[])],'agent')});}
  return {total:sumRows(pages.map(page=>page.value.total),numeric),lifetime:sumRows(pages.map(page=>page.value.lifetime),numeric),routing_total:sumRows(pages.map(page=>page.value.routing_total),numeric),buckets:[...buckets.values()].sort((a,b)=>a.date.localeCompare(b.date)),agents:groups(pages.flatMap(page=>page.value.agents||[]),'agent'),models:groups(pages.flatMap(page=>page.value.models||[]),'model'),days:pages[0]?.value.days,timezone:pages[0]?.value.timezone,spaces:pages};
}
