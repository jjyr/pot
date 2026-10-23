async page => {
  const check=(value,message)=>{if(!value)throw Error(message);};
  const input=page.locator('#pot-composer'),value=()=>page.evaluate(()=>window.composerSelectorValue());
  const local='[@研究助手](pot-assistant:local/research)',remote='[@研究助手](pot-assistant:remote/research)';
  const shortcut=await page.evaluate(()=>navigator.platform.includes('Mac')?'Meta':'Control');
  // A script assignment starts a fresh native undo history. WebKit's trusted
  // fill can otherwise group these edits with the preceding fixture scenario.
  await input.evaluate(editor=>{editor.value='';editor.dispatchEvent(new Event('input',{bubbles:true}));});
  async function select(query,index=0){
    await input.fill(query);await page.locator('.completion-option').nth(index).waitFor();
    await page.locator('.completion-option').nth(index).click();
  }
  await select('前文 @');
  check(await input.inputValue()==='前文 @研究助手 ','Native textarea contains only the compact display name');
  check(await value()==='前文 '+local+' ','Completion retains the full node address');
  await input.pressSequentially('后文');
  check(await value()==='前文 '+local+' 后文','Trusted typing preserves the reference');
  await input.press(shortcut+'+z');
  check(['前文 '+local+' ','前文 '+local+' 后','前文 @',''].includes(await value()),'Native undo restores an earlier text/identity state: '+await value());
  await input.press(shortcut+'+Shift+z');
  check(await value()==='前文 '+local+' 后文','Native redo restores both text and identity');
  // Copy/paste goes through the browser clipboard event contract without using
  // the machine clipboard, which may contain private user data.
  const copied=await input.evaluate(editor=>{
    editor.setSelectionRange(3,8);const clipboardData=new DataTransfer();
    editor.dispatchEvent(new ClipboardEvent('copy',{clipboardData,bubbles:true,cancelable:true}));
    return clipboardData.getData('text/plain');
  });
  check(copied===local,'Copying the complete label retains its address');
  const paste=async text=>input.evaluate((editor,text)=>{
    const clipboardData=new DataTransfer();clipboardData.setData('text/plain',text);
    editor.dispatchEvent(new ClipboardEvent('paste',{clipboardData,bubbles:true,cancelable:true}));
  },text);
  await paste(remote);
  check(await value()==='前文 '+remote+' 后文','Replacing a same-name label changes only its address');
  await input.press(shortcut+'+z');
  check(await value()==='前文 '+local+' 后文','Undo restores the original same-name address');
  await input.press(shortcut+'+Shift+z');
  check(await value()==='前文 '+remote+' 后文','Redo restores the replacement same-name address');
  await input.press(shortcut+'+z');
  await input.evaluate(editor=>editor.setSelectionRange(editor.value.length,editor.value.length));
  await paste(' '+remote);
  check(await value()==='前文 '+local+' 后文 '+remote,'Pasted same-name assistant keeps its distinct node: '+await value());
  check(!(await input.inputValue()).includes('pot-assistant:'),'Pasted addresses remain hidden in the editor');
  await input.press('Backspace');
  check(await value()==='前文 '+local+' 后文 ','Backspace removes the whole last reference: '+await value());
  await input.press(shortcut+'+z');
  // WebKit can group the controlled paste and following deletion. Both native
  // undo boundaries must restore the exact addresses of all remaining labels.
  check(['前文 '+local+' 后文 '+remote,'前文 '+local+' 后文'].includes(await value()),'Undo restores an earlier reference state with the correct nodes: '+await value());
  await input.press(shortcut+'+Shift+z');
  check(await value()==='前文 '+local+' 后文 ','Redo removes only that reference');
  await input.evaluate(editor=>editor.setSelectionRange(3,3));await input.press('Delete');
  check(await value()==='前文  后文 ','Forward delete removes a complete reference');
  await input.press(shortcut+'+z');
  check(await value()==='前文 '+local+' 后文 ','Undo restores the forward-deleted reference');
  await input.evaluate(editor=>editor.setSelectionRange(5,5));await input.pressSequentially('改');
  check(await value()==='前文 改 后文 ','Editing inside a label replaces the reference without retaining its identity');
  await input.press(shortcut+'+z');
  check(await value()==='前文 '+local+' 后文 ','Undo of a label edit restores its identity');
  // Long wrapped drafts exercise mirror geometry at the native height cap.
  await input.evaluate(editor=>editor.setSelectionRange(editor.value.length,editor.value.length));
  await input.pressSequentially('\n'+('长文本测试 '.repeat(80)));
  await input.evaluate(editor=>{editor.scrollTop=editor.scrollHeight;editor.dispatchEvent(new Event('scroll'));});
  const geometry=await input.evaluate(editor=>{
    const layer=editor.previousElementSibling,a=editor.getBoundingClientRect(),b=layer.getBoundingClientRect();
    return {height:editor.clientHeight,scroll:editor.scrollTop,mirrorScroll:layer.scrollTop,left:a.left-b.left,top:a.top-b.top,width:editor.clientWidth-layer.clientWidth};
  });
  check(geometry.height<=150&&geometry.scroll>0&&Math.abs(geometry.scroll-geometry.mirrorScroll)<=1,'Long drafts keep the highlight layer at the native scroll position');
  check(Math.abs(geometry.left)<1&&Math.abs(geometry.top)<1&&geometry.width===0,'Highlight and textarea geometry stay aligned');
  return {nativeEditing:true,geometry};
}
