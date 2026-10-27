import assert from 'node:assert/strict';
import {inlineLinks,fileHref} from '../../web/src/state/markdown-links.mjs';
import {lockDialogScroll} from '../../web/src/components/dialog-scroll.mjs';
const source='See [中文](</project/有 空格/a(b).md:12>) and ![图](artifacts/a(b).png) `![code](/no)` \\[escaped](/no).';
assert.deepEqual(inlineLinks(source).map(({href,image})=>({href,image})),[{href:'/project/有 空格/a(b).md:12',image:false},{href:'artifacts/a(b).png',image:true}]);
for(const href of ['relative.md','relative.md:12','/project/a.go:120','pot-file://task/report.md'])assert(fileHref(href),href);
for(const href of ['http:120','https:120','javascript:120','file:120','http://localhost:3000','https://example.com','javascript:alert(1)','#heading','//example.com/x'])assert(!fileHref(href),href);
assert.equal(inlineLinks('[nested [title]](a\\(b\\).md)')[0].href,'a(b).md');
assert.equal(inlineLinks('[title](<path with (spaces).md> "optional title")')[0].href,'path with (spaces).md');
assert.equal(inlineLinks('[title](path.md "optional title")')[0].href,'path.md');
console.log('Explicit Markdown file node syntax and scheme boundaries passed');

// Dialog disposal must restore both horizontal/vertical positions and author
// overflow declarations, including priorities. Repeated close is a no-op.
const previousDocument=globalThis.document;
function surface(parentElement,overflow='',priority=''){
  const declarations=new Map(overflow?[['overflow',{value:overflow,priority}]]:[]);
  return {parentElement,scrollTop:240,scrollLeft:35,scrollHeight:1200,clientHeight:500,scrollWidth:900,clientWidth:500,
    style:{getPropertyValue:key=>declarations.get(key)?.value||'',getPropertyPriority:key=>declarations.get(key)?.priority||'',setProperty:(key,value,priority='')=>declarations.set(key,{value,priority}),removeProperty:key=>declarations.delete(key)}};
}
try{
  const root=surface(null,'auto','important'),history=surface(root,'scroll'),opener=surface(history);
  opener.scrollHeight=opener.clientHeight;opener.scrollWidth=opener.clientWidth;
  globalThis.document={documentElement:root};
  const release=lockDialogScroll(opener);
  assert.equal(root.style.getPropertyValue('overflow'),'hidden');assert.equal(history.style.getPropertyValue('overflow'),'hidden');
  assert.equal(opener.style.getPropertyValue('overflow'),'','The launch control is not made into a scroll container');
  root.scrollTop=0;history.scrollLeft=0;release();
  assert.equal(root.scrollTop,240);assert.equal(history.scrollLeft,35);
  assert.equal(root.style.getPropertyValue('overflow'),'auto');assert.equal(root.style.getPropertyPriority('overflow'),'important');
  assert.equal(history.style.getPropertyValue('overflow'),'scroll');assert.equal(history.style.getPropertyPriority('overflow'),'');
  history.scrollTop=320;release();assert.equal(history.scrollTop,320,'Late close/disconnect cannot replay an old offset');
}finally{if(previousDocument===undefined)delete globalThis.document;else globalThis.document=previousDocument;}
console.log('File viewer scroll lock restores offsets and declarations once');
