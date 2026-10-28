// A small DOM for callback/IO contracts. It does not establish browser layout,
// native input behaviour, focus geometry or accessibility rendering.
export class Element extends EventTarget {
 constructor(tag){super();this.tagName=tag;this.nodeType=1;this.children=[];this.style={};this.dataset={};this.attrs=new Map();this.className='';this.disabled=false;this.open=false;this._text='';this.scrollTop=this.scrollLeft=0;this.offsetParent=true;this.classList={contains:name=>this.className.split(' ').includes(name),add:name=>{if(!this.classList.contains(name))this.className+=' '+name;},remove:name=>{this.className=this.className.split(' ').filter(x=>x!==name).join(' ');},toggle:(name,on)=>{if(on??!this.classList.contains(name))this.classList.add(name);else this.classList.remove(name);}};}
 get attributes(){const values=new Map(this.attrs);if(this.className)values.set('class',this.className);if(this.id)values.set('id',this.id);for(const [key,value]of Object.entries(this.dataset))values.set('data-'+key.replace(/[A-Z]/g,c=>'-'+c.toLowerCase()),String(value));return [...values].map(([name,value])=>({name,value}));}
 get childNodes(){return this.children;}get firstChild(){return this.children[0];}get lastChild(){return this.children.at(-1);}get options(){return this.children;}
 set textContent(value){this.replaceChildren();if(String(value))this.append(new Text(String(value)));}get textContent(){return this.children.map(c=>c.textContent).join('');}
 setAttribute(key,value){if(key==='class')this.className=String(value);else if(key==='id')this.id=String(value);else if(key.startsWith('data-'))this.dataset[key.slice(5).replace(/-([a-z])/g,(_,c)=>c.toUpperCase())]=String(value);else this.attrs.set(key,String(value));}
 getAttribute(key){return this.attributes.find(attr=>attr.name===key)?.value??null;}hasAttribute(key){return this.getAttribute(key)!==null;}
 removeAttribute(key){if(key==='class')this.className='';else if(key==='id')this.id='';else if(key.startsWith('data-'))delete this.dataset[key.slice(5).replace(/-([a-z])/g,(_,c)=>c.toUpperCase())];else this.attrs.delete(key);}
 append(...children){for(const child of children){child.remove?.();child.parentElement=child.parentNode=this;this.children.push(child);}}
 prepend(...children){for(const child of children.reverse())this.insertBefore(child,this.firstChild);}
 insertBefore(child,next){child.remove?.();child.parentElement=child.parentNode=this;const index=this.children.indexOf(next);this.children.splice(index<0?this.children.length:index,0,child);}
 replaceChildren(...children){for(const child of [...this.children])child.remove();this._text='';this.append(...children);}
 replaceWith(node){const parent=this.parentElement;if(parent){parent.insertBefore(node,this);this.remove();}}
 after(...nodes){const parent=this.parentElement;if(!parent)return;const next=parent.children[parent.children.indexOf(this)+1];for(const node of nodes)parent.insertBefore(node,next);}
 before(...nodes){const parent=this.parentElement;if(parent)for(const node of nodes)parent.insertBefore(node,this);}
 get isConnected(){return this===document.body||Boolean(this.parentElement?.isConnected);}
 contains(node){return this===node||this.children.some(child=>child.contains?.(node));}
 matches(selector){selector=selector.trim();const pieces=selector.split(/\s+/);if(pieces.length>1){const end=pieces.pop();return this.matches(end)&&!!this.parentElement?.closest(pieces.join(' '));}const attr=selector.match(/\[([^=\]]+)(?:=["']?([^\]"']+)["']?)?\]/);if(attr){const value=this.getAttribute(attr[1]);if(value===null||attr[2]!==undefined&&value!==attr[2])return false;selector=selector.replace(attr[0],'');}const classes=selector.split('.');return (!classes[0]||this.tagName===classes[0])&&classes.slice(1).every(name=>this.classList.contains(name));}
 querySelectorAll(selector){return this.children.flatMap(child=>[...(selector.split(',').some(s=>child.matches(s))?[child]:[]),...child.querySelectorAll(selector)]);}
 querySelector(selector){return this.querySelectorAll(selector)[0]||null;}closest(selector){return this.matches(selector)?this:this.parentElement?.closest(selector);}
 showModal(){this.open=true;}close(){this.open=false;this.dispatchEvent(new Event('close'));}
 remove(){if(this.parentElement)this.parentElement.children=this.parentElement.children.filter(child=>child!==this);this.parentElement=this.parentNode=null;}
 focus(){document.activeElement=this;}click(){if(!this.disabled)return this.onclick?.({currentTarget:this,preventDefault(){}});}
 reportValidity(){return true;}getBoundingClientRect(){return {width:1000,height:700,top:0,left:0};}
 get clientWidth(){return 1000;}
 dispatchEvent(event){const result=super.dispatchEvent(event);if(event.bubbles)this.parentElement?.dispatchEvent(event);return result;}
}
class Text {
 constructor(value){this.nodeType=3;this.data=value;}
 get textContent(){return this.data;}set textContent(value){this.data=String(value);}
 matches(){return false;}querySelectorAll(){return [];}contains(node){return node===this;}
 remove(){Element.prototype.remove.call(this);}
 get isConnected(){return !!this.parentElement?.isConnected;}
}
export function installDOM(){
 globalThis.Node=Element;globalThis.document=new EventTarget();Object.assign(document,{createElement:tag=>new Element(tag),createElementNS:(_,tag)=>new Element(tag),body:new Element('body'),hidden:false,visibilityState:'visible',hasFocus:()=>true});
 globalThis.window=new EventTarget();window.confirm=()=>true;window.open=()=>({opener:null,location:'',close(){this.closed=true;}});
 globalThis.location={origin:'https://fixture.invalid',href:'https://fixture.invalid/'};globalThis.history={state:{},pushState(){},replaceState(){}};globalThis.innerWidth=1280;globalThis.matchMedia=()=>({matches:false,addEventListener(){},removeEventListener(){}});globalThis.requestAnimationFrame=callback=>setTimeout(callback,0);globalThis.cancelAnimationFrame=clearTimeout;
 globalThis.ResizeObserver=class{observe(){}disconnect(){}};
 return document;
}
export const tick=()=>new Promise(resolve=>setImmediate(resolve));
export function input(node,value,event='input'){if(node.type==='checkbox')node.checked=value;else node.value=value;node['on'+event]?.({currentTarget:node});node.dispatchEvent(new Event(event,{bubbles:true}));}
export const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject};};
