import { test } from 'node:test'
import assert from 'node:assert/strict'
import { GiB } from './chartdata.js'
import { fmtNum, fmtHeap, msptClass, fmtDuration, triggerLabel, threadHint, lagBands, lagWindow } from './perf.js'

test('fmtNum: deutsches Komma, höchstens eine Nachkommastelle', () => {
  assert.equal(fmtNum(5.24), '5,2')
  assert.equal(fmtNum(12), '12')
})

test('fmtHeap', () => {
  assert.equal(fmtHeap(5.2 * GiB, 12 * GiB), '5,2 von 12 GiB')
  assert.equal(fmtHeap(null, 12 * GiB), '—')
})

test('msptClass: grün < 40, gelb < 50, rot ab 50', () => {
  assert.equal(msptClass(39.9), 'ok')
  assert.equal(msptClass(40), 'warn')
  assert.equal(msptClass(49.9), 'warn')
  assert.equal(msptClass(50), 'err')
  assert.equal(msptClass(null), '')
})

test('fmtDuration', () => {
  assert.equal(fmtDuration('2026-11-25T10:00:00Z', '2026-11-25T10:00:00Z'), '< 5 s')
  assert.equal(fmtDuration('2026-11-25T10:00:00Z', '2026-11-25T10:00:35Z'), '35 s')
  assert.equal(fmtDuration('2026-11-25T10:00:00Z', '2026-11-25T10:02:05Z'), '2 min 5 s')
})

test('triggerLabel und threadHint', () => {
  assert.equal(triggerLabel('mspt'), 'MSPT')
  assert.equal(triggerLabel('gc'), 'GC-Pause')
  assert.equal(triggerLabel('cpu'), 'Server-Thread')
  assert.equal(threadHint('Server thread'), 'Spielwelt')
  assert.equal(threadHint('Netty Epoll Server IO'), 'Netzwerk')
  assert.equal(threadHint('C2ME worker'), 'Chunks')
  assert.equal(threadHint('Worker-Main'), 'Chunks')
  assert.equal(threadHint('GC Thread'), 'Speicher aufräumen')
  assert.equal(threadHint('Unbekannt'), '')
})

test('lagBands: gespeicherte Lags + laufender Lag ohne Dopplung', () => {
  const lags = [{ id: 1, start: '2026-11-25T10:00:00Z', end: '2026-11-25T10:00:30Z' }]
  const open = { id: 2, start: '2026-11-25T11:00:00Z', end: '2026-11-25T11:00:10Z' }
  const t = (iso) => Date.parse(iso) / 1000
  assert.deepEqual(lagBands(lags, open), [
    { from: t('2026-11-25T10:00:00Z'), to: t('2026-11-25T10:00:30Z') },
    { from: t('2026-11-25T11:00:00Z'), to: t('2026-11-25T11:00:10Z') },
  ])
  assert.equal(lagBands([...lags, open], open).length, 2)
  // gespeicherte Kopie des offenen Lags hat altes Ende → Live-Wert gewinnt
  const stale = { ...open, end: '2026-11-25T11:00:02Z' }
  assert.deepEqual(lagBands([...lags, stale], open)[1], { from: t(open.start), to: t(open.end) })
  assert.deepEqual(lagBands(null, null), [])
})

test('lagWindow: 5 min Rand um den Lag', () => {
  const w = lagWindow({ start: '2026-11-25T10:00:00Z', end: '2026-11-25T10:00:30Z' })
  assert.equal(w.to - w.from, 630)
})
