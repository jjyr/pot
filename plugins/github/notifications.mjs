// GitHub owns polling, cursors, filtering and best-effort context expansion.
// Notification snapshots are checkpointed independently of optional detail reads.
import {createHash} from 'node:crypto';
const digest=value=>createHash('sha256').update(JSON.stringify(value)).digest('hex');
const clean=(value,max)=>String(value??'').slice(0,max);
const titleBytes=value=>{let text='';for(const ch of String(value??'')){if(Buffer.byteLength(text+ch)>512)break;text+=ch;}return text;};
class Failure extends Error {constructor(status,delay){super(status);this.status=status;this.delay=delay;}}
class Budget extends Error {}
function validate(config){
 if(!config||typeof config!=='object'||Array.isArray(config)||Object.keys(config).some(k=>!['repositories','reasons'].includes(k)))throw new Failure('overflow',3600);
 for(const key of ['repositories','reasons'])if(config[key]!==undefined&&(!Array.isArray(config[key])||config[key].length>100||config[key].some(v=>typeof v!=='string'||v.length>200)))throw new Failure('overflow',3600);
 if(config.repositories?.some(v=>!/^[-\w.]+\/[-\w.]+$/.test(v)))throw new Failure('overflow',3600);
 if(config.reasons?.some(v=>!/^[-a-zA-Z0-9_]+$/.test(v)))throw new Failure('overflow',3600);
}

export async function poll(input,options={}) {
 const now=input.now,config=input.config||{},account=input.accounts.github;
 // Tests can inject a fake API and clock; production has no configurable origin.
 const base=options.base||'https://api.github.com',fetcher=options.fetch||fetch,clock=options.clock||(()=>performance.now());
 const deadline=clock()+25000;
 let requests=0,detailReads=0;
 if(!account||!process.env.GH_TOKEN)return {state:input.state,events:[],delay_seconds:300,status:'authorization'};
 let state=structuredClone(input.state);
 if(!state||state.account!==account)state={account,threads:{},pending:{},baseline:false,since:null,modified:null,failures:0,scan:null};
 let interval=Math.max(60,state.interval||60);
 const request=async(path,headers={},detail=false)=>{
 const url=new URL(path,base);
 if(url.origin!==new URL(base).origin||!/^\/(notifications|repos\/)/.test(url.pathname))throw new Failure('permission',3600);
 const remaining=deadline-clock();
 if(remaining<=0||requests>=24||detail&&detailReads>=8)throw new Budget();
 requests++;if(detail)detailReads++;
 let response;
 try {response=await fetcher(url,{method:'GET',headers:{Accept:'application/vnd.github+json',Authorization:'Bearer '+process.env.GH_TOKEN,'X-GitHub-Api-Version':'2022-11-28',...headers},redirect:'error',signal:AbortSignal.timeout(Math.max(1,Math.ceil(Math.min(5000,remaining))))});}catch{if(clock()>=deadline)throw new Budget();throw new Failure('backoff',Math.min(3600,60*2**Math.min(6,state.failures||0)));}
 const advertised=Number(response.headers.get('x-poll-interval'));if(Number.isFinite(advertised)&&advertised>0)interval=Math.max(interval,advertised);
 if(response.status===401)throw new Failure('authorization',900);
 if(response.status===429||(response.status===403&&(response.headers.get('x-ratelimit-remaining')==='0'||response.headers.has('retry-after')))){
 const retry=response.headers.get('retry-after'),seconds=Number(retry)||Math.ceil((Date.parse(retry)-now)/1000)||0;
 const reset=Number(response.headers.get('x-ratelimit-reset'))*1000;
 throw new Failure('rate_limited',Math.max(interval,seconds,Math.ceil((reset-now)/1000)||0,60));
 }
 if(response.status===403)throw new Failure('permission',3600);
 if(response.status!==304&&!response.ok)throw new Failure('backoff',300);
 return response;
 };
 const json=async response=>{const text=await response.text();if(Buffer.byteLength(text)>2*1024*1024)throw new Failure('overflow',3600);return JSON.parse(text);};
 const pages=async path=>{
 const rows=[];
 for(let page=0;path;page++){
 if(page>=20)throw new Budget();
 const response=await request(path,{},true),data=await json(response);
 if(!Array.isArray(data)||rows.length+data.length>2000)throw new Failure('overflow',3600);rows.push(...data);
 path=response.headers.get('link')?.match(/<([^>]+)>;\s*rel="next"/)?.[1];
 }
 return rows;
 };
 const flush=()=>{
 const events=[];
 if(state.nextEmitAt>now)return events;
 for(const [key,pending]of Object.entries(state.pending)){
 if(events.length>=8)break;
 if(now-pending.last<60000&&now-pending.first<300000)continue;
 events.push({id:account+':'+digest([key,pending.items]),title:pending.title,body:JSON.stringify({repository:pending.repository,reason:pending.reason,updates:pending.items}),url:pending.url,occurred_at:pending.updated});delete state.pending[key];
 }
 if(events.length)state.nextEmitAt=now+300000;
 return events;
 };
 // Accepted snapshots keep draining even while the upstream feed backs off.
 const events=[];
 const result=(status,delay)=>({state,events:events.length?events:flush(),delay_seconds:Math.min(86400,Math.max(60,Math.ceil(delay))),status});
 if(state.nextPollAt>now)return result(state.retryStatus||'ready',(state.nextPollAt-now)/1000);
 const enrich=[];
 let detailFailure;
 try {
 validate(config);
 const selection=digest([config.repositories||[],config.reasons||[]]);
 if(!state.scan||state.scan.selection!==selection){
 const since=new Date(state.since?Date.parse(state.since)-120000:now-120000).toISOString();
 state.scan={selection,at:now,initial:!state.baseline,index:0,rows:[],feeds:(config.repositories?.length?[...new Set(config.repositories)]:[null]).map(repository=>({repository,path:(repository?'/repos/'+repository:'')+'/notifications?all=true&per_page=100&since='+encodeURIComponent(since),modified:repository?state.repositoryModified?.[repository]:state.modified,paginated:false}))};
 }
 const scan=state.scan;
 let paused=false;
 while(scan.index<scan.feeds.length){
 const feed=scan.feeds[scan.index];
 if(!scan.rows.length){
 if(!feed.path){scan.index++;continue;}
 if(clock()>=deadline||requests>=24){paused=true;break;}
 const response=await request(feed.path,feed.paginated?{}:feed.modified?{'If-Modified-Since':feed.modified}:{});
 const data=response.status===304?[]:await json(response);
 if(!Array.isArray(data)||data.length>2000)throw new Failure('overflow',3600);
 scan.rows=data.filter(thread=>thread&&typeof thread==='object').map(thread=>({id:thread.id,updated_at:thread.updated_at,reason:clean(thread.reason,200),repository:{full_name:thread.repository?.full_name},subject:{title:titleBytes(thread.subject?.title),type:clean(thread.subject?.type,100),url:clean(thread.subject?.url,2048),latest_comment_url:clean(thread.subject?.latest_comment_url,2048)||undefined}}));
 const next=response.headers.get('link')?.match(/<([^>]+)>;\s*rel="next"/)?.[1];
 if(!feed.paginated)feed.modified=response.headers.get('last-modified')||feed.modified;
 if(next)feed.paginated=true;
 feed.path=next||null;
 }
 while(scan.rows.length){
 const thread=scan.rows[0],repo=thread.repository?.full_name,subject=thread.subject||{},id=String(thread.id),updated=thread.updated_at;
 if(!repo||repo.length>200||!/^[-\w.]+\/[-\w.]+$/.test(repo)||!thread.id||!Number.isFinite(Date.parse(updated))){scan.rows.shift();continue;}
 const marker=digest([updated,thread.reason,subject.latest_comment_url]),old=state.threads[id];
 const matched=(!config.repositories?.length||config.repositories.some(r=>r.toLowerCase()===repo.toLowerCase()))&&(!config.reasons?.length||config.reasons.includes(thread.reason));
 if(old?.marker===marker){scan.rows.shift();continue;}
 let subjectURL,match;
 try {subjectURL=new URL(subject.url||'',base);match=subjectURL.origin===new URL(base).origin&&subjectURL.pathname.match(/^\/repos\/([^/]+)\/([^/]+)\/(issues|pulls)\/(\d+)$/);}catch{}
 const expandable=match&&repo===match[1]+'/'+match[2]&&['Issue','PullRequest'].includes(subject.type);
 const key=expandable?repo+'#'+match[4]:repo+'#notification-'+id;
 if(!scan.initial&&matched&&!state.pending[key]&&Object.keys(state.pending).length>=128){
 if(!events.length)events.push(...flush());
 if(Object.keys(state.pending).length>=128){paused=true;break;}
 }
 const record={marker,at:updated,seen:old?.seen||[],latest:subject.latest_comment_url};
 if(!scan.initial&&matched){
 const previous=old||{at:state.since||new Date(now).toISOString(),seen:[]},pending=state.pending[key];
 const item={type:clean(subject.type||'Notification',100),updated,reason:clean(thread.reason,200),changes:[],truncated:['Issue','PullRequest'].includes(subject.type)};
 const items=[...(pending?.items||[]),item];
 while(Buffer.byteLength(JSON.stringify(items))>12000&&items.length>1){items.shift();item.truncated=true;}
 state.pending[key]={title:titleBytes(subject.title)||repo,repository:repo,reason:thread.reason,url:expandable?'https://github.com/'+repo+'/'+(subject.type==='PullRequest'?'pull':'issues')+'/'+match[4]:'https://github.com/notifications?query='+encodeURIComponent('repo:'+repo),updated,first:pending?.first||now,last:now,items};
 if(expandable)enrich.push({repo,subjectURL,number:match[4],subject,previous,record,item,items});
 }
 state.threads[id]=record;scan.rows.shift();
 }
 if(paused)break;
 if(!feed.path){
 state.repositoryModified ||= {};
 const modified=feed.paginated?null:feed.modified||null;
 if(feed.repository)state.repositoryModified[feed.repository]=modified;else state.modified=modified;
 scan.index++;
 }
 }
 if(!paused){state.since=new Date(scan.at).toISOString();state.baseline=true;state.scan=null;}
 // Keep a bounded deduplication window without rolling back accepted snapshots.
 const entries=Object.entries(state.threads).sort((a,b)=>Date.parse(a[1].at)-Date.parse(b[1].at));
 for(const [id]of entries.slice(0,Math.max(0,entries.length-2000)))delete state.threads[id];
 // Detail failures never invalidate the feed cursor or another notification.
 for(const entry of enrich){
 if(clock()>=deadline||detailReads>=8)break;
 const {repo,subjectURL,number,subject,previous,record,item,items}=entry;
 try {
 const info=await json(await request(subjectURL.href,{},true)),issue='/repos/'+repo+'/issues/'+number,since='&since='+encodeURIComponent(previous.at);
 const comments=(await pages(issue+'/comments?per_page=100'+since)).map(c=>({...c,kind:'comment'}));
 if(subject.type==='PullRequest'){
 comments.push(...(await pages('/repos/'+repo+'/pulls/'+number+'/comments?per_page=100'+since)).map(c=>({...c,kind:'review_comment'})));
 comments.push(...(await pages('/repos/'+repo+'/pulls/'+number+'/reviews?per_page=100')).map(c=>({...c,kind:'review'})));
 }
 const seen=[...record.seen],changes=[];
 for(const comment of comments){
 const at=comment.updated_at||comment.submitted_at||comment.created_at;
 if(!at||Date.parse(at)<Date.parse(previous.at))continue;
 const latest=Number(previous.latest?.split('/').at(-1));
 if(Date.parse(at)===Date.parse(previous.at)&&!previous.seen?.length&&(comment.kind!=='comment'||Number.isSafeInteger(latest)&&Number(comment.id)<=latest))continue;
 const fingerprint=digest([comment.kind,comment.id,at,comment.body]);if(seen.includes(fingerprint))continue;
 seen.push(fingerprint);changes.push({kind:comment.kind,author:clean(comment.user?.login,100),at,body:clean(comment.body,3000),url:clean(comment.html_url,2048)});
 }
 Object.assign(item,{author:clean(info.user?.login,100),description:clean(info.body,1200),changes:changes.slice(0,30),truncated:changes.length>30});
 while(Buffer.byteLength(JSON.stringify(items))>12000&&items.length>1){items.shift();item.truncated=true;}
 while(Buffer.byteLength(JSON.stringify(items))>12000&&item.changes.length){item.changes.pop();item.truncated=true;}
 record.seen=seen.slice(-300);
 }catch(error){
 if(error.status==='rate_limited'||error.status==='authorization'){detailFailure=error;break;}
 if(error instanceof Budget)break;
 }
 }
 state.interval=interval;state.failures=0;
 if(detailFailure){const delay=Math.max(interval,detailFailure.delay);state.nextPollAt=now+delay*1000;state.retryStatus=detailFailure.status;return result(detailFailure.status,delay);}
 state.nextPollAt=now+interval*1000;delete state.retryStatus;
 return result(scan.initial&&!paused?'baseline':'ready',Object.keys(state.pending).length||paused?Math.min(interval,60):interval);
 }catch(error){
 // Preserve completed pages and basic notifications; retry only unfinished work.
 if(error instanceof Budget){state.interval=interval;state.failures=0;state.nextPollAt=now+interval*1000;delete state.retryStatus;return result('ready',interval);}
 state.failures=Math.min(8,(state.failures||0)+1);state.interval=interval;
 const delay=Math.max(interval,error.delay||Math.min(3600,60*2**state.failures));
 state.nextPollAt=now+delay*1000;state.retryStatus=error.status||'backoff';
 return result(state.retryStatus,delay);
 }
}
