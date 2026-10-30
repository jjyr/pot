import {sourceServer,launchBrowser} from './browser-harness.mjs';

const server=await sourceServer();let browser;
try{
  browser=await launchBrowser();
  const page=await browser.newPage(),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  try{
    await page.goto(server.origin+'/scripts/tests/fixtures/prepared-empty.html');
    const results=await page.evaluate(async()=>{
      const {createInitialChatLayout}=await import('/components/initial-chat-layout.mjs');
      const results=[];
      const check=(value,label)=>{if(!value)throw Error(label);results.push(label);};
      const frame=()=>new Promise(resolve=>requestAnimationFrame(resolve));
  {
    const history=document.createElement('div');
    history.className='history';
    history.style.visibility='hidden';
    history.style.width='320px';
    history.style.height='240px';
    document.body.append(history);
    await document.fonts.ready;
    let layoutCount=0,revealCount=0,readyFlag=true;
    const readyFn=()=>readyFlag;
    let synth=null;
    const layoutFn=()=>{layoutCount++; if(synth) synth.preparedCommit();};
    const revealFn=()=>{revealCount++; history.style.visibility='visible';};
    synth=createInitialChatLayout({history,layout:layoutFn,ready:readyFn,reveal:revealFn,timeout:100000});
    try {
      // (a) start->preparedCommit->cancel: no layout/reveal.
      synth.start();
      synth.preparedCommit();
      synth.cancel();
      await Promise.resolve();
      await frame();
      await frame();
      check(layoutCount===0,'(a) preparedCommit+cancel did not layout');
      check(revealCount===0,'(a) preparedCommit+cancel did not reveal');
      // (b) restart->preparedCommit->cancel->start: stale kick must not layout before RAF.
      layoutCount=0;revealCount=0;
      synth.start();
      synth.preparedCommit();
      synth.cancel();
      synth.start();
      await Promise.resolve();
      check(layoutCount===0,'(b) stale kick did not layout before RAF');
      synth.cancel();
      // (c) ready=false start/kick: no reveal; then ready=true duplicate preparedCommit with guard.
      layoutCount=0;revealCount=0;readyFlag=false;
      synth.start();
      synth.preparedCommit();
      await Promise.resolve();
      await frame();
      check(revealCount===0,'(c) ready=false kick did not reveal');
      readyFlag=true;
      const beforeReveal=revealCount;
      synth.preparedCommit();
      synth.preparedCommit();
      await Promise.resolve();
      check(layoutCount===2,'(c) duplicate preparedCommit laid out exactly twice');
      check(revealCount===beforeReveal,'(c) duplicate preparedCommit did not reveal before frame');
      await frame();
      check(revealCount===beforeReveal+1,'(c) revealed exactly once after frame');
      // (d) after reveal, repeated kicks and frames must not layout again.
      const countAtReveal=layoutCount;
      synth.preparedCommit();
      synth.preparedCommit();
      await Promise.resolve();
      await frame();
      await frame();
      check(layoutCount===countAtReveal,'(d) no kicks after revealed');
    } finally {
      synth.cancel();
      history.remove();
    }
  }

      return results;
    });
    if(errors.length)throw Error('Page errors: '+errors.join('; '));
    console.log(JSON.stringify({passed:true,results}));
  }finally{await page.close();}
}finally{await browser?.close();await server.close();}
