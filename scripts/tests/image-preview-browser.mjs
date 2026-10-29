import assert from 'node:assert/strict';
import {deflateSync} from 'node:zlib';
import {readFile} from 'node:fs/promises';
const pngChunk=(name,data)=>{const type=Buffer.from(name),length=Buffer.alloc(4),crc=Buffer.alloc(4);length.writeUInt32BE(data.length);let sum=0xffffffff;for(const byte of Buffer.concat([type,data])){sum^=byte;for(let bit=0;bit<8;bit++)sum=(sum>>>1)^((sum&1)?0xedb88320:0);}crc.writeUInt32BE((sum^0xffffffff)>>>0);return Buffer.concat([length,type,data,crc]);};
const pngHeader=Buffer.alloc(13);pngHeader.writeUInt32BE(32);pngHeader.writeUInt32BE(16,4);pngHeader[8]=8;pngHeader[9]=6;
const animation=Buffer.alloc(8);animation.writeUInt32BE(2);
const frameControl=sequence=>{const bytes=Buffer.alloc(26);bytes.writeUInt32BE(sequence);bytes.writeUInt32BE(32,4);bytes.writeUInt32BE(16,8);bytes.writeUInt16BE(1,20);bytes.writeUInt16BE(10,22);return pngChunk('fcTL',bytes);};
const pixels=color=>{const rows=Buffer.alloc(16*(1+32*4));for(let y=0;y<16;y++)for(let x=0;x<32;x++){const offset=y*(1+32*4)+1+x*4;rows[offset+color]=255;rows[offset+3]=255;}return deflateSync(rows);};
const sequence=Buffer.alloc(4);sequence.writeUInt32BE(2);
const apng=Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),pngChunk('IHDR',pngHeader),pngChunk('acTL',animation),frameControl(0),pngChunk('IDAT',pixels(0)),frameControl(1),pngChunk('fdAT',Buffer.concat([sequence,pixels(2)])),pngChunk('IEND',Buffer.alloc(0))]).toString('base64');
import {sourceServer,launchBrowser} from './browser-harness.mjs';
const policySource=await readFile(new URL('../../internal/webui/server.go',import.meta.url),'utf8'),policy=JSON.parse(policySource.match(/Set\("Content-Security-Policy", ("[^"\n]+")\)/)[1]);
const server=await sourceServer(),browser=await launchBrowser();
try{
 for(const locale of ['en','zh-CN']){
  const page=await browser.newPage({viewport:{width:locale==='en'?1280:390,height:800}}),errors=[];page.on('pageerror',error=>errors.push(error.message));
  try{
   await page.route('**/scripts/tests/fixtures/prepared-empty.html',async route=>{const response=await route.fetch();await route.fulfill({response,headers:{...response.headers(),'content-security-policy':policy}});});
   await page.goto(server.origin+'/scripts/tests/fixtures/prepared-empty.html');
   const result=await page.evaluate(async({locale,apng})=>{
    document.documentElement.lang=locale;
    const {createResourceView}=await import('/components/resource-view.mjs'),{createImagePreview}=await import('/components/image-preview.mjs');
    const sheet=document.createElement('link');sheet.rel='stylesheet';sheet.href='/styles.css';document.head.append(sheet);await new Promise(resolve=>sheet.onload=resolve);
    const check=(value,message)=>{if(!value)throw Error(message);};
    const until=async condition=>{const end=performance.now()+15000;while(!condition()){check(performance.now()<end,'Image preview timed out');await new Promise(resolve=>setTimeout(resolve,20));}};
    const canvas=document.createElement('canvas');canvas.width=4096;canvas.height=2048;const context=canvas.getContext('2d');context.fillStyle='#4b88aa';context.fillRect(0,0,4096,2048);context.fillStyle='white';context.font='160px sans-serif';context.fillText('Original image',100,300);
    const blob=await new Promise(resolve=>canvas.toBlob(resolve));canvas.width=canvas.height=0;
    const createURL=URL.createObjectURL.bind(URL),revokeURL=URL.revokeObjectURL.bind(URL),urls=new Map();URL.createObjectURL=blob=>{const url=createURL(blob);urls.set(url,blob);return url;};URL.revokeObjectURL=url=>{urls.delete(url);revokeURL(url);};
    let urlReads=0,released=0,downloads=0,originalTarget;const original=URL.createObjectURL(blob),resource={id:'image-preview',name:'image.png',entry:'image.png',mime_type:'image/png',size:blob.size,files:[{path:'image.png'}]};
    const view=createResourceView({resource,mode:'preview'},{acquireResource:()=>({read:async()=>blob,url:async()=>{urlReads++;return originalTarget||original;},release(){released++;}}),downloadResource:async()=>downloads++});document.body.append(view);
    await until(()=>view.dataset.state==='ready');const image=view.querySelector('img');
    check(image.naturalWidth===1600&&image.naturalHeight===800,'inline image uses bounded raster');check(urlReads===0,'original URL requested only for modal');
    image.click();await until(()=>document.querySelector('.image-viewer img')?.naturalWidth===4096);
    check(urlReads===1,'viewer uses original URL');check(document.querySelector('.image-viewer img').naturalHeight===2048,'viewer preserves original height');
    document.querySelector('.file-image-download').click();await until(()=>downloads===1);document.querySelector('.file-image-close').click();check(!document.querySelector('.image-viewer'),'viewer closes');
    originalTarget=URL.createObjectURL(new Blob(['invalid image'],{type:'image/png'}));image.click();await until(()=>document.querySelector('.image-viewer-error')&&!document.querySelector('.image-viewer-error').hidden);
    check(!document.querySelector('.image-viewer-viewport').hasAttribute('aria-busy'),'original load failure clears busy state');check(view.dataset.state==='ready','failed original preserves inline preview');document.querySelector('.file-image-close').click();URL.revokeObjectURL(originalTarget);originalTarget=null;
    const previewURL=image.src;view.remove();await until(()=>released===1);check(!urls.has(previewURL),'inline URL revoked on disconnect');check(!image.hasAttribute('src'),'inline decoded image released');
    const {renderMessageAttachments}=await import('/components/attachments.mjs');let attachmentReads=0,attachmentURLs=0,attachmentReleases=0;
    const attachments=renderMessageAttachments([{id:'upload',name:'upload.png',mime_type:'image/png',size:blob.size}],{client:{retainAttachment:()=>()=>attachmentReleases++,attachmentBlob:async()=>{attachmentReads++;return blob;},attachmentURL:async()=>{attachmentURLs++;return original;},downloadAttachment:async()=>{}},scope:{space:'synthetic',owner:'synthetic'},locale:()=>locale});document.body.append(attachments);
    await until(()=>attachments.querySelector('pot-resource')?.dataset.state==='ready');check(attachments.querySelector('img').naturalWidth===1600&&attachmentReads===1&&attachmentURLs===0,'uploaded attachments use shared Blob without original image URL');attachments.remove();await until(()=>attachmentReleases===1);
    // Build a real animated WebP from two canvas-encoded VP8 frames. Keeping
    // the original bytes preserves animation rather than freezing frame one.
    const motionCanvas=document.createElement('canvas');motionCanvas.width=32;motionCanvas.height=16;
    const frameBytes=[];for(const color of ['red','blue']){motionCanvas.getContext('2d').fillStyle=color;motionCanvas.getContext('2d').fillRect(0,0,32,16);const frame=await new Promise(resolve=>motionCanvas.toBlob(resolve,'image/webp'));if(frame.type==='image/webp'){const data=new Uint8Array(await frame.arrayBuffer()),parts=[];for(let offset=12;offset+8<=data.length;){const size=new DataView(data.buffer).getUint32(offset+4,true),name=String.fromCharCode(...data.slice(offset,offset+4));if(['VP8 ','VP8L','ALPH'].includes(name))parts.push(data.slice(offset,offset+8+size+(size%2)));offset+=8+size+(size%2);}const body=new Uint8Array(parts.reduce((sum,part)=>sum+part.length,0));let offset=0;for(const part of parts){body.set(part,offset);offset+=part.length;}frameBytes.push(body);}}
    if(frameBytes.length===2){
      const concat=parts=>{const bytes=new Uint8Array(parts.reduce((sum,part)=>sum+part.length,0));let offset=0;for(const part of parts){bytes.set(part,offset);offset+=part.length;}return bytes;};
      const chunk=(name,data)=>{const head=new Uint8Array(8);head.set(new TextEncoder().encode(name));new DataView(head.buffer).setUint32(4,data.length,true);return concat([head,data,...(data.length%2?[new Uint8Array(1)]:[])]);};
      const header=new Uint8Array(10);header[0]=2;header[4]=31;header[7]=15;
      const frames=frameBytes.map(bytes=>{const frame=new Uint8Array(16);frame[6]=31;frame[9]=15;frame[12]=100;return chunk('ANMF',concat([frame,bytes]));});
      const content=concat([new TextEncoder().encode('WEBP'),chunk('VP8X',header),chunk('ANIM',new Uint8Array(6)),...frames]),riff=new Uint8Array(8);riff.set(new TextEncoder().encode('RIFF'));new DataView(riff.buffer).setUint32(4,content.length,true);
      const animated=new Blob([riff,content],{type:'image/webp'}),motion=await createImagePreview({read:async()=>animated,url:async()=>original,maxEdge:8});
      check(motion.width===32&&motion.height===16,'animated WebP is not resized into a still frame');check(urls.get(motion.url).size===animated.size,'animated WebP original bytes preserved');motion.release();
    }
    motionCanvas.width=motionCanvas.height=0;
    const animatedPNG=new Blob([Uint8Array.from(atob(apng),char=>char.charCodeAt(0))],{type:'image/png'}),apngPreview=await createImagePreview({read:async()=>animatedPNG,url:async()=>original,maxEdge:8});
    check(apngPreview.width===32&&apngPreview.height===16,'APNG is not resized into a still frame');check(urls.get(apngPreview.url).size===animatedPNG.size,'APNG original bytes preserved');apngPreview.release();
    const bitmap=globalThis.createImageBitmap;globalThis.createImageBitmap=undefined;
    const fallback=await createImagePreview({read:async()=>blob,url:async()=>original});check(fallback.width===1600&&fallback.originalWidth===4096,'image-element fallback bounds preview');fallback.release();const urlOnly=await createImagePreview({url:async()=>original,type:'image/png'});check(urlOnly.url===original&&urlOnly.originalWidth===4096,'URL-only PNG keeps authorized source without blob fetch or freezing animation');urlOnly.release();globalThis.createImageBitmap=bitmap;
    if(bitmap){
      let active=0,maximum=0;globalThis.createImageBitmap=async(...args)=>{active++;maximum=Math.max(maximum,active);try{await new Promise(resolve=>setTimeout(resolve,60));return await bitmap(...args);}finally{active--;}};
      const abort=new AbortController();const pending=Array.from({length:5},(_,index)=>createImagePreview({read:async()=>blob,url:async()=>original,signal:index===4?abort.signal:undefined}));abort.abort();
      const settled=await Promise.allSettled(pending);check(maximum<=2,'at most two concurrent decoders');check(settled[4].status==='rejected'&&settled[4].reason.name==='AbortError','queued cancellation rejects');for(const value of settled)if(value.status==='fulfilled')value.value.release();globalThis.createImageBitmap=bitmap;
    }
    URL.revokeObjectURL(original);check(urls.size===0,'all derived and temporary URLs released');URL.createObjectURL=createURL;URL.revokeObjectURL=revokeURL;
    return {originalPixels:4096*2048,inlinePixels:1600*800,urlReads,downloads};
   },{locale,apng});
   assert.deepEqual(errors,[]);console.log(JSON.stringify({locale,...result,passed:true}));
  }finally{await page.close();}
 }
}finally{await browser.close();await server.close();}
