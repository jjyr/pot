import collections, hashlib, json, os, pathlib, subprocess, tarfile, urllib.request, zipfile
SOURCE='f18ef6daefaeb0d14ef29866e29314b845b1d376'
release=json.load(urllib.request.urlopen('https://api.github.com/repos/jjyr/pot/releases/422513414'))
names=['Pot-0.0.1-arm64.zip','pot-0.0.1-linux-arm64.tar.gz','pot-0.0.1-linux-amd64.tar.gz','pot-0.0.1-darwin-arm64.tar.gz']
results=[]
for name in names:
    a=next(x for x in release['assets'] if x['name']==name)
    path=pathlib.Path(os.environ['RUNNER_TEMP'])/name
    subprocess.run(['curl','--fail','--silent','--show-error','--location','--retry','2','--max-time','240','--output',str(path),a['browser_download_url']],check=True)
    with path.open('rb') as f: digest=hashlib.file_digest(f,'sha256').hexdigest()
    assert a['digest']=='sha256:'+digest and path.stat().st_size==a['size']
    iszip=name.endswith('.zip')
    archive=zipfile.ZipFile(path) if iszip else tarfile.open(path,'r:gz')
    files=[]; components=collections.defaultdict(lambda:dict(files=0,bytes=0,compressed_bytes=0)); nodeparts=collections.Counter(); suspicious=[]; readmes=[]; binary=[]
    members=archive.infolist() if iszip else archive.getmembers()
    for m in members:
        p=m.filename if iszip else m.name
        regular=(not m.is_dir() and ((m.external_attr>>16)&0o170000)!=0o120000) if iszip else m.isfile()
        if not regular: continue
        size=m.file_size if iszip else m.size
        compressed=m.compress_size if iszip else None
        parts=p.split('/'); resource=p.split('/Resources/',1)
        if len(resource)==2:
            rest=resource[1]; component='Resources/'+rest.split('/')[0]
            if rest.startswith('node/'):
                rel=rest[len('node/'):]; ns=rel.split('/')
                key='/'.join(ns[:3]) if rel.startswith('lib/node_modules/') else ('bin/node' if rel=='bin/node' else ns[0])
                nodeparts[key]+=size
        elif '/MacOS/' in p: component='MacOS/'+p.rsplit('/',1)[-1]
        elif '/bin/' in p: component='bin/'+p.rsplit('/',1)[-1]
        else: component='/'.join(parts[1:3])
        c=components[component]; c['files']+=1;c['bytes']+=size;c['compressed_bytes']+=compressed or 0
        files.append(dict(path=p,bytes=size,**({'compressed_bytes':compressed} if iszip else {})))
        if any(x in ('.git','pr-images','docs','test','tests','__tests__') for x in parts): suspicious.append(p)
        if pathlib.PurePosixPath(p).name.lower().startswith(('readme','license','copying')): readmes.append(p)
        if p.endswith(('/bin/pot','/MacOS/pot','/MacOS/pot-app','/node/bin/node')):
            tmp=pathlib.Path(os.environ['RUNNER_TEMP'])/'inventory-binary'
            data=archive.read(m) if iszip else archive.extractfile(m).read()
            tmp.write_bytes(data)
            binary.append(dict(path=p,file=subprocess.check_output(['file',str(tmp)],text=True).strip().split(': ',1)[-1]))
            tmp.unlink()
    archive.close()
    result=dict(name=name,id=a['id'],sha256=digest,archive_bytes=a['size'],file_count=len(files),uncompressed_file_bytes=sum(x['bytes'] for x in files),components=dict(sorted(components.items(),key=lambda x:-x[1]['bytes'])),node_breakdown=dict(nodeparts.most_common()),largest_files=sorted(files,key=lambda x:-x['bytes'])[:10],docs_tests_git_paths=suspicious,readme_license_count=len(readmes),readme_license_examples=readmes[:20],binaries=binary)
    if not iszip:
        for c in result['components'].values(): c.pop('compressed_bytes')
    results.append(result);path.unlink();print(name, result['archive_bytes'],result['uncompressed_file_bytes'],flush=True)
pathlib.Path('package-inventory.json').write_text(json.dumps(dict(source=SOURCE,packages=results),indent=2)+'\n')
