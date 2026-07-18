<script>
  import { onMount } from 'svelte'
  import { auditLog } from './stream.js'

  let entries = $state([])
  let error = $state('')

  async function refresh() {
    try {
      entries = await auditLog()
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

  function fmtTime(t) {
    return new Date(t).toLocaleString('de-DE')
  }
</script>

<div class="panel wide">
  <h2>Audit-Log</h2>
  {#if error}<div class="err-msg">{error}</div>{/if}
  {#if entries.length === 0}
    <div class="empty">Noch keine Einträge — jede Aktion (RCON, Start/Stopp, Routinen) landet hier.</div>
  {:else}
    <table class="audit-table">
      <tbody>
        {#each entries as e (e.id)}
          <tr>
            <td class="mono">{fmtTime(e.time)}</td>
            <td class="action">{e.action}</td>
            <td class="dim">{e.detail}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<style>
  .audit-table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
  .audit-table td { padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--panel-border); }
  .mono { font-family: ui-monospace, monospace; font-size: 0.8rem; white-space: nowrap; }
  .action { font-weight: 600; }
  .dim { color: var(--text-dim); }
  .err-msg { color: var(--err); font-size: 0.85rem; margin-bottom: 0.6rem; }
</style>
