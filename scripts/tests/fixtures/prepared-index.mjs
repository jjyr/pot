import {createMessageWindow} from '/components/message-window.mjs';
import {currentPreparedRender} from '/components/prepared-render.mjs';
import {createConversationController} from '/components/conversation-controller.mjs';
import {captureReadingPosition} from '/components/reading-position.mjs';
await document.fonts.ready;
const app=document.createElement('div');app.className='pot-app';app.style.cssText='height:690px;display:block';
const scroller=document.createElement('div');scroller.className='history';scroller.style.cssText='height:650px;width:900px;margin:auto';
const column=document.createElement('div');column.className='message-column';const host=document.createElement('div');host.className='message-window';column.append(host);scroller.append(column);app.append(scroller);document.body.append(app);
const initialWidth=Math.min(innerWidth-20,900),nextWidth=Math.min(initialWidth-90,510);scroller.style.width=initialWidth+'px';
const heights=new Map(),prepared=new Map(),ids=new WeakMap(),errors=[],frames=[],events=[],checks=[];let sequence=0,phase='initial',closed=false,maxCandidates=0,ui;
const id=node=>{if(!ids.has(node))ids.set(node,++sequence);return ids.get(node);};
let rows=Array.from({length:320},(_,i)=>({id:'m'+i,viewKey:'m'+i,sequence:i+1,role:i%3?'assistant':'user',text:('Message '+i+' true geometry · '+ 'readable synthetic paragraph '.repeat(3+i%9)+'\n').repeat(1+i%4)}));
function draw(){ui.setMessages(rows);}
ui=createMessageWindow({scroller,host,heights,requestRender:draw,render:(message,existing)=>{const record=existing||{row:document.createElement('article')};record.row.className='message role-'+message.role;record.row.dataset.messageId=message.id;record.row.dataset.sequence=message.sequence;if(record.text!==message.text){record.text=message.text;record.row.textContent=message.text;record.row.style.whiteSpace='pre-wrap';}if(currentPreparedRender()?.context.phase==='prepare')prepared.set(message.id,(prepared.get(message.id)||0)+1);return record;},dispose:record=>record.row.remove()});
function sample(){const v=scroller.getBoundingClientRect();const nodes=[...host.querySelectorAll('article')];return {at:performance.now(),phase,top:scroller.scrollTop,total:scroller.scrollHeight,busy:host.hasAttribute('aria-busy'),heights:heights.size,prepared:prepared.size,landing:ui.landing,reading:ui.snapshot(),captured:captureReadingPosition(scroller),live:nodes.map(node=>{const r=node.getBoundingClientRect();return {instance:id(node),id:node.dataset.messageId,sequence:node.dataset.sequence,held:node.hasAttribute('data-conversation-hold'),top:r.top-v.top,height:r.height,width:r.width,overlap:Math.min(r.bottom,v.bottom)-Math.max(r.top,v.top),measured:heights.get(node.dataset.windowKey),computed:{font:getComputedStyle(node).font,lineHeight:getComputedStyle(node).lineHeight,fontSize:getComputedStyle(node).fontSize},actual:r.height+parseFloat(getComputedStyle(node).marginTop||0)+parseFloat(getComputedStyle(node).marginBottom||0)};}),candidates:document.querySelectorAll('[data-preparation-surface] article').length};}
window.addEventListener('error',event=>errors.push(event.message));window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason)));
function tick(){if(closed)return;const s=sample();maxCandidates=Math.max(maxCandidates,s.candidates);frames.push(s);requestAnimationFrame(tick);}requestAnimationFrame(tick);draw();
const wait=async(predicate,label)=>{const start=performance.now();while(!predicate()){if(performance.now()-start>12000)throw Error('Timeout '+label);await new Promise(requestAnimationFrame);}};
document.querySelector('#run').onclick=async()=>{let failure;try{
await wait(()=>ui.prepared,'initial');checks.push({label:'initial full indexed',sample:sample()});if(heights.size!==320||prepared.size!==320)throw Error('Not all loaded rows prepared before canvas');
if(sample().live.some(row=>Math.abs(row.actual-row.measured)>.1))throw Error('Measured geometry differs from actual committed row');
phase='jump';scroller.dispatchEvent(new WheelEvent('wheel',{deltaY:-100,bubbles:true}));scroller.scrollTop=scroller.scrollHeight/2;events.push({kind:'native-jump-assignment',sample:sample()});await wait(()=>ui.prepared&&sample().live.some(row=>!row.held&&row.overlap>=24),'jump');await new Promise(requestAnimationFrame);checks.push({label:'jump actual target',sample:sample()});
phase='resize';const previous=sample();scroller.style.width=nextWidth+'px';draw();checks.push({label:'resize pending',sample:sample()});await wait(()=>ui.prepared,'resize');checks.push({label:'resize ready',sample:sample()});if(sample().live.some(row=>row.width>nextWidth))throw Error('Resize did not commit new measured width');
phase='change-offscreen';const changed=rows[3];rows=rows.map(row=>row===changed?{...row,text:row.text+'Additional real paragraphs. '.repeat(100)}:row);draw();await wait(()=>ui.prepared,'offscreen revision');checks.push({label:'offscreen revised',sample:sample()});if(heights.size!==320)throw Error('Full index lost entries');
phase='font';scroller.style.fontSize='17.3px';draw();await wait(()=>ui.prepared,'font');checks.push({label:'font ready',sample:sample()});if(sample().live.some(row=>Math.abs(row.actual-row.measured)>.1))throw Error('Font geometry mismatch');
if(frames.some(frame=>frame.heights>0&&frame.heights!==320))throw Error('Incomplete index was published');
if(frames.some(frame=>frame.phase==='jump'&&!frame.live.some(row=>row.overlap>=24)))throw Error('Jump exposed an empty readable frame');
if(frames.some(frame=>frame.live.some(row=>row.held&&frame.captured.anchors.some(anchor=>anchor.key==='message:'+row.id))))throw Error('Temporary hold was captured as reading position');
phase='native-fraction';const nativePosition=captureReadingPosition(scroller);const nativeRow=sample().live.find(row=>row.overlap>=24);nativePosition.anchors=nativePosition.anchors.map(anchor=>({...anchor,offset:anchor.offset-.46875}));nativePosition.anchor={id:nativeRow.id,sequence:nativeRow.sequence,offset:nativeRow.top-.46875};nativePosition.top+=.46875;
ui.restore({mode:'reading',position:nativePosition});draw();await new Promise(requestAnimationFrame);const fractionIntent=ui.snapshot();
for(let pass=0;pass<12;pass++){draw();await new Promise(requestAnimationFrame);}
if(JSON.stringify(ui.snapshot().position)!==JSON.stringify(fractionIntent.position))throw Error('Engine fractional landing was absorbed or accumulated during repeated publication');checks.push({label:'real engine fractional landing',sample:sample()});
const fraction=await fractionalLanding();checks.push({label:'fractional native landing',...fraction});
if(errors.length)throw Error(errors.join(';'));if(maxCandidates>96)throw Error('Unbounded preparation cohort '+maxCandidates);
}catch(error){failure=error.stack;}
const result={failure,checks,events,frames,maxCandidates,errors};window.preparedIndexResult=result;document.querySelector('#result').textContent=JSON.stringify({failure,checks:checks.map(check=>check.sample?({label:check.label,top:check.sample.top,heights:check.sample.heights,prepared:check.sample.prepared,busy:check.sample.busy,live:check.sample.live.length}):check),maxCandidates,errors},null,2);};

async function fractionalLanding(){
 const outer=document.createElement('div'),row=document.createElement('article');outer.style.cssText='height:100px;width:200px;overflow:auto';row.className='message';row.style.cssText='height:1000.46875px;margin:0';row.dataset.messageId='fraction';row.dataset.sequence='1';row.dataset.scrollAnchor='message:fraction';outer.append(row);document.body.append(outer);outer.scrollTop=100;
 const writes=[];let owner;const write=requested=>{outer.scrollTop=requested;writes.push({requested,actual:outer.scrollTop});owner.wrote(outer.scrollTop,requested);};
 const engine={cancelScroll(){},cleanup(){},isScrolling:false,isTouchScrolling:false,scrollToOffset:write,applyScrollAdjustment:delta=>write(outer.scrollTop+delta),getScrollOffset:()=>outer.scrollTop};
 owner=createConversationController({scroller:outer,virtualizer:engine,requestLayout:()=>{}});
 const originalOffset=row.getBoundingClientRect().top-outer.getBoundingClientRect().top-.46875;
 owner.api.restore({mode:'reading',position:{top:100.46875,anchors:[{key:'message:fraction',offset:originalOffset}],anchor:{id:'fraction',offset:originalOffset,sequence:'1'}}});
 owner.reconcile();owner.publish();const first=owner.api.snapshot();
 for(let i=0;i<12;i++){owner.reconcile();owner.publish();}
 if(writes.length!==1||Math.abs(owner.api.position.anchors[0].offset-originalOffset)>1e-7)throw Error('Native fractional landing was retried or erased by publication');
 owner.api.engage(new WheelEvent('wheel',{deltaY:36,bubbles:true}));outer.scrollTop+=36;owner.nativeScroll();owner.reconcile();owner.publish();
 const after=owner.api.snapshot();if(Math.abs(after.position.anchors[0].offset-(originalOffset-36))>1e-7)throw Error('Native input erased the retained fractional anchor');
 const landing=owner.api.landing;owner.close();outer.remove();return {writes,first,after,landing};
}
