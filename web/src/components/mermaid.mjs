import {preparedIsland,deferPreparedConnection} from './prepared-render.mjs';
import {translate} from '../locales/messages.mjs';

let pele;
const loadPele=()=>pele??=import('../vendor/pele.mjs').catch(error=>{pele=undefined;throw error;});

// Connection callbacks also cover nested Markdown, collapsed tool results and
// whole-message replacement, without asking each caller to dispose diagrams.
function registerDiagram() {
  if(customElements.get('pot-mermaid'))return;
  customElements.define('pot-mermaid',class extends HTMLElement {
    generation=0;
    connectedCallback() {
      if(this.preparationSurrogate||this.started)return;if(deferPreparedConnection(this,()=>this.connectedCallback()))return;this.started=true;
      const generation=++this.generation;
      const canvas=this.querySelector('.mermaid-canvas'),source=this.querySelector('.mermaid-source');
      const t=key=>translate(this.ownerDocument.documentElement.lang,`markdown.${key}`);
      this.dataset.state='loading';this.setAttribute('aria-busy','true');
      if(!this.closed){canvas.textContent=t('diagramLoading');canvas.setAttribute('role','status');source.open=false;this.preparationTicket?.release();return;}
      const current=()=>this.isConnected&&generation===this.generation;
      const failed=()=>{if(!current())return;this.dataset.state='error';this.removeAttribute('aria-busy');canvas.hidden=false;source.open=true;};
      loadPele().then(({mount})=>{
        if(!this.isConnected||generation!==this.generation)return;
        this.diagram=mount(canvas,this.sourceText,{
          links:false,images:false,limit:20000,outputLimit:1000000,maxEdges:500,
          zoom:{labels:{zoomIn:t('zoomIn'),zoomOut:t('zoomOut'),reset:t('resetZoom')}},
          onRender:()=>{if(!current())return;if(this.dataset.state!=='ready')source.open=false;this.dataset.state='ready';this.removeAttribute('aria-busy');canvas.hidden=false;},
          onError:failed,
        });
      }).catch(()=>{
        if(!this.isConnected||generation!==this.generation)return;
        canvas.textContent=t('diagramUnavailable');failed();
      });
    }
    disconnectedCallback(){queueMicrotask(()=>{if(!this.isConnected)this.retireRendering();});}
    retireRendering(){
      this.preparationTicket?.release();
      if(!this.started)return;this.started=false;
      ++this.generation;this.diagram?.destroy();this.diagram=undefined;
    }
  });
}

export function createMermaidBlock(text,pre,{closed=true,slot=''}={}) {
  registerDiagram();
  const root=document.createElement('pot-mermaid'),canvas=document.createElement('div'),source=document.createElement('details'),summary=document.createElement('summary');
  Object.assign(root,{sourceText:text,closed,renderSlot:slot});root.dataset.unfinished=String(!closed);canvas.className='mermaid-canvas';source.className='mermaid-source';source.open=false;
  summary.textContent=translate(document.documentElement.lang,'markdown.diagramSource');
  source.append(summary,pre);root.append(canvas,source);return preparedIsland(root);
}
