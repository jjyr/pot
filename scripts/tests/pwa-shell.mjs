// Source fixture equivalent of webassets.FS(). Go tests check the embedded tree.
import {readFile,readdir} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {resolve} from 'node:path';
export async function shellAssets(root,transform=(_path,body)=>body){
 const files=new Map();
 async function walk(directory,prefix){for(const entry of (await readdir(directory,{withFileTypes:true})).sort((a,b)=>a.name<b.name?-1:1)){const path=resolve(directory,entry.name),url=prefix+entry.name;if(entry.isDirectory())await walk(path,url+'/');else files.set(url,transform(url,await readFile(path)));}}
 await walk(resolve(root,'web/src'),'/');await walk(resolve(root,'pkg/storage/migrations/ui-cache'),'/pkg/storage/migrations/ui-cache/');await walk(resolve(root,'pkg/storage/migrations/ui-notifications'),'/pkg/storage/migrations/ui-notifications/');
 const worker=files.get('/service-worker.js');files.delete('/service-worker.js');
 const hash=body=>createHash('sha256').update(body).digest('hex');
 const assets=[...files].map(([url,body])=>({url,sha256:hash(body)}));
 const version=hash(Buffer.concat([Buffer.from(JSON.stringify(assets)),worker]));
 files.set('/service-worker.js',Buffer.from(worker.toString().replace('/* POT_SHELL_MANIFEST */ null',JSON.stringify({version,assets}))));
 return {files,version,assets};
}
