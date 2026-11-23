<script>
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { onMount } from 'svelte'
  import { history, track } from './stream.js'

  // series: [{key, label, color}], unit: axis label
  let { series = [], unit = '', height = 180 } = $props()

  let el
  let plot = null

  // typischer Abstand der Zeitachse (Median), daraus die Lückenschwelle:
  // fehlen mehr als ~4 Messungen (Tab im Hintergrund, Standby, MSM aus),
  // zeigt der Graph eine Lücke statt einer geraden Linie
  function gapSeconds(t) {
    if (t.length < 3) return Infinity
    const d = []
    for (let i = 1; i < t.length; i++) d.push(t[i] - t[i - 1])
    d.sort((a, b) => a - b)
    return Math.max(d[Math.floor(d.length / 2)] * 4, 20)
  }

  function buildData(h) {
    // Zeitachse der ersten Serie; die übrigen messen im selben Takt, aber
    // nicht in exakt derselben Sekunde → nächster Punkt innerhalb der Toleranz
    const first = h[series[0]?.key]
    if (!first || first.t.length === 0) return null
    const gap = gapSeconds(first.t)
    const tol = Math.min(gap / 4, 15)
    const cols = series.map((s) => h[s.key])
    const pos = cols.map(() => 0)
    const data = [[], ...series.map(() => [])]
    for (let i = 0; i < first.t.length; i++) {
      const t = first.t[i]
      if (i > 0 && t - first.t[i - 1] > gap) {
        // Lückenmarke: eine leere Zeile direkt nach dem letzten Punkt
        data[0].push(first.t[i - 1] + 1)
        cols.forEach((_, k) => data[k + 1].push(null))
      }
      data[0].push(t)
      cols.forEach((buf, k) => {
        if (!buf) return data[k + 1].push(null)
        let j = pos[k]
        while (j + 1 < buf.t.length && Math.abs(buf.t[j + 1] - t) <= Math.abs(buf.t[j] - t)) j++
        pos[k] = j
        data[k + 1].push(Math.abs(buf.t[j] - t) <= tol ? buf.v[j] : null)
      })
    }
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
