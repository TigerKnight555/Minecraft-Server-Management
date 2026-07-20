<script>
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { onMount } from 'svelte'
  import { history } from './stream.js'

  // series: [{key, label, color}], unit: axis label
  let { series = [], unit = '', height = 180 } = $props()

  let el
  let plot = null

  function buildData(h) {
    // align all series on the first series' time axis (they tick together)
    const first = h[series[0]?.key]
    if (!first || first.t.length === 0) return null
    const data = [first.t]
    for (const s of series) {
      const buf = h[s.key]
      data.push(buf ? alignTo(first.t, buf) : new Array(first.t.length).fill(null))
    }
    return data
  }

  function alignTo(times, buf) {
    if (buf.t.length === times.length) return buf.v
    // simple nearest-fill for slightly out-of-sync series
    const map = new Map(buf.t.map((t, i) => [t, buf.v[i]]))
    return times.map((t) => map.get(t) ?? null)
  }

  onMount(() => {
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
          spanGaps: true,
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
