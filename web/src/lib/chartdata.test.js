import { test } from 'node:test'
import assert from 'node:assert/strict'
import { isBytes, toChart, buildData, GiB } from './chartdata.js'

test('isBytes: .mem und jvm.heap.* sind Bytes', () => {
  for (const k of ['host.mem', 'container.mc-fabric.mem', 'jvm.heap.after_gc', 'jvm.heap.total']) {
    assert.equal(isBytes(k), true, k)
  }
  for (const k of ['host.cpu', 'jvm.gc.pause_max', 'jvm.thread.server.cpu', 'mc.mspt']) {
    assert.equal(isBytes(k), false, k)
  }
})

test('toChart rechnet Bytes in GiB um', () => {
  assert.equal(toChart('jvm.heap.total', 12 * GiB), 12)
  assert.equal(toChart('mc.mspt', 42), 42)
  assert.equal(toChart('jvm.heap.total', null), null)
})

test('buildData ergänzt Serien mit anderem Takt linear', () => {
  const series = [{ key: 'a' }, { key: 'b' }]
  const h = { a: { t: [0, 10, 20], v: [1, 2, 3] }, b: { t: [5, 15], v: [10, 20] } }
  assert.deepEqual(buildData(series, h), [
    [0, 5, 10, 15, 20],
    [1, 1.5, 2, 2.5, 3],
    [null, 10, 15, 20, null],
  ])
})

test('buildData zeigt echte Lücken', () => {
  const h = { a: { t: [0, 10, 20, 30, 500], v: [1, 2, 3, 4, 5] } }
  assert.deepEqual(buildData([{ key: 'a' }], h), [
    [0, 10, 20, 30, 31, 500],
    [1, 2, 3, 4, null, 5],
  ])
})

test('buildData ohne Daten der ersten Serie: null', () => {
  assert.equal(buildData([{ key: 'a' }], {}), null)
  assert.equal(buildData([{ key: 'a' }], { a: { t: [], v: [] } }), null)
})
