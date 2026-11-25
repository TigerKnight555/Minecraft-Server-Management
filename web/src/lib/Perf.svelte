<script>
  import { onMount } from 'svelte'
  import Chart from './Chart.svelte'
  import { perf, mc, perfStatus, perfLags } from './stream.js'
  import { fmtNum, fmtHeap, msptClass, fmtDuration, triggerLabel, threadHint, lagBands, lagWindow } from './perf.js'

  const GC_LINE = 'JVM_XX_OPTS: "-Xlog:gc:file=/data/logs/gc.log:time,uptime:filecount=5,filesize=10m"'

  let range = $state(1)   // Stunden: 1 / 6 / 24
  let jump = $state(null) // {from, to} nach Klick auf einen Lag
  let lags = $state([])
  let lagError = $state('')

  async function loadLags() {
    try {
      lags = await perfLags(7)
      lagError = ''
    } catch (err) {
      lagError = err.message
    }
  }

  onMount(() => {
    perfStatus().then((s) => perf.set(s)).catch(() => {})
    loadLags()
    const t = setInterval(loadLags, 30_000)
    return () => clearInterval(t)
  })

  let p = $derived($perf)
  let bands = $derived(lagBands(lags, p?.openLag))
  let maxGroupCpu = $derived(Math.max(1, ...(p?.groups ?? []).map((g) => g.cpu)))
  // Zeitraum der Graphen; ein Wechsel bindet sie per {#key} neu ein
  let win = $derived(jump ? { hours: null, from: jump.from, to: jump.to } : { hours: range === 1 ? null : range, from: null, to: null })
  let chartKey = $derived(jump ? `lag-${jump.from}-${jump.to}` : `h-${range}`)

  function pickRange(h) {
    range = h
    jump = null
  }
  function showLag(lag) {
    jump = lagWindow(lag)
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }
  const fmtTime = (iso) =>
    new Date(iso).toLocaleString('de-DE', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })
</script>

{#if p?.openLag}
  <div class="lag-now">Lag läuft gerade · {p.openLag.diagnosis || 'Ursache wird ermittelt'}</div>
{/if}

<div class="tiles">
  <div class="tile">
    <div class="label">Server-Thread</div>
    <div class="value">{p?.serverThreadCpu != null ? Math.round(p.serverThreadCpu) + ' %' : '—'}</div>
    <div class="detail">eines Kerns · Spielwelt</div>
  </div>
  <div class="tile {msptClass($mc?.mspt || null)}">
    <div class="label">MSPT</div>
    <div class="value">{$mc?.mspt ? fmtNum($mc.mspt) + ' ms' : '—'}</div>
    <div class="detail">Dauer eines Ticks · Grenze 50 ms</div>
  </div>
  <div class="tile">
    <div class="label">Heap nach GC</div>
    <div class="value" style="font-size:1.1rem">{fmtHeap(p?.heapAfterGc, p?.heapTotal)}</div>
    <div class="detail">wirklich belegter Java-Speicher</div>
  </div>
  <div class="tile">
    <div class="label">Letzte GC-Pause</div>
    <div class="value">{p?.lastGcPauseMs != null ? fmtNum(p.lastGcPauseMs) + ' ms' : '—'}</div>
  </div>
  <div class="tile">
    <div class="label">Java-CPU gesamt</div>
    <div class="value">{p?.javaCpu != null ? Math.round(p.javaCpu) + ' %' : '—'}</div>
    <div class="detail">{p?.hostCores ? `von ${p.hostCores} Kernen` : ''}</div>
  </div>
</div>

<div class="range-bar">
  {#each [1, 6, 24] as h (h)}
    <button class={!jump && range === h ? 'active' : ''} onclick={() => pickRange(h)}>{h} h</button>
  {/each}
  {#if jump}
    <span class="jump">Zeitraum eines Lags · <button class="link" onclick={() => (jump = null)}>zurück</button></span>
  {/if}
</div>

<div class="panels">
  {#key chartKey}
    <div class="panel wide">
      <h2>Server-Thread (% Kern) und MSPT (ms)</h2>
      {#if p && !p.threads?.ok}<div class="hint">Threads: {p.threads?.reason}</div>{/if}
      <Chart
        unit="% Kern"
        unit2="ms"
        {bands}
        hours={win.hours}
        from={win.from}
        to={win.to}
        thresholds={[{ value: 50, axis: 2, color: '#f87171' }]}
        series={[
          // MSPT zuerst: kommt auch ohne Java-Prozess, eine leere erste Serie leert den Graphen
          { key: 'mc.mspt', label: 'MSPT', color: '#f59e0b', axis: 2 },
          { key: 'jvm.thread.server.cpu', label: 'Server-Thread', color: '#4ade80' },
        ]}
      />
    </div>

    <div class="panel">
      <h2>Heap (GiB)</h2>
      {#if p && !p.gcLog?.ok}
        <div class="hint">
          {p.gcLog?.reason} — in <code>docker-compose.yml</code> bei <code>mc-fabric</code> unter
          <code>environment</code> eintragen:
          <pre>{GC_LINE}</pre>
          danach <code>docker compose up -d mc-fabric</code> (startet den Minecraft-Server neu).
        </div>
      {/if}
      <Chart
        unit="GiB"
        {bands}
        hours={win.hours}
        from={win.from}
        to={win.to}
        series={[
          { key: 'jvm.heap.total', label: 'gesamt', color: '#8b949e' },
          { key: 'jvm.heap.after_gc', label: 'nach GC', color: '#60a5fa' },
        ]}
      />
    </div>

    <div class="panel">
      <h2>GC-Pausen (ms)</h2>
      <Chart
        unit="ms"
        {bands}
        hours={win.hours}
        from={win.from}
        to={win.to}
        thresholds={[{ value: 100, color: '#f87171' }]}
        series={[{ key: 'jvm.gc.pause_max', label: 'längste Pause', color: '#a78bfa' }]}
      />
    </div>
  {/key}

  <div class="panel wide">
    <h2>Java-Threads</h2>
    {#if !p}
      <div class="empty">Lade …</div>
    {:else if !p.threads?.ok}
      <div class="hint">{p.threads?.reason}</div>
    {:else if !p.groups?.length}
      <div class="empty">Erste Messung läuft …</div>
    {:else}
      <table class="threads">
        <tbody>
          {#each p.groups as g (g.name)}
            <tr>
              <td class="name">
                {g.name} ({g.count}){#if threadHint(g.name)}<span class="dim"> · {threadHint(g.name)}</span>{/if}
              </td>
              <td class="bar"><div style="width:{(g.cpu / maxGroupCpu) * 100}%"></div></td>
              <td class="num">{Math.round(g.cpu)} %</td>
            </tr>
          {/each}
        </tbody>
      </table>
      <p class="legend">
        % = Anteil eines CPU-Kerns. Server thread = Spielwelt (läuft auf einem einzigen Kern — bei 100 % laggt
        der Server) · Netty = Netzwerk · C2ME/Worker = Chunks laden und erzeugen · GC = Speicher aufräumen
      </p>
    {/if}
  </div>

  <div class="panel wide">
    <h2>Lag-Protokoll (7 Tage)</h2>
    {#if lagError}<div class="hint">{lagError}</div>{/if}
    {#if !lags.length}
      <div class="empty">Keine Lags erkannt</div>
    {:else}
      <div class="table-scroll">
        <table class="lags">
          <thead>
            <tr>
              <th>Zeit</th><th>Dauer</th><th>Auslöser</th><th>MSPT</th><th>Spieler</th>
              <th>Heap</th><th>Top-Threads</th><th>Diagnose</th>
            </tr>
          </thead>
          <tbody>
            {#each lags as lag (lag.id)}
              <tr onclick={() => showLag(lag)} title="Graphen auf diesen Zeitraum setzen">
                <td>{fmtTime(lag.start)}</td>
                <td>{fmtDuration(lag.start, lag.end)}</td>
                <td>{(lag.triggers ?? [lag.trigger]).map(triggerLabel).join(', ')}</td>
                <td>{lag.worstMspt ? fmtNum(lag.worstMspt) + ' ms' : '—'}</td>
                <td>{lag.players}</td>
                <td>{fmtHeap(lag.heapAfterGc || null, lag.heapTotal || null)}</td>
                <td>{(lag.topThreads ?? []).map((g) => `${g.name} ${Math.round(g.cpu)} %`).join(', ')}</td>
                <td>{lag.diagnosis || 'keine eindeutige Ursache'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
</div>

<style>
  .lag-now {
    background: rgba(248, 113, 113, 0.15); border: 1px solid var(--err); color: var(--err);
    border-radius: 8px; padding: 0.6rem 1rem; margin-bottom: 1rem; font-size: 0.9rem;
  }
  .range-bar { display: flex; gap: 0.5rem; align-items: center; margin-bottom: 1rem; }
  .range-bar button {
    background: var(--panel-border); border: none; border-radius: 6px; color: var(--text);
    padding: 0.35rem 0.9rem; cursor: pointer; font-size: 0.85rem;
  }
  .range-bar button.active { background: var(--accent); color: #0f1419; font-weight: 600; }
  .range-bar .jump { color: var(--text-dim); font-size: 0.85rem; }
  .range-bar button.link { background: none; color: var(--accent); padding: 0; text-decoration: underline; }
  .hint { color: var(--warn); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .hint pre {
    background: #0a0e12; border-radius: 6px; padding: 0.5rem; overflow-x: auto;
    color: var(--text); font-size: 0.75rem; margin: 0.4rem 0;
  }
  table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
  td, th { padding: 0.35rem 0.5rem; border-bottom: 1px solid var(--panel-border); text-align: left; }
  th { color: var(--text-dim); font-weight: 500; }
  .threads .name { white-space: nowrap; }
  .threads .bar { width: 45%; }
  .threads .bar div { height: 0.6rem; background: var(--accent); border-radius: 3px; min-width: 1px; }
  .threads .num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .dim { color: var(--text-dim); }
  .legend { color: var(--text-dim); font-size: 0.75rem; margin: 0.6rem 0 0; }
  .table-scroll { overflow-x: auto; }
  .lags tbody tr { cursor: pointer; }
  .lags tbody tr:hover { background: #222a33; }
</style>
