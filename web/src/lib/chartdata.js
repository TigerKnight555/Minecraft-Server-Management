// Reine Datenaufbereitung für die Graphen (ohne Svelte, testbar mit node --test).

export const GiB = 1024 * 1024 * 1024

// Speicherwerte liegen in der DB in Bytes und werden in GiB gezeichnet:
// Container-/Host-RAM (*.mem) und der Java-Heap (jvm.heap.*)
export const isBytes = (key) => key.endsWith('.mem') || key.startsWith('jvm.heap.')

export const toChart = (key, v) => (v == null ? v : isBytes(key) ? v / GiB : v)

// typischer Abstand einer Serie (Median), daraus die Lückenschwelle:
// fehlen mehr als ~4 Messungen (Tab im Hintergrund, Standby, MSM aus),
// zeigt der Graph eine Lücke statt einer geraden Linie
export function gapSeconds(t) {
  if (t.length < 3) return Infinity
  const d = []
  for (let i = 1; i < t.length; i++) d.push(t[i] - t[i - 1])
  d.sort((a, b) => a - b)
  // mindestens 90 s: der Verlauf aus der DB ist gröber (30–60 s) als live (5 s)
  return Math.max(d[Math.floor(d.length / 2)] * 4, 90)
}

// Die Serien messen in unterschiedlichem Takt (Host, Container, Verlauf aus
// der DB gröber als live). Gemeinsame Zeitachse = alle Zeitpunkte aller
// Serien; jede Serie wird zwischen ihren eigenen Punkten linear ergänzt und
// nur bei echten Lücken unterbrochen (null + Marke direkt nach dem Punkt).
export function buildData(series, h) {
  const bufs = series.map((s) => h[s.key]).map((b) => (b && b.t.length ? b : null))
  if (!bufs[0]) return null
  const gaps = bufs.map((b) => (b ? gapSeconds(b.t) : Infinity))
  const axis = new Set()
  bufs.forEach((b, k) => {
    if (!b) return
    b.t.forEach((t, i) => {
      axis.add(t)
      if (i > 0 && t - b.t[i - 1] > gaps[k]) axis.add(b.t[i - 1] + 1)
    })
  })
  const xs = [...axis].sort((a, b) => a - b)
  const data = [xs]
  bufs.forEach((b, k) => {
    const col = []
    let j = 0
    for (const x of xs) {
      if (!b) { col.push(null); continue }
      while (j + 1 < b.t.length && b.t[j + 1] <= x) j++
      const t0 = b.t[j], v0 = b.v[j]
      if (t0 === x) { col.push(v0); continue }
      const t1 = b.t[j + 1], v1 = b.v[j + 1]
      if (t0 > x || t1 === undefined || t1 - t0 > gaps[k] || v0 == null || v1 == null) { col.push(null); continue }
      col.push(v0 + ((v1 - v0) * (x - t0)) / (t1 - t0))
    }
    data.push(col)
  })
  return data
}
