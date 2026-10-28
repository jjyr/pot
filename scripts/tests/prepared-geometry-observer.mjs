// External DOM evidence: candidate measurements come from the connected hidden
// surface. This does not change product modules, element prototypes or layout.
export async function observePreparedGeometry(page){
 await page.addInitScript(()=>{
  const identities=new WeakMap();let sequence=0;
  const identity=node=>{if(!identities.has(node))identities.set(node,++sequence);return identities.get(node);};
  const evidence=window.preparedGeometry={rows:{}};
  function sample(){
   for(const row of document.querySelectorAll('[data-preparation-surface] article.message')){
    const nodes=[...row.querySelectorAll('pot-resource,pot-message-file,pot-mermaid,pot-file-image')];
    const pending=nodes.some(node=>!node.closest('[hidden]')&&node.getBoundingClientRect().height&&node.dataset.state==='loading'&&!node.closest('.prepared-frame'));
    if(pending||document.fonts?.status==='loading')continue;
    const box=row.getBoundingClientRect(),style=getComputedStyle(row),key=row.dataset.messageId;
    if(key)evidence.rows[key]={at:performance.now(),instance:identity(row),height:box.height,width:box.width,size:box.height+parseFloat(style.marginTop||0)+parseFloat(style.marginBottom||0),components:nodes.map(node=>({instance:identity(node),kind:node.localName,state:node.dataset.state,frame:node.classList.contains('prepared-frame')}))};
   }
  }
  const observer=new MutationObserver(sample);observer.observe(document,{subtree:true,childList:true,attributes:true,characterData:true});
  const frame=()=>{sample();requestAnimationFrame(frame);};requestAnimationFrame(frame);
 });
}
