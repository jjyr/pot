import assert from 'node:assert/strict';
import {Virtualizer,_resetIOSDetectionForTests} from '../../web/src/vendor/tanstack-virtual-core.mjs';
import {createConversationController} from '../../web/src/components/conversation-controller.mjs';

// Exercise real TanStack gesture/compensation state with a deterministic clock.
// No browser physics are simulated: offsets represent browser input.
const originalNavigator=Object.getOwnPropertyDescriptor(globalThis,'navigator');
const originalElement=globalThis.Element;
const originalCSS=globalThis.CSS;
globalThis.Element=class extends EventTarget{};
function fixture(os){
 Object.defineProperty(globalThis,'navigator',{configurable:true,value:{userAgent:os,platform:os,maxTouchPoints:5}});_resetIOSDetectionForTests();
 let time=0,next=0,offsetChanged,controller;const timers=new Map(),frames=new Map(),writes=[];
 const win=Object.assign(new EventTarget(),{performance:{now:()=>time},setTimeout(fn,delay){const id=++next;timers.set(id,{fn,at:time+delay});return id;},clearTimeout:id=>timers.delete(id),requestAnimationFrame(fn){const id=++next;frames.set(id,fn);return id;},cancelAnimationFrame:id=>frames.delete(id)});
 const styles=new Map(),history=new Element();Object.assign(history,{scrollTop:1000,scrollHeight:10000,clientHeight:600,clientWidth:390,ownerDocument:{defaultView:win},getBoundingClientRect:()=>({top:0}),querySelectorAll:()=>[],style:{getPropertyValue:key=>styles.get(key)?.value||'',getPropertyPriority:key=>styles.get(key)?.priority||'',setProperty:(key,value,priority='')=>styles.set(key,{value,priority})}});
 const virtualizer=new Virtualizer({count:100,getScrollElement:()=>history,estimateSize:()=>100,initialOffset:1000,
  observeElementRect(_v,cb){cb({width:390,height:600});return()=>{};},observeElementOffset(_v,cb){offsetChanged=cb;return()=>{};},
  scrollToFn(offset,{adjustments=0}={}){const top=offset+adjustments;if(top!==history.scrollTop){writes.push(top);history.scrollTop=top;controller?.wrote(top);}},onChange:()=>{}});
 virtualizer._willUpdate();virtualizer.getMeasurements();
 return {history,virtualizer,writes,timers,attach:value=>{controller=value;},
  event(name){const event=new Event(name);Object.defineProperty(event,'touches',{value:name==='touchstart'?[{clientY:200}]:[]});history.dispatchEvent(event);},
  scroll(top){history.scrollTop=top;controller?.nativeScroll();offsetChanged(top,true);},
  tick(ms){time+=ms;for(const [id,timer]of [...timers])if(timer.at<=time){timers.delete(id);timer.fn();}},
  frame(){const ready=[...frames.values()];frames.clear();for(const fn of ready)fn();},
  close:()=>virtualizer.cleanup(),
 };
}
try{
 for(const os of ['iPhone','Android']){
  const f=fixture(os),v=f.virtualizer;
  f.event('touchstart');assert.equal(v.isTouchScrolling,true,`${os}: contact owns scrolling before movement`);
  v.resizeItem(0,160);assert.deepEqual(f.writes,[],`${os}: measurement during contact defers native writes`);
  assert.equal(v._holdOffset,60,'deferred correction keeps logical layout aligned');
  f.scroll(950);f.event('touchend');assert.equal(v.isTouchScrolling,true);
  f.tick(80);f.event('touchstart');f.tick(100);
  assert.equal(v.isTouchScrolling,true,'a retired tail timer cannot end the next touch');assert.deepEqual(f.writes,[]);
  f.scroll(1020);f.event('touchcancel');assert.equal(v.isTouchScrolling,true,'native pointer handoff retains momentum protection');
  f.scroll(1070);f.event('scrollend');assert.equal(v.isTouchScrolling,false);
  assert.equal(v._holdOffset,0);assert.deepEqual(f.writes,[1130],'one correction is committed against the latest native offset');
  f.event('touchstart');f.scroll(1050);f.event('touchend');f.tick(150);
  assert.equal(v.isTouchScrolling,false,'quiet timeout is the fallback when scrollend is absent');
  f.event('touchstart');assert.equal(v.isTouchScrolling,true);f.event('touchend');f.tick(150);f.close();
  assert.equal(f.timers.size,0,'disposing retires gesture timers');
 }
 // Explicit navigation can stop an iOS fling. A newer touch must cancel that
 // queued stop immediately, including restoring the native overflow setting.
 globalThis.CSS={supports:()=>true};
 const stop=fixture('iPhone');stop.event('touchstart');stop.scroll(950);stop.event('touchend');
 stop.virtualizer.scrollToEnd();assert.equal(stop.history.style.getPropertyValue('overflow-y'),'hidden');
 stop.event('touchstart');stop.virtualizer.cancelScroll();
 stop.frame();stop.frame();
 assert.equal(stop.history.style.getPropertyValue('overflow-y'),'','new input restores native scrolling immediately');
 assert.equal(stop.virtualizer.scrollState,null);assert.equal(stop.virtualizer.isTouchScrolling,true);
 assert.deepEqual(stop.writes,[],'a cancelled destination cannot replay over the new touch');stop.close();
 // The product controller is tested against the real engine. DOM facts can
 // arrive before or after its measurement cache, independently of user input.
 for(const os of ['iPhone','Android']){
  for(const order of ['dom-first','measure-first']){
   const f=fixture(os),v=f.virtualizer;let actualTop=1000,requested=0;
   const row={closest:()=>null,dataset:{scrollAnchor:'message:m',messageId:'m',sequence:'11'},getBoundingClientRect:()=>({top:actualTop-f.history.scrollTop-v._holdOffset,bottom:actualTop-f.history.scrollTop-v._holdOffset+200,height:200})};
   f.history.querySelectorAll=()=>[row];
   const notifications=[];
   const c=createConversationController({scroller:f.history,virtualizer:v,requestLayout:()=>requested++,scrolled:delta=>notifications.push({delta,published:c.api.snapshot().position?.anchor?.offset,actual:row.getBoundingClientRect().top})});f.attach(c);
   const states=[],off=c.api.subscribeReading(state=>states.push(state));
   const commit=()=>{c.beforeLayout();v.getMeasurements();c.reconcile();c.publish();};
   c.api.restore({mode:'reading',position:{top:1000,anchors:[{key:'message:m',offset:0}],anchor:{id:'m',offset:0,sequence:'11'}}});commit();f.writes.length=0;
   const trace=[];
   if(order==='measure-first')v.resizeItem(0,160);
   actualTop+=60;commit();trace.push({reason:'height +60',top:f.history.scrollTop,offset:row.getBoundingClientRect().top});
   if(order==='dom-first')v.resizeItem(0,160);
   for(let i=0;i<5;i++)commit();
   assert.deepEqual(f.writes,[1060],`${os}/${order}: a late measurement and repeated layouts cannot compensate the same delta twice`);
   assert.equal(trace[0].offset,0);
   actualTop+=30;commit();v.resizeItem(1,130);commit();
   actualTop+=20;v.resizeItem(2,120);commit();
   assert.deepEqual(f.writes,[1060,1090,1110],'independent height changes each receive their own correction');
   // Wheel and keyboard movement is adopted before the next geometry commit.
   for(const kind of ['wheel','keydown']){
    const beforeInput=row.getBoundingClientRect().top;actualTop+=25;
    const event=new Event(kind);Object.assign(event,kind==='wheel'?{deltaY:-36,deltaX:0}:{key:'ArrowUp'});
    f.history.closest=()=>null;f.history.dispatchEvent(event);
    assert.equal(c.api.snapshot().mode,'reading','A pending render cannot expose an older Latest intent to context/cache consumers');
    if(kind==='keydown')c.idle(); // The preceding wheel's idle can arrive first.
    f.scroll(f.history.scrollTop-36);
    v.resizeItem(3,v.getMeasurements()[3].size+25);commit();
    assert.equal(row.getBoundingClientRect().top,beforeInput+36,`${kind}: pending DOM growth is geometry; only the native input delta changes the anchor`);
    if(kind==='keydown'){f.scroll(f.history.scrollTop-4);commit();assert.equal(row.getBoundingClientRect().top,beforeInput+40,'the next native key-tail frame remains input after an older idle notification');}
    const writes=f.writes.length;commit();commit();assert.equal(f.writes.length,writes);
   }
   // Touch corrections use the same model and are held by the engine, rather
   // than a second cursor/timer. Drawing the held offset preserves the anchor.
   f.event('touchstart');const move=new Event('touchmove');Object.defineProperty(move,'touches',{value:[{clientY:220}]});f.history.dispatchEvent(move);f.scroll(f.history.scrollTop-20);
   const touchOffset=row.getBoundingClientRect().top,native=f.history.scrollTop,writes=f.writes.length;
   actualTop+=40;v.resizeItem(4,140);commit();commit();
   assert.equal(f.history.scrollTop,native);assert.equal(f.writes.length,writes);assert.equal(v._holdOffset,40);assert.equal(row.getBoundingClientRect().top,touchOffset);
   v.setOptions({...v.options,onChange:()=>{if(!v.isTouchScrolling)commit();}});
   f.event('touchend');f.event('scrollend');commit();
   assert.equal(f.history.scrollTop,native+40);assert.equal(v._holdOffset,0);assert.equal(row.getBoundingClientRect().top,touchOffset);
   assert.equal(f.writes.length,writes+1,'the held correction flushes once after native touch ownership ends');
   // Layout proximity to the bottom is not user intent to follow.
   f.history.scrollHeight=f.history.scrollTop+f.history.clientHeight;commit();assert.equal(c.api.following,false);
   c.api.locate({id:'m'});const interrupt=new Event('wheel');Object.assign(interrupt,{deltaY:-36,deltaX:0});f.history.dispatchEvent(interrupt);f.scroll(f.history.scrollTop-36);commit();assert.equal(c.api.destination,null,'input interrupts explicit navigation');
   const published=states.at(-1);assert.equal(published.position.anchor.offset,row.getBoundingClientRect().top,'published state reflects the compensated geometry');
   assert.ok(notifications.length>0&&notifications.every(item=>item.published===item.actual),'history boundary callbacks observe the newly committed reading point');
   assert.ok(requested>0);off();c.close();f.close();
  }
 }
}finally{
 if(originalNavigator)Object.defineProperty(globalThis,'navigator',originalNavigator);else delete globalThis.navigator;
 globalThis.Element=originalElement;_resetIOSDetectionForTests();
 globalThis.CSS=originalCSS;
}
console.log('TanStack touch ownership, rapid handoff, compensation and reading-intent checks passed');
