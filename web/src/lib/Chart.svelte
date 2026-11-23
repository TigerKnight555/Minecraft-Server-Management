<script>
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { onMount } from 'svelte'
  import { history, track } from './stream.js'

  // series: [{key, label, color}], unit: axis label
  let { series = [], unit = '', height = 180 } = $props()

  let el
  let plot = null

  // typischer Abstand einer Serie (Median), daraus die Lückenschwelle:
  // fehlen mehr als ~4 Messungen (Tab im Hintergrund, Standby, MSM aus),
  // zeigt der Graph eine Lücke statt einer geraden Linie
  function gapSeconds(t) {
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
  function buildData(h) {
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

  onMount(() => {
    track(series.map((s) => s.key))
    const opts = {
      width: el.clientWidth,
      height,
      cursor: { drag: { setScale: false } },
      select: { show: false },
      legend: { live: true },
      scales: { x: { time: true } },
      axes: [
        { stroke: '#8b949e', grid: { stroke: '#2a323c' }, ticks: { stroke: '#2a323c' } },
        {
          stroke: '#8b949e',
          grid: { stroke: '#2a323c' },
          ticks: { stroke: '#2a323c' },
          label: unit,
          labelGap: 8,
        },
      ],
      series: [
        {},
        ...series.map((s) => ({
          label: s.label,
          stroke: s.color,
          width: 1.5,
          // Lücken (Leerzeile aus buildData) bleiben sichtbar
          spanGaps: false,
        })),
      ],
    }
    plot = new uPlot(opts, [[], ...series.map(() => [])], el)

    const unsub = history.subscribe((h) => {
      const data = buildData(h)
      if (data && plot) plot.setData(data)
    })

    const ro = new ResizeObserver(() => {
      if (plot && el) plot.setSize({ width: el.clientWidth, height })
    })
    ro.observe(el)

    return () => {
      unsub()
      ro.disconnect()
      plot?.destroy()
    }
  })
</script>

<div class="chart-wrap" bind:this={el}></div>
