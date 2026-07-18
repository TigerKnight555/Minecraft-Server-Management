<script>
  import { containers } from './stream.js'
  import { onDestroy } from 'svelte'

  let selected = $state('')
  let lines = $state([])
  let autoScroll = $state(true)
  let viewEl
  let es = null

  const MAX_LINES = 500

  // default to the first running container (usually mc-fabric)
  $effect(() => {
    if (!selected && $containers.length > 0) {
      const mc = $containers.find((c) => c.name === 'mc-fabric')
      selected = (mc ?? $containers[0]).name
    }
  })

  $effect(() => {
    if (selected) open(selected)
  })

  function open(name) {
    close()
    lines = []
    es = new EventSource(`/api/stream/logs?container=${encodeURIComponent(name)}&tail=200`)
    es.addEventListener('log', (e) => {
      lines.push(JSON.parse(e.data))
      if (lines.length > MAX_LINES) lines = lines.slice(-MAX_LINES)
      if (autoScroll && viewEl) queueMicrotask(() => (viewEl.scrollTop = viewEl.scrollHeight))
    })
    es.onerror = () => {
      // container gone or stream broke; EventSource retries itself
    }
  }

  function close() {
    es?.close()
    es = null
  }

  onDestroy(close)
</script>

<div class="panel wide">
  <h2>
    Logs
    <select bind:value={selected} style="margin-left: 0.8rem">
      {#each $containers as c (c.id)}
        <option value={c.name}>{c.name}</option>
      {/each}
    </select>
    <label style="margin-left: 0.8rem; font-size: 0.75rem; text-transform: none; letter-spacing: 0">
      <input type="checkbox" bind:checked={autoScroll} /> autoscroll
    </label>
  </h2>
  <div class="log-view" bind:this={viewEl}>{lines.join('\n')}</div>
</div>
