// Reuse the production sidebar activity fixture and its fake read/SSE boundaries.
// Only synthetic directory data changes; no messages, Tasks or approvals are sent.
const params=new URLSearchParams(location.search);
params.set('frame','1');
history.replaceState(null,'',`${location.pathname}?${params}`);
await import('./pot-ui-assistant-activity-browser.mjs');
const f=window.activityFixture;
const until=async test=>{for(let i=0;i<250;i++){if(test())return;await new Promise(resolve=>setTimeout(resolve,20));}throw Error('Unread review fixture timed out');};
const row=(owner,space='one')=>[...document.querySelectorAll('.assistant-row')].find(node=>node.dataset.viewKey===JSON.stringify([space,owner]));
// The selected conversation still shows older history, so its unseen reply keeps
// its existing unread status. The production visibility/read guards decide this.
Object.assign(f.items.one[0],{reply_count:1,reply_id:'unseen-main',preview:'A new reply is waiting beyond the visible history.'});
Object.assign(f.items.one[1],{executing:true,reply_count:3,reply_id:'unseen-research',pending_approvals:2});
Object.assign(f.items.one[2],{name:'Code reviewer',preview:'The review is ready to read.'});
Object.assign(f.items.one[3],{name:'拥有很长名字的研究与开发助手 · Research and development',executing:false,reply_count:12,reply_id:'unseen-long',preview:'Twelve new replies, displayed as 9+.'});
f.items.one.push({id:'quiet',name:'Quiet assistant',phase:'ready',reply_count:0,preview:'Everything has been read.'});
// Load the remote reply while connected, then retain it through a disconnection.
Object.assign(f.items.two[0],{reply_count:2,reply_id:'unseen-remote'});
f.change(['assistants']);f.change(['assistants'],'two');
await until(()=>row('long')?.querySelector('.unread-badge')?.textContent==='9+'&&row('research','two')?.querySelector('.unread-badge'));
const request=f.client.request;
f.client.request=async(path,options)=>{
  const value=await request(path,options);
  if(path==='/api/v1/spaces')value.items[1].connected=false;
  return value;
};
f.change(['spaces'],'two');
await until(()=>f.client.assistants?.find(item=>item.space_id==='two'&&item.id==='research')?.offline);
if(innerWidth<720)[...document.querySelectorAll('button')].find(node=>/返回助手|Back to Assistants/.test(node.getAttribute('aria-label')||''))?.click();
await document.fonts.ready;
await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
window.unreadReview={ready:true,row,fixture:f};
