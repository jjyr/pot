import assert from 'node:assert/strict';
import {storedPreference} from '../../web/src/state/preferences.mjs';
const values=new Map([['pot-ui-locale','zh-CN'],['pot-ui-theme','dark']]);
const storage={getItem:key=>values.get(key)??null};
assert.equal(storedPreference('pot-ui-locale','en',storage),'zh-CN');
assert.equal(storedPreference('pot-ui-theme','system',storage),'dark');
assert.equal(storedPreference('missing','fallback',storage),'fallback');
assert.equal(storedPreference('pot-ui-theme','system',{getItem:()=>{throw Error('denied');}}),'system');
console.log('Pot preferences and restricted storage passed');
