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
      const generation=++this.generation;
      const canvas=this.querySelector('.mermaid-canvas'),source=this.querySelector('.mermaid-source');
      const t=key=>translate(this.ownerDocument.documentElement.lang,`markdown.${key}`);
      this.dataset.state='loading';this.setAttribute('aria-busy','true');
      const failed=()=>{this.dataset.state='error';this.removeAttribute('aria-busy');canvas.hidden=false;source.open=true;};
      loadPele().then(({mount})=>{
        if(!this.isConnected||generation!==this.generation)return;
        this.diagram=mount(canvas,this.sourceText,{
          links:false,images:false,limit:20000,outputLimit:1000000,maxEdges:500,
          zoom:{labels:{zoomIn:t('zoomIn'),zoomOut:t('zoomOut'),reset:t('resetZoom')}},
          onRender:()=>{if(this.dataset.state!=='ready')source.open=false;this.dataset.state='ready';this.removeAttribute('aria-busy');canvas.hidden=false;},
          onError:failed,
        });
      }).catch(()=>{
        if(!this.isConnected||generation!==this.generation)return;
        canvas.textContent=t('diagramUnavailable');failed();
      });
    }
    disconnectedCallback() {
      ++this.generation;this.diagram?.destroy();this.diagram=undefined;
    }
  });
}

export function createMermaidBlock(text,pre) {
  registerDiagram();
  const root=document.createElement('pot-mermaid'),canvas=document.createElement('div'),source=document.createElement('details'),summary=document.createElement('summary');
  root.sourceText=text;canvas.className='mermaid-canvas';source.className='mermaid-source';source.open=true;
  summary.textContent=translate(document.documentElement.lang,'markdown.diagramSource');
  source.append(summary,pre);root.append(canvas,source);return root;
}
