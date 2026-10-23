'use strict';
window.PotLocale = (() => {
 const codes = ['en','zh-CN','zh-TW','ja','ko','es'];
 const normalize = value => {
  const parts=String(value||'').toLowerCase().replaceAll('_','-').split('-');
  if(parts[0]==='zh'){
   if(parts.includes('hant'))return 'zh-TW';
   if(parts.includes('hans'))return 'zh-CN';
   return parts.some(v=>['tw','hk','mo'].includes(v))?'zh-TW':'zh-CN';
  }
  return codes.includes(parts[0])?parts[0]:null;
 };
 const saved=()=>{try{return normalize(localStorage.getItem('pot.website.language'));}catch{return null;}};
 const save=code=>{try{localStorage.setItem('pot.website.language',code);}catch{/* Storage is optional in isolated previews. */}};
 const preferred=()=>saved()||(navigator.languages||[navigator.language]).map(normalize).find(Boolean)||'en';
 if(document.documentElement.hasAttribute('data-detect-language')){
  const url=new URL(preferred()+'/',location.href);url.search=location.search;url.hash=location.hash;location.replace(url.href);
 }
 return {codes,normalize,save,preferred};
})();
