// Parse explicit inline Markdown nodes, preserving escaped punctuation, nested
// parentheses, Unicode and angle-wrapped destinations. Never scan bare paths.
const escaped=(s,i)=>{let n=0;while(--i>=0&&s[i]==='\\')n++;return n%2===1;};
const unescape=s=>s.replace(/\\([!"#$%&'()*+,\-./:;<=>?@[\]\\^_`{|}~])/g,'$1').replace(/&amp;/g,'&').replace(/&lt;/g,'<').replace(/&gt;/g,'>');
export function inlineLinks(s){
  const out=[];
  for(let i=0;i<s.length;i++){
    if(escaped(s,i))continue;
    if(s[i]==='`'){let n=1;while(s[i+n]==='`')n++;const end=s.indexOf('`'.repeat(n),i+n);if(end>=0)i=end+n-1;continue;}
    const start=i,image=s[i]==='!'&&s[i+1]==='[';if(image)i++;if(s[i]!=='[')continue;
    const begin=i+1;let j=begin,depth=1;for(;j<s.length;j++){if(escaped(s,j))continue;if(s[j]==='[')depth++;if(s[j]===']'&&!--depth)break;}
    if(s[j+1]!=='('){i=start;continue;}let k=j+2;while(/[ \t]/.test(s[k]||'\0'))k++;let at=k,href='';
    if(s[k]==='<'){at=++k;while(k<s.length&&(s[k]!=='>'||escaped(s,k))&&s[k]!=='\n')k++;if(s[k]!=='>'){i=start;continue;}href=s.slice(at,k++);while(/[ \t]/.test(s[k]||'\0'))k++;if(s[k]===String.fromCharCode(34)||s[k]===String.fromCharCode(39)){const quote=s[k++];while(k<s.length&&(s[k]!==quote||escaped(s,k)))k++;if(k<s.length)k++;while(/[ \t]/.test(s[k]||'\0'))k++;}if(s[k]!==')'){i=start;continue;}}
    else{depth=1;for(;k<s.length;k++){if(!escaped(s,k)){if(s[k]==='(')depth++;if(s[k]===')'&&!--depth)break;}if(s[k]==='\n')break;}if(depth){i=start;continue;}href=s.slice(at,k).trim().replace(/\s+(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')$/,'');}
    if(!href){i=start;continue;}out.push({start,end:k+1,href:unescape(href),label:unescape(s.slice(begin,j)),image});i=k;
  }return out;
}
export function fileHref(href){if(href.startsWith('pot-file://'))return true;if(/^(?:https?|javascript|data|file|mailto|ftp|ssh|tel):/i.test(href))return false;href=href.replace(/:[1-9][0-9]*$/,'');return !!href&&!/^[a-z][a-z0-9+.-]*:/i.test(href)&&!href.startsWith('#')&&!href.startsWith('//');}
