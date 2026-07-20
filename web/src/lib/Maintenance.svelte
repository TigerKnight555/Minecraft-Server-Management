<script>
  import { onMount } from 'svelte'
  import { listMaintenance, createMaintenance, endMaintenance, deleteMaintenance } from './stream.js'

  let windows = $state([])
  let active = $state(false)
  let error = $state('')
  let form = $state({ name: '', start: '', end: '' })

  async function refresh() {
    try {
      const res = await listMaintenance()
      windows = res.windows
      active = res.active
      error = ''
    } catch (err) {
      error = err.message
    }
  }

  onMount(() => {
    refresh()
    const t = setInterval(refresh, 15000)
    return () => clearInterval(t)
  })

  async function submit(e) {
    e.preventDefault()
    try {
      await createMaintenance(form)
      form = { name: '', start: '', end: '' }
      await refresh()
    } catch (err) {
      error = err.message
    }
  }

  async function endNow(w) {
    if (!confirm(`Fenster "${w.name}" jetzt beenden? Der Server wird wieder gestartet.`)) return
    try {
      await endMaintenance(w.id)
      await refresh()
    } catch (err) {
      error = err.message
    }
  }

  async function remove(w) {
    if (!confirm(`Fenster "${w.name}" löschen?`)) return
    try {
      await deleteMaintenance(w.id)
      await refresh()
    } catch (err) {
      error = err.message
    }
  }

  function fmt(t) {
    return new Date(t).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' })
  }
  function status(w) {
    const now = Date.now()
    if (w.ended) return 'beendet'
    if (new Date(w.start).getTime() > now) return 'geplant'
    if (new Date(w.end).getTime() > now) return 'AKTIV'
    return 'läuft aus'
  }
</script>

<h2 style="margin-top: 1.2rem">Wartungsfenster</h2>
{#if active}
  <div class="maint-banner">🔧 Wartungsfenster aktiv — Server offline, Alarme stumm.</div>
{/if}
{#if error}<div class="err-msg">{error}</div>{/if}

<form class="routine-form" onsubmit={submit}>
  <label>Name <input bind:value={form.name} required placeholder="z. B. Arbeiten am Stromkasten" /></label>
  <label>Von <input type="datetime-local" bind:value={form.start} required /></label>
  <label>Bis <input type="datetime-local" bind:value={form.end} required /></label>
  <button type="submit">Ankündigen</button>
  <span class="dim">Spieler-Warnungen 30/15/5/1 min vorher · Alarme während des Fensters stumm · Server startet zum Ende automatisch</span>
</form>

{#if windows.length > 0}
  <table class="rt-table">
    <thead><tr><th>Name</th><th>Von</th><th>Bis</th><th>Status</th><th></th></tr></thead>
    <tbody>
      {#each windows as w (w.id)}
        <tr class={w.ended ? 'disabled' : ''}>
          <td>{w.name}</td>
          <td class="mono">{fmt(w.start)}</td>
          <td class="mono">{fmt(w.end)}</td>
          <td>{status(w)}</td>
          <td class="rt-actions">
            {#if status(w) === 'AKTIV'}
              <button onclick={() => endNow(w)}>Vorzeitig beenden</button>
            {/if}
            {#if !w.started || w.ended}
              <button class="danger" onclick={() => remove(w)}>Löschen</button>
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}

<style>
  .maint-banner {
    background: #7c2d12; color: #fed7aa; border-radius: 6px;
    padding: 0.6rem 0.9rem; margin-bottom: 0.8rem; font-weight: 600;
  }
  .routine-form {
    display: flex; flex-wrap: wrap; gap: 0.8rem; align-items: end;
    background: #0a0e12; border-radius: 6px; padding: 0.8rem; margin-bottom: 1rem;
  }
  .routine-form label {
    display: flex; flex-direction: column; gap: 0.25rem;
    font-size: 0.75rem; color: var(--text-dim);
  }
  .routine-form input {
    background: var(--panel); border: 1px solid var(--panel-border);
    border-radius: 5px; color: var(--text); padding: 0.4rem 0.6rem; font-size: 0.85rem;
  }
  .routine-form button {
    background: var(--accent); color: #0f1419; border: none; border-radius: 5px;
    padding: 0.45rem 1rem; font-weight: 600; cursor: pointer;
  }
  .rt-table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
  .rt-table th {
    text-align: left; color: var(--text-dim); font-weight: 500;
    padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--panel-border);
  }
  .rt-table td { padding: 0.45rem 0.6rem; border-bottom: 1px solid var(--panel-border); }
  .rt-table tr.disabled td { opacity: 0.45; }
  .mono { font-family: ui-monospace, monospace; font-size: 0.8rem; }
  .dim { color: var(--text-dim); font-size: 0.75rem; }
  .rt-actions button {
    background: var(--panel-border); border: none; border-radius: 5px;
    color: var(--text); padding: 0.25rem 0.6rem; cursor: pointer;
    font-size: 0.75rem; margin-right: 0.3rem;
  }
  .rt-actions button.danger:hover { background: var(--err); color: #0f1419; }
  .err-msg { color: var(--err); font-size: 0.85rem; margin-bottom: 0.6rem; }
</style>
