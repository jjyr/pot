import assert from 'node:assert/strict';
import {Virtualizer,_resetIOSDetectionForTests} from '../../web/src/vendor/tanstack-virtual-core.mjs';
import {createReadingCursor} from '../../web/src/components/reading-cursor.mjs';

// Exercise real TanStack gesture/compensation state with a deterministic clock.
// No browser physics are simulated: offsets represent browser input.
const originalNavigator=Object.getOwnPropertyDescriptor(globalThis,'navigator');
const originalElement=globalThis.Element;
const originalCSS=globalThis.CSS;
globalThis.Element=class extends EventTarget{};
function fixture(os){
 Object.defineProperty(globalThis,'navigator',{configurable:true,value:{userAgent:os,platform:os,maxTouchPoints:5}});_resetIOSDetectionForTests();
 let time=0,next=0,offsetChanged;const timers=new Map(),frames=new Map(),writes=[];
 const win={performance:{now:()=>time},setTimeout(fn,delay){const id=++next;timers.set(id,{fn,at:time+delay});return id;},clearTimeout:id=>timers.delete(id),requestAnimationFrame(fn){const id=++next;frames.set(id,fn);return id;},cancelAnimationFrame:id=>frames.delete(id)};
 const styles=new Map(),history=new Element();Object.assign(history,{scrollTop:1000,scrollHeight:10000,clientHeight:600,clientWidth:390,ownerDocument:{defaultView:win},getBoundingClientRect:()=>({top:0}),querySelectorAll:()=>[],style:{getPropertyValue:key=>styles.get(key)?.value||'',getPropertyPriority:key=>styles.get(key)?.priority||'',setProperty:(key,value,priority='')=>styles.set(key,{value,priority})}});
 const virtualizer=new Virtualizer({count:100,getScrollElement:()=>history,estimateSize:()=>100,initialOffset:1000,
  observeElementRect(_v,cb){cb({width:390,height:600});return()=>{};},observeElementOffset(_v,cb){offsetChanged=cb;return()=>{};},
  scrollToFn(offset,{adjustments=0}={}){const top=offset+adjustments;if(top!==history.scrollTop){writes.push(top);history.scrollTop=top;}},onChange:()=>{}});
 virtualizer._willUpdate();virtualizer.getMeasurements();
 return {history,virtualizer,writes,timers,
  event(name){const event=new Event(name);Object.defineProperty(event,'touches',{value:name==='touchstart'?[{clientY:200}]:[]});history.dispatchEvent(event);},
  scroll(top){history.scrollTop=top;offsetChanged(top,true);},
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
 // The cursor consumes its bound virtualizer's state rather than timing motion.
 const f=fixture('iPhone'),v=f.virtualizer,commands=[];
 const reading=createReadingCursor(f.history);
 reading.bindScroll({get scrolling(){return v.isTouchScrolling;},cancel:()=>v.cancelScroll(),project:command=>commands.push(command)});
 reading.restore({mode:'reading',position:{top:1000,anchors:[]}});reading.apply();commands.length=0;
 f.event('touchstart');f.scroll(950);reading.observeScroll();reading.apply();reading.layout(()=>{});
 f.event('touchend');f.event('touchstart');reading.reflow();
 assert.deepEqual(commands,[],'continuous touch and layout updates do not restore an old cursor');
 reading.latest();reading.apply();assert.equal(commands.length,1);assert.equal(commands[0].explicit,true,'an explicit Latest command immediately takes priority');
 commands.length=0;f.event('touchend');f.event('scrollend');reading.pause();reading.apply();reading.apply();
 assert.deepEqual(commands,[],'ordinary renders never turn a saved historical anchor into a new scroll command');
 reading.latest();f.scroll(0);reading.observeScroll();
 assert.equal(reading.following,true,'native offsets from hidden/remounted history cannot replace pending Latest restoration');
 reading.dispose();f.close();
 // Width reflow can deliver multiple measurement batches. Retain the requested
 // message offset through those batches, then release it on the next gesture.
 const resized=fixture('iPhone');let rowTop=1000;
 const row={dataset:{scrollAnchor:'message:m',messageId:'m'},getBoundingClientRect:()=>({top:rowTop-resized.history.scrollTop,bottom:rowTop-resized.history.scrollTop+200,height:200})};
 resized.history.querySelectorAll=()=>[row];
 const cursor=createReadingCursor(resized.history),positions=[];
 cursor.bindScroll({scrolling:false,cancel:()=>{},project({top}){positions.push(top);resized.history.scrollTop=top;cursor.acceptScroll();}});
 cursor.restore({mode:'reading',position:{top:1000,anchors:[{key:'message:m',offset:0}]}});cursor.apply();positions.length=0;
 cursor.resize();rowTop+=30;cursor.apply();cursor.commit();rowTop+=20;cursor.apply();
 assert.deepEqual(positions,[1030,1050],'measurement batches keep the original resized reading anchor');
 resized.history.dispatchEvent(new Event('pointerdown'));positions.length=0;rowTop+=10;cursor.apply();
 assert.deepEqual(positions,[],'new input releases width restoration immediately');cursor.dispose();resized.close();
}finally{
 if(originalNavigator)Object.defineProperty(globalThis,'navigator',originalNavigator);else delete globalThis.navigator;
 globalThis.Element=originalElement;_resetIOSDetectionForTests();
 globalThis.CSS=originalCSS;
}
console.log('TanStack touch ownership, rapid handoff, compensation and reading-intent checks passed');
