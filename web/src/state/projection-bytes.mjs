const encoder=new TextEncoder(),decoder=new TextDecoder();
export const utf8Bytes=text=>encoder.encode(text).length;
// Match the cache's existing UTF-8 JSON body accounting and per-row allowance.
export const messageBytes=message=>utf8Bytes(JSON.stringify(message))+256;
export function truncateUTF8(text,limit,{tail=false}={}){
  const bytes=encoder.encode(text);if(bytes.length<=limit)return text;
  let start=tail?bytes.length-limit:0,end=tail?bytes.length:limit;
  while(start<end&&(bytes[start]&0xc0)===0x80)start++;
  if(!tail)while(end>start&&(bytes[end]&0xc0)===0x80)end--;
  return decoder.decode(bytes.subarray(start,end));
}
