// Runs only against TestHistoryBrowserFixture's private Control. No chat send or
// model invocation: the real editor, persistence, cache and shell do the work.
import {avatar} from '../../web/src/components/avatar.mjs';

async function imagePalette(source) {
  const image=new Image();image.src=source;await image.decode();
  if(image.naturalWidth!==128||image.naturalHeight!==128)throw Error('Avatar must be a normalized 128px PNG');
  const canvas=document.createElement('canvas');canvas.width=canvas.height=128;
  const context=canvas.getContext('2d');context.drawImage(image,0,0);
  const {data}=context.getImageData(0,0,128,128),counts=new Map();
  const rgb=i=>`rgb(${data[i]}, ${data[i+1]}, ${data[i+2]})`,background=rgb(0);
  for(let i=0;i<data.length;i+=4){const color=rgb(i);if(color!==background)counts.set(color,(counts.get(color)||0)+1);}
  return [[...counts].sort((a,b)=>b[1]-a[1])[0][0],background];
}


export async function runAvatarRegression(probe) {
  const checks=[],assert=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
  const wait=async read=>{for(let i=0;i<400;i++){const value=read();if(value)return value;await new Promise(resolve=>setTimeout(resolve,20));}throw Error('Timed out: '+read);};
  const button=label=>[...document.querySelectorAll('button')].find(item=>item.textContent.trim()===label||item.getAttribute('aria-label')===label);
  const row=name=>[...document.querySelectorAll('.assistant-row')].find(item=>item.querySelector('.assistant-title')?.textContent===name);
  const fill=(input,value)=>{input.value=value;input.dispatchEvent(new Event('input',{bubbles:true}));};
  const preview=()=>document.querySelector('.editor-fields > .avatar');
  const nameInput=()=>document.querySelector('.editor-form input[required]');
  const directory=()=>probe.client.request('/api/v1/assistants',{owner:''});
  const view=innerWidth<720?'mobile':'desktop';
  const locale=value=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value}}));
  locale('en');
  const before=(await directory()).length;
  const open=async seed=>{
    const original=crypto.randomUUID.bind(crypto);let first=true;
    crypto.randomUUID=()=>{if(first){first=false;return seed;}return original();};
    try{button('New assistant').click();await wait(()=>probe.posts.at(-1)?.request_id===seed);}
    finally{crypto.randomUUID=original;}
  };
  probe.failNext=true;await open('avatar-regression-'+view+'-1');
  await wait(()=>button('New assistant')&&!button('New assistant').disabled);
  assert(!preview(),'Plus never opens a creation editor');
  const failureNotice=document.querySelector('.assistant-creation-error');
  assert(!failureNotice.hidden&&failureNotice.textContent.includes('Could not create'),'Directory exposes a localized creation failure');
  if(document.querySelector('.pot-app').classList.contains('show-directory'))assert(failureNotice.getClientRects().length>0,'Phone directory failure is visible while Chat is hidden');
  assert((await directory()).length===before,'Failed creation leaves the directory intact');
  const failure=probe.posts.at(-1);
  locale('zh-CN');button('新建助手').click();
  await wait(()=>probe.posts.length>=2&&probe.posts.at(-1).request_id===failure.request_id);
  await wait(()=>document.querySelector('.window-title .assistant-identity-name')?.textContent&&document.querySelector('.role-assistant'));
  let saved=await directory(),firstDefinition=saved.find(item=>item.request_id===failure.request_id);
  await wait(()=>document.querySelector('.window-title')?.textContent.includes(firstDefinition.name));
  const first=firstDefinition.avatar;
  assert(probe.posts.at(-1).locale==='en','Retry retains the creation language after a locale switch');
  assert(!firstDefinition.name.includes(' '),'Unused base names have no number');
  assert(document.querySelector('.role-assistant').textContent.includes("Hi, I'm "+firstDefinition.name),'Welcome introduces the allocated name in the original language');
  assert(row(firstDefinition.name)?.querySelector('img')?.src===first,'Directory displays the saved random avatar');
  assert(document.querySelector('.window-title img')?.src===first,'Chat displays the same avatar');
  button('助手设置').click();await wait(preview);
  assert(preview().querySelector('img').src===first,'Settings uses persisted avatar');
  fill(nameInput(),'Avatar '+view+' renamed');
  document.querySelector('details[data-section="instructions"]').open=true;
  const preferences=document.querySelector('textarea[aria-label="个性与偏好"]');fill(preferences,'Please keep replies concise.');
  locale('en');assert(document.querySelector('textarea[aria-label="Personality and preferences"]').value===preferences.value,'Live locale switch preserves personality draft');
  assert(button('Change avatar')&&button('Restore default avatar'),'Settings exposes translated avatar controls');
  document.querySelector('.editor-form').requestSubmit();await wait(()=>!preview());
  await wait(()=>row('Avatar '+view+' renamed'));
  assert(row('Avatar '+view+' renamed').querySelector('img').src===first,'Rename preserves original colors');
  await open('avatar-regression-'+view+'-2');
  await wait(()=>!button('Creating assistant…'));
  saved=await directory();const secondDefinition=saved.find(item=>item.request_id==='avatar-regression-'+view+'-2'),second=secondDefinition.avatar;
  assert(secondDefinition.id!==firstDefinition.id&&second!==first,'New assistant has an independent identity and palette');
  await wait(()=>document.querySelector('.window-title')?.textContent.includes(secondDefinition.name));
  button('Assistant settings').click();await wait(preview);fill(nameInput(),'Avatar '+view+' edited retry');
  const custom=new File([await(await fetch('/web/src/assets/PotCompanion.png')).blob()],'avatar.png',{type:'image/png'}),transfer=new DataTransfer();transfer.items.add(custom);
  const upload=document.querySelector('.editor-form input[type=file]');upload.files=transfer.files;upload.dispatchEvent(new Event('change'));
  await wait(()=>preview().querySelector('img').src!==second);
  const imported=preview().querySelector('img').src;document.querySelector('.editor-form').requestSubmit();await wait(()=>!preview());
  await wait(()=>row('Avatar '+view+' edited retry')?.querySelector('img')?.src===imported);
  button('Assistant settings').click();await wait(preview);button('Restore default avatar').click();
  await wait(()=>preview().querySelector('img').src===second);document.querySelector('.editor-form').requestSubmit();await wait(()=>!preview());
  assert((await directory()).find(item=>item.id===secondDefinition.id).avatar===second,'Restore default preserves original random colors');
  await probe.remount();await wait(()=>row('Avatar '+view+' renamed'));
  assert(row('Avatar '+view+' renamed').querySelector('img').src===first,'Fresh client retains saved colors');
  assert((await directory()).length===before+2,'Only the two intended assistants were created');
  await probe.remount('tray');await wait(()=>row('Avatar '+view+' renamed'));
  assert(row('Avatar '+view+' renamed').querySelector('img').src===first,'Tray uses the same saved identity');
  await probe.remount();await wait(()=>row('Avatar '+view+' renamed'));
  assert(document.documentElement.scrollWidth===innerWidth,'Viewport has no horizontal overflow');
  assert(probe.errors.length===0,'No browser errors');
  return {view,checks,first:await imagePalette(first),second:await imagePalette(second),modelCalls:'No messages sent'};
}

export async function verifyAvatarReload() {
  const view=innerWidth<720?'mobile':'desktop';
  for(const suffix of ['renamed','edited retry']){
    const name='Avatar '+view+' '+suffix;
    for(let i=0;i<400;i++){
      const row=[...document.querySelectorAll('.assistant-row')].find(item=>item.querySelector('.assistant-title')?.textContent===name);
      if(row?.querySelector('img')?.src.startsWith('data:image/png;base64,')){await imagePalette(row.querySelector('img').src);break;}
      if(i===399)throw Error('Reload lost '+name);await new Promise(resolve=>setTimeout(resolve,20));
    }
  }
  return view+' persisted avatars available after full reload';
}
