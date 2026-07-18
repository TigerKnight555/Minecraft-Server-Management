<script>
  import { containers, stats, containerAction } from './stream.js'

  let busy = $state({})
  let feedback = $state({})

  // which containers may be controlled comes from the server: a 403 simply
  // reports "not allowlisted", so buttons show for all and errors surface.
  async function act(name, action) {
    const verb = { start: 'starten', stop: 'stoppen', restart: 'neustarten' }[action]
    if (action !== 'start' && !confirm(`Container "${name}" wirklich ${verb}? Spieler werden getrennt.`)) return
    busy = { ...busy, [name]: true }
    feedback = { ...feedback, [name]: '' }
    try {
      await containerAction(name, action)
      feedback = { ...feedback, [name]: `${verb} OK` }
    } catch (err) {
      feedback = { ...feedback, [name]: `Fehler: ${err.message}` }
    } finally {
      busy = { ...busy, [name]: false }
      setTimeout(() => (feedback = { ...feedback, [name]: '' }), 5000)
    }
  }

  function mem(name) {
    const s = $stats[name]
    return s ? (s.memUsage / 1024 / 1024).toFixed(0) + ' MiB' : ''
  }
</script>

<div class="panel wide">
  <h2>Container</h2>
  <table class="ct-table">
    <thead>
      <tr><th>Name</th><th>Image</th><th>Status</th><th>RAM</th><th>Aktionen</th></tr>
    </thead>
    <tbody>
      {#each $containers as c (c.id)}
        <tr>
          <td class="ct-name">{c.name}</td>
          <td class="ct-dim">{c.image}</td>
          <td>
            <span class="state {c.state === 'running' ? 'ok' : 'err'}">{c.status}</span>
          </td>
          <td class="ct-dim">{mem(c.name)}</td>
          <td class="ct-actions">
            {#if c.state === 'running'}
              <button onclick={() => act(c.name, 'restart')} disabled={busy[c.name]}>Neustart</button>
              <button class="danger" onclick={() => act(c.name, 'stop')} disabled={busy[c.name]}>Stopp</button>
            {:else}
              <button onclick={() => act(c.name, 'start')} disabled={busy[c.name]}>Start</button>
            {/if}
            {#if feedback[c.name]}<span class="fb">{feedback[c.name]}</span>{/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  .ct-table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
  .ct-table th {
    text-align: left; color: var(--text-dim); font-weight: 500;
    padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--panel-border);
  }
  .ct-table td { padding: 0.5rem 0.6rem; border-bottom: 1px solid var(--panel-border); }
  .ct-name { font-weight: 600; }
  .ct-dim { color: var(--text-dim); }
  .state.ok { color: var(--accent); }
  .state.err { color: var(--err); }
  .ct-actions button {
    background: var(--panel-border); border: none; border-radius: 5px;
    color: var(--text); padding: 0.3rem 0.7rem; cursor: pointer;
    font-size: 0.8rem; margin-right: 0.4rem;
  }
  .ct-actions button:hover { background: #3a4552; }
  .ct-actions button.danger:hover { background: var(--err); color: #0f1419; }
  .ct-actions button:disabled { opacity: 0.5; cursor: default; }
  .fb { font-size: 0.75rem; color: var(--text-dim); margin-left: 0.3rem; }
</style>
