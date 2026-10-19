// Runs only against TestHistoryBrowserFixture's private Control. No chat send or
// model invocation: the real editor, persistence, cache and shell do the work.
import {avatar,assistantAvatarKey} from '../../web/src/components/avatar.mjs';

export async function runAvatarRegression(probe) {
  const checks=[],assert=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
  const wait=async(read)=>{for(let i=0;i<300;i++){const value=read();if(value)return value;await new Promise(resolve=>setTimeout(resolve,20));}throw Error('Timed out: '+read);};
  const colors=element=>[element.querySelector('.avatar-mark').style.background,element.style.background];
  const painted=async element=>{await wait(()=>element?.style.background);return colors(element);};
  const equal=(actual,expected,message)=>assert(JSON.stringify(actual)===JSON.stringify(expected),message+': '+JSON.stringify(actual));
  const button=label=>[...document.querySelectorAll('button')].find(item=>item.textContent.trim()===label||item.getAttribute('aria-label')===label);
  const fill=(input,value)=>{input.value=value;input.dispatchEvent(new Event('input',{bubbles:true}));};
  const nameInput=()=>document.querySelector('.editor-form input[required]');
  const preview=()=>document.querySelector('.editor-fields > .avatar');
  const row=name=>[...document.querySelectorAll('.assistant-row')].find(item=>item.querySelector('.assistant-title')?.textContent===name);
  const directory=()=>probe.client.request('/api/v1/assistants',{owner:''});
  const view=innerWidth<720?'mobile':'desktop';
  const expected=view==='desktop'?[['rgb(104, 80, 131)','rgb(245, 221, 210)'],['rgb(139, 74, 104)','rgb(220, 233, 226)']]:[['rgb(90, 111, 60)','rgb(227, 232, 217)'],['rgb(58, 101, 144)','rgb(245, 221, 210)']];
  const open=async seed=>{
    const original=crypto.randomUUID.bind(crypto);let first=true;
    crypto.randomUUID=()=>{if(first){first=false;return seed;}return original();};
    try{(await wait(()=>document.querySelector('.directory-heading button'))).click();await wait(preview);await painted(preview());}
    finally{crypto.randomUUID=original;}
  };
  const submit=()=>document.querySelector('.editor-form').requestSubmit();
  const checkChat=async(name,want)=>{
    await wait(()=>row(name));
    equal(await painted(row(name).querySelector('.avatar')),want,'Authoritative directory '+name);
    // The row may be hidden by narrow navigation; click exercises the same handler.
    row(name).click();
    await wait(()=>document.querySelector('.window-title').textContent===name);
    equal(await painted(document.querySelector('.window-title .avatar')),want,'Chat title '+name);
  };
  await wait(()=>document.querySelector('.directory-heading button'));
  const before=(await directory()).length;
  await open('avatar-regression-'+view+'-cancel');
  button('Cancel').click();await wait(()=>!preview());
  assert((await directory()).length===before,'Cancel creates no assistant');
  await open('avatar-regression-'+view+'-1');
  const first=await painted(preview());equal(first,expected[0],'Fixed preview palette');
  fill(nameInput(),'Avatar '+view+' first');
  // A shell rerender and live language change must retain this editor/appearance.
  window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value:'zh-CN'}}));
  await wait(()=>document.documentElement.lang==='zh-CN');
  equal(await painted(preview()),first,'Locale rerender retains preview');
  window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value:'en'}}));
  await wait(()=>document.documentElement.lang==='en');
  probe.failNext=true;submit();
  await wait(()=>document.querySelector('.editor-form [role=alert]')?.textContent.includes('Deliberate creation failure'));
  equal(await painted(preview()),first,'Failed creation retains preview');
  const failed=probe.posts.at(-1);submit();
  await wait(()=>row('Avatar '+view+' first'));
  const firstPost=probe.posts.at(-1);
  assert(firstPost.request_id===failed.request_id&&firstPost.request_id==='avatar-regression-'+view+'-1','Unchanged retry payload retains preview seed');
  await checkChat('Avatar '+view+' first',first);
  const saved=(await directory()).find(item=>item.request_id===firstPost.request_id);
  assert(!!saved,'Persisted list returns creation payload seed');
  button('Assistant settings').click();await wait(preview);
  equal(await painted(preview()),first,'Settings uses persisted seed');
  fill(nameInput(),'Avatar '+view+' renamed');submit();await wait(()=>!preview());
  await checkChat('Avatar '+view+' renamed',first);
  await open('avatar-regression-'+view+'-2');
  const second=await painted(preview());equal(second,expected[1],'Independent second preview');
  fill(nameInput(),'Avatar '+view+' second');probe.failNext=true;submit();
  await wait(()=>document.querySelector('.editor-form [role=alert]')?.textContent.includes('Deliberate creation failure'));
  const secondFailure=probe.posts.at(-1);
  fill(nameInput(),'Avatar '+view+' edited retry');submit();
  await wait(()=>row('Avatar '+view+' edited retry'));
  assert(probe.posts.at(-1).request_id===secondFailure.request_id,'Edited retry payload retains original preview seed');
  await checkChat('Avatar '+view+' edited retry',second);
  assert(JSON.stringify(first)!==JSON.stringify(second),'Assistants have independent colors');
  // Recreate client and shell: fresh directory and empty in-memory resource cache.
  await probe.remount();await wait(()=>row('Avatar '+view+' renamed'));
  await checkChat('Avatar '+view+' renamed',first);
  await checkChat('Avatar '+view+' edited retry',second);
  assert((await directory()).length===before+2,'Only two intended assistants persisted');
  const fallback=avatar(32,assistantAvatarKey({id:'legacy-stable',name:'Old'}));
  const renamedFallback=avatar(32,assistantAvatarKey({id:'legacy-stable',name:'New'}));
  equal(await painted(fallback),await painted(renamedFallback),'Legacy missing-seed fallback survives rename');
  assert(assistantAvatarKey(null)==='main','Empty fallback stays deterministic');
  await probe.remount('tray');await wait(()=>row('Avatar '+view+' renamed'));
  equal(await painted(row('Avatar '+view+' renamed').querySelector('.avatar')),first,'Shared tray directory uses persisted seed');
  await probe.remount();await wait(()=>row('Avatar '+view+' renamed'));
  assert(document.documentElement.scrollWidth===innerWidth,'Viewport has no horizontal overflow');
  assert(probe.errors.length===0,'No browser errors');
  return {view,checks,first,second,modelCalls:'No messages sent'};
}

// Called after a full iframe document reload: no palette or resource cache from
// creation survives. Verify real DOM colors against the original fixed palette.
export async function verifyAvatarReload() {
  const view=innerWidth<720?'mobile':'desktop';
  const expected=view==='desktop'?[['rgb(104, 80, 131)','rgb(245, 221, 210)'],['rgb(139, 74, 104)','rgb(220, 233, 226)']]:[['rgb(90, 111, 60)','rgb(227, 232, 217)'],['rgb(58, 101, 144)','rgb(245, 221, 210)']];
  for(const [index,suffix]of ['renamed','edited retry'].entries()){
    const name='Avatar '+view+' '+suffix;
    let actual;
    for(let i=0;i<300;i++){
      const row=[...document.querySelectorAll('.assistant-row')].find(item=>item.querySelector('.assistant-title')?.textContent===name);
      const avatar=row?.querySelector('.avatar');
      if(avatar?.style.background){actual=[avatar.querySelector('.avatar-mark').style.background,avatar.style.background];break;}
      await new Promise(resolve=>setTimeout(resolve,20));
    }
    if(JSON.stringify(actual)!==JSON.stringify(expected[index]))throw Error('Reload changed '+name+': '+JSON.stringify(actual));
  }
  return view+' full document reload: both original palettes retained';
}
