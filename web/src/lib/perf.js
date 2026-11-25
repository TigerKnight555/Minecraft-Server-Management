// Reine Hilfsfunktionen des Leistung-Tabs (testbar mit node --test).
import { GiB } from './chartdata.js'

export const fmtNum = (v) => v.toLocaleString('de-DE', { maximumFractionDigits: 1 })

// „5,2 von 12 GiB"
export function fmtHeap(after, total) {
  if (after == null || total == null) return '—'
  return `${fmtNum(after / GiB)} von ${fmtNum(total / GiB)} GiB`
}

// MSPT-Kachel: grün < 40, gelb < 50, rot ≥ 50 ms
export function msptClass(ms) {
  if (ms == null) return ''
  return ms < 40 ? 'ok' : ms < 50 ? 'warn' : 'err'
}

export function fmtDuration(start, end) {
  const s = Math.round((new Date(end) - new Date(start)) / 1000)
  if (s < 5) return '< 5 s'
  if (s < 60) return `${s} s`
  return `${Math.floor(s / 60)} min ${s % 60} s`
}

const TRIGGERS = { mspt: 'MSPT', gc: 'GC-Pause', cpu: 'Server-Thread' }
export const triggerLabel = (t) => TRIGGERS[t] ?? t

// kurze Erklärung der wichtigen Threads
const HINTS = [
  [/^Server thread$/, 'Spielwelt'],
  [/^Netty/, 'Netzwerk'],
  [/^(C2ME|Worker-Main)/, 'Chunks'],
  [/^(GC Thread|G1 )/, 'Speicher aufräumen'],
]
export function threadHint(name) {
  for (const [re, txt] of HINTS) if (re.test(name)) return txt
  return ''
}

const toSec = (iso) => Math.floor(new Date(iso).getTime() / 1000)

// Lag-Zeiträume für die roten Bänder; der laufende Lag kommt live dazu
export function lagBands(lags, openLag) {
  // der laufende Lag kommt live (aktuelles Ende) und ersetzt die gespeicherte Kopie
  const all = (lags ?? []).filter((l) => !(openLag && l.id === openLag.id))
  if (openLag) all.push(openLag)
  // Lags unter 5 s (start = end) bekommen ein minimal sichtbares Fenster
  return all.map((l) => {
    const from = toSec(l.start)
    return { from, to: Math.max(toSec(l.end), from + 5) }
  })
}

// Zeitraum der Graphen nach Klick auf einen Lag
export function lagWindow(lag, padSec = 300) {
  // der Rand von padSec hält das Fenster auch bei Null-Lags weit über 60 s
  return { from: toSec(lag.start) - padSec, to: toSec(lag.end) + padSec }
}
