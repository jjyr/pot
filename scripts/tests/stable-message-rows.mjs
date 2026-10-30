import assert from 'node:assert/strict'
import { mergeConversationRows as merge } from '../../web/src/state/stable-message-rows.mjs'

const r = (id, n) => ({ id, viewKey: id, sequence: String(n) })
const keys = rows => rows.map(r => r.viewKey)
const clone = o => JSON.parse(JSON.stringify(o))

const b2 = r('b', 2), c3 = r('c', 3)
const base = { previous: [b2, c3], incoming: [], suppressedKeys: [], ephemeralKeys: [] }
const init = clone(base)
let out = merge(base.previous, base.incoming, { suppressedKeys: [], ephemeralKeys: [] })
assert.deepEqual(keys(out), ['b', 'c'])
assert.deepEqual(clone(base), init)

const c3u = { ...c3, text: 'updated' }
out = merge([b2, c3], [c3u], { suppressedKeys: [], ephemeralKeys: [] })
assert.deepEqual(keys(out), ['b', 'c'])
assert.equal(out[1].text, 'updated')

const a1 = r('a', 1)
out = merge([b2, c3], [a1, b2], { suppressedKeys: [], ephemeralKeys: [] })
assert.deepEqual(keys(out), ['a', 'b', 'c'])

const d4 = r('d', 4)
out = merge([a1, b2, c3], [c3, d4], { suppressedKeys: [], ephemeralKeys: [] })
assert.deepEqual(keys(out), ['a', 'b', 'c', 'd'])

const cReceipt = { ...c3, receipt: 'xyz' }
out = merge([a1, b2, c3, d4], [cReceipt], { suppressedKeys: [], ephemeralKeys: [] })
assert.deepEqual(keys(out), ['a', 'b', 'c', 'd'])
assert.equal(keys(out).indexOf('c'), 2)

assert.throws(() => merge([a1, d4], [c3], { suppressedKeys: [], ephemeralKeys: [] }))
assert.throws(() => merge([a1, b2], [b2, a1], { suppressedKeys: [], ephemeralKeys: [] }))
assert.throws(() => merge([a1], [{ id: 'x', viewKey: 'x' }], { suppressedKeys: [], ephemeralKeys: [] }))
assert.throws(() => merge([a1], [a1, a1], { suppressedKeys: [], ephemeralKeys: [] }))

const activity = { ...r('activity', 5), ephemeral: true }
out = merge([a1, b2, c3, d4], [activity], { suppressedKeys: [], ephemeralKeys: ['activity'] })
assert.deepEqual(keys(out), ['a', 'b', 'c', 'd', 'activity'])
assert.equal(out[4].presentationSuppressed, false)

const hidden = merge([a1, b2, c3, d4, activity], [], { suppressedKeys: ['activity'], ephemeralKeys: ['activity'] })
assert.deepEqual(keys(hidden), ['a', 'b', 'c', 'd', 'activity'])
assert.equal(hidden[4].presentationSuppressed, true)

const e5 = r('e', 5)
out = merge([a1, b2, c3, d4, activity], [e5], { suppressedKeys: ['activity'], ephemeralKeys: ['activity'] })
assert.deepEqual(keys(out), ['a', 'b', 'c', 'd', 'activity', 'e'])
assert.equal(out[4].presentationSuppressed, true)
assert.equal(out[5].presentationSuppressed, false)

const cleared = merge([a1, b2, c3, d4, activity], [e5], { suppressedKeys: [], ephemeralKeys: ['activity'] })
assert.equal(cleared[4].presentationSuppressed, false)

assert.throws(() => merge([], [b2, a1], { suppressedKeys: [], ephemeralKeys: [] }))
out = merge([b2], [d4], { suppressedKeys: [], ephemeralKeys: [] })
assert.deepEqual(keys(out), ['b', 'd'])
out = merge([a1, b2], [a1, activity, b2], { ephemeralKeys: ['activity'], suppressedKeys: [] })
assert.deepEqual(keys(out), ['a', 'activity', 'b'])

console.log('PASS')
