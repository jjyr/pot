import assert from 'node:assert/strict';
import {storedPreference} from '../../web/src/state/preferences.mjs';

const values=new Map([['pot-go-preview-locale','zh-CN'],['pot-go-preview-theme','dark']]);
const storage={getItem:key=>values.get(key)??null,setItem:(key,value)=>values.set(key,value)};
assert.equal(storedPreference('pot-ui-locale','en',storage),'zh-CN');
assert.equal(storedPreference('pot-ui-theme','system',storage),'dark');
assert.equal(values.get('pot-ui-theme'),'dark');
assert.equal(values.get('pot-go-preview-theme'),'dark');
values.set('pot-ui-locale','en');
assert.equal(storedPreference('pot-ui-locale','zh-CN',storage),'en');
assert.equal(storedPreference('unrelated','fallback',storage),'fallback');
const readOnly={...storage,setItem:()=>{throw Error('read only');}};
values.delete('pot-ui-theme');
assert.equal(storedPreference('pot-ui-theme','system',readOnly),'dark');
assert.equal(storedPreference('pot-ui-theme','system',{getItem:()=>{throw Error('denied');}}),'system');
console.log('Preference compatibility, precedence and restricted storage passed');
