<script>
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { onMount } from 'svelte'
  import { writable } from 'svelte/store'
  import { history, track, fetchRange } from './stream.js'
  import { buildData } from './chartdata.js'

  // series: [{key, label, color, axis?}] — axis: 2 hängt die Serie an die
  // rechte Achse (unit2). Alle Props nach height sind optional: ohne sie
  // verhält sich der Graph wie bisher (Live-Stunde aus dem gemeinsamen
  // Verlauf, eine Achse) — die Dashboard-Graphen bleiben unverändert.
  // Zeitraum-Props werden nur beim Einbinden gelesen; zum Wechseln den
  // Graphen per {#key} neu einbinden.
  let {
    series = [],
    unit = '',
    height = 180,
    unit2 = '',      // Beschriftung der rechten Achse
    thresholds = [], // [{value, axis?: 1|2, color?}] gestrichelte Grenzlinie
    bands = [],      // [{from, to}] Unix-Sekunden, rot hinterlegt (Lags)
    hours = null,    // gleitender Zeitraum in Stunden aus /api/history (lädt alle 60 s neu)
    from = null,     // fester Zeitraum in Unix-Sekunden (mit to), z. B. ein Lag
    to = null,
  } = $props()

  let el
  let plot = null
  let win = null // Zeitfenster im Abruf-Modus: x-Achse zeigt genau diesen Bereich

  // Achsenbereich so wählen, dass die Grenzlinien immer sichtbar sind.
  // Ob die Achse einen eigenen Bereich bekommt, entscheidet sich beim
  // Einbinden; die Grenzwerte selbst liest die Funktion bei jeder
  // Skalierung frisch, spätere Änderungen wirken also.
  function scaleRange(axis) {
    const mine = (t) => (t.axis ?? 1) === axis
    if (!thresholds.some(mine)) return null
    return (u, min, max) => {
      const limits = thresholds.filter(mine).map((t) => t.value)
      return [Math.min(0, min ?? 0), Math.max(max ?? 0, ...limits) * 1.1]
    }
  }

  function drawBands(u) {
    if (!bands.length) return
    const { left, top, width, height: h } = u.bbox
    const ctx = u.ctx
    ctx.save()
    ctx.fillStyle = 'rgba(248, 113, 113, 0.18)'
    for (const b of bands) {
      const x0 = u.valToPos(b.from, 'x', true)
      const x1 = u.valToPos(b.to, 'x', true)
      if (x1 < left || x0 > left + width) continue
      const a = Math.max(x0, left)
      const z = Math.min(x1, left + width)
      // sehr kurze Lags trotzdem sichtbar (mind. 3 px)
      ctx.fillRect(a, top, Math.max(z - a, 3), h)
    }
    ctx.restore()
  }

  function drawThresholds(u) {
    if (!thresholds.length) return
    const { left, top, width, height: h } = u.bbox
    const ctx = u.ctx
    ctx.save()
    ctx.setLineDash([6, 4])
    ctx.lineWidth = 1.5
    for (const th of thresholds) {
      const scale = th.axis === 2 ? 'y2' : 'y'
      if (u.scales[scale]?.min == null) continue
      const y = u.valToPos(th.value, scale, true)
      if (y < top || y > top + h) continue
      ctx.strokeStyle = th.color ?? '#f87171'
      ctx.beginPath()
      ctx.moveTo(left, y)
      ctx.lineTo(left + width, y)
      ctx.stroke()
    }
    ctx.restore()
  }

  onMount(() => {
    const keys = series.map((s) => s.key)
    const fixed = from != null || hours != null
    const hasY2 = series.some((s) => s.axis === 2)

    const scales = { x: { time: true } }
    if (fixed) scales.x.range = (u, min, max) => (win ? [win.from, win.to] : [min, max])
    const r1 = scaleRange(1)
    const r2 = scaleRange(2)
    if (r1) scales.y = { range: r1 }
    if (hasY2) scales.y2 = r2 ? { range: r2 } : {}

    const axes = [
      { stroke: '#8b949e', grid: { stroke: '#2a323c' }, ticks: { stroke: '#2a323c' } },
      {
        stroke: '#8b949e',
        grid: { stroke: '#2a323c' },
        ticks: { stroke: '#2a323c' },
        label: unit,
        labelGap: 8,
      },
    ]
    if (hasY2) {
      axes.push({
        scale: 'y2',
        side: 1,
        stroke: '#8b949e',
        grid: { show: false },
        ticks: { stroke: '#2a323c' },
        label: unit2,
        labelGap: 8,
      })
    }

    const opts = {
      width: el.clientWidth,
      height,
      cursor: { drag: { setScale: false } },
      select: { show: false },
      legend: { live: true },
      scales,
      axes,
      series: [
        {},
        ...series.map((s) => ({
          label: s.label,
          stroke: s.color,
          width: 1.5,
          // Lücken (Leerzeile aus buildData) bleiben sichtbar
          spanGaps: false,
          ...(s.axis === 2 ? { scale: 'y2' } : {}),
        })),
      ],
      hooks: {
        drawClear: [drawBands], // hinter den Linien
        draw: [drawThresholds],
      },
    }
    const empty = [[], ...series.map(() => [])]
    plot = new uPlot(opts, empty, el)

    // Live: gemeinsamer Verlauf (SSE + vorgeladene Stunde);
    // Abruf-Modus: eigener Speicher aus /api/history
    const source = fixed ? writable({}) : history
    if (!fixed) track(keys)
    const unsub = source.subscribe((h) => {
      const data = buildData(series, h)
      if (data && plot) plot.setData(data)
      else if (fixed && plot) plot.setData(empty)
    })

    let timer = null
    let seq = 0 // nur die Antwort des jüngsten Abrufs gilt
    async function load() {
      const my = ++seq
      const now = Math.floor(Date.now() / 1000)
      // mindestens 60 s Fenster, sonst lehnt /api/history ab (to <= from)
      win = from != null ? { from, to: Math.max(to ?? now, from + 60) } : { from: now - hours * 3600, to: now }
      const data = await fetchRange(keys, win.from, win.to)
      if (my === seq) source.set(data)
    }
    if (fixed) {
      load()
      if (from == null) timer = setInterval(load, 60_000)
    }

    const ro = new ResizeObserver(() => {
      if (plot && el) plot.setSize({ width: el.clientWidth, height })
    })
    ro.observe(el)

    return () => {
      unsub()
      clearInterval(timer)
      ro.disconnect()
      plot?.destroy()
      plot = null
    }
  })

  // Lag-Bänder/Grenzlinien neu zeichnen, wenn sich die Listen ändern
  $effect(() => {
    bands
    thresholds
    // setData skaliert neu (Grenzlinien), zeichnet Bänder und Linien frisch
    if (plot) plot.setData(plot.data, true)
  })
</script>

<div class="chart-wrap" bind:this={el}></div>
