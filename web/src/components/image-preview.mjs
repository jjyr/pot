// Original bytes stay in the shared resource repository. Only a bounded raster
// is attached to inline previews; transient decoders and canvas stores are freed.
const waiting=[];let decoding=0;
function acquire(signal){
  return new Promise((resolve,reject)=>{
    const entry={start(){signal?.removeEventListener('abort',abort);decoding++;resolve(()=>{decoding--;pump();});}};
    const abort=()=>{const index=waiting.indexOf(entry);if(index>=0)waiting.splice(index,1);reject(signal.reason||new DOMException('Image retired','AbortError'));};
    if(signal?.aborted){abort();return;}signal?.addEventListener('abort',abort,{once:true});waiting.push(entry);pump();
  });
}
function pump(){while(decoding<2&&waiting.length)waiting.shift().start();}
const check=signal=>{if(signal?.aborted)throw signal.reason||new DOMException('Image retired','AbortError');};
async function decodeURL(url,signal){
  const image=new Image();image.decoding='async';
  try{
    await new Promise((resolve,reject)=>{
      const finish=error=>{image.onload=image.onerror=null;signal?.removeEventListener('abort',abort);error?reject(error):resolve();};
      const abort=()=>{image.removeAttribute('src');finish(signal.reason||new DOMException('Image retired','AbortError'));};
      image.onload=()=>finish();image.onerror=()=>finish(Error('Image decode failed'));
      signal?.addEventListener('abort',abort,{once:true});if(signal?.aborted){abort();return;}image.src=url;
    });
    check(signal);return image;
  }catch(error){image.removeAttribute('src');throw error;}
}
async function animatedRaster(blob,type){
  if(type==='image/gif')return true;
  if(type==='image/webp'){
    if(!blob)return true;
    const head=new Uint8Array(await blob.slice(0,30).arrayBuffer());
    return head.length<21||String.fromCharCode(...head.slice(12,16))==='VP8X'&&!!(head[20]&2);
  }
  if(type==='image/png'){
    if(!blob)return true;
    // APNG declares animation before its first IDAT. Skip ancillary data by
    // length instead of copying the compressed image into an ArrayBuffer.
    let offset=8;
    for(let count=0;count<128&&offset+8<=blob.size;count++){
      const bytes=await blob.slice(offset,offset+8).arrayBuffer(),view=new DataView(bytes);
      const kind=String.fromCharCode(...new Uint8Array(bytes,4,4));
      if(kind==='acTL')return true;if(kind==='IDAT')return false;
      offset+=view.getUint32(0)+12;
    }
    return true;
  }
  return false;
}
export async function createImagePreview({read,url,type,signal,maxEdge=1600}){
  const release=await acquire(signal);let bitmap,image,sourceURL,canvas,originalURL;
  try{
    check(signal);
    let blob=read?await read():null;check(signal);
    if(!blob){originalURL=await url();check(signal);}
    const preserveMotion=await animatedRaster(blob,type||blob?.type);check(signal);
    if(blob&&typeof createImageBitmap==='function'){
      try{bitmap=await createImageBitmap(blob);}catch(error){check(signal);}
    }
    if(!bitmap){sourceURL=blob?URL.createObjectURL(blob):null;image=await decodeURL(sourceURL||originalURL||await url(),signal);}
    check(signal);
    const width=bitmap?.width||image.naturalWidth,height=bitmap?.height||image.naturalHeight;
    if(!width||!height)throw Error('Invalid image dimensions');
    const scale=preserveMotion?1:Math.min(1,maxEdge/Math.max(width,height));let previewWidth=Math.max(1,Math.round(width*scale)),previewHeight=Math.max(1,Math.round(height*scale));
    let preview=blob;
    if(!preview&&preserveMotion)return {url:originalURL,width,height,originalWidth:width,originalHeight:height,release(){}};
    if(scale<1||!preview){
      try{
      canvas=document.createElement('canvas');canvas.width=previewWidth;canvas.height=previewHeight;
      const context=canvas.getContext('2d');context.imageSmoothingQuality='high';context.drawImage(bitmap||image,0,0,previewWidth,previewHeight);
      // PNG preserves transparency and small text without introducing JPEG loss.
      preview=await new Promise((resolve,reject)=>canvas.toBlob(value=>value?resolve(value):reject(Error('Image preview encoding failed')),'image/png'));
      }catch(error){check(signal);preview=blob;previewWidth=width;previewHeight=height;if(!preview)return {url:originalURL,width,height,originalWidth:width,originalHeight:height,release(){}};}
    }
    check(signal);
    const previewURL=URL.createObjectURL(preview);let released=false;
    return {url:previewURL,width:previewWidth,height:previewHeight,originalWidth:width,originalHeight:height,release(){if(released)return;released=true;URL.revokeObjectURL(previewURL);}};
  }finally{bitmap?.close();image?.removeAttribute('src');if(sourceURL)URL.revokeObjectURL(sourceURL);if(canvas)canvas.width=canvas.height=0;release();}
}
