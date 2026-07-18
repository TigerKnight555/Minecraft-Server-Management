<script>
  import { onMount } from 'svelte'
  import { listRoutines, createRoutine, updateRoutine, deleteRoutine, runRoutine, recentRuns, restorePlayer, listRestorePlayers } from './stream.js'

  let routines = $state([])
  let runs = $state([])
  let error = $state('')
  let showForm = $state(false)

  let form = $state(emptyForm())
  function emptyForm() {
    return {
      name: '', cron: '30 4 * * *', kind: 'rcon', payload: '', warnMinutes: 5, enabled: true,
      skipIfPlayersOnline: false, waitForEmpty: false, waitDeadline: '',
      applyStaged: false, watchdogMinutes: 5,
    }
  }

  const kindLabel = {
    rcon: 'RCON-Befehl',
    restart: 'Container-Neustart',
    'announce-restart': 'Angekündigter Neustart',
    backup: 'Backup (restic)',
  }

  async function refresh() {
    try {
      routines = await listRoutines()
      runs = await recentRuns()
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
      await createRoutine({
        ...form,
        warnMinutes: Number(form.warnMinutes),
        watchdogMinutes: form.kind === 'announce-restart' ? Number(form.watchdogMinutes) : 0,
      })
      form = emptyForm()
      showForm = false
      await refresh()
    } catch (err) {
      error = err.message
    }
  }

  // kompakte Badges für die Stufe-2-Optionen in der Tabelle
  function optionBadges(r) {
    const out = []
    if (r.skipIfPlayersOnline) out.push('überspringt bei Spielern')
    if (r.waitForEmpty) out.push(`wartet auf leer${r.waitDeadline ? ` bis ${r.waitDeadline}` : ''}`)
    if (r.applyStaged) out.push('spielt Updates ein')
    if (r.watchdogMinutes > 0) out.push(`Watchdog ${r.watchdogMinutes} min`)
    return out
  }

  async function toggle(r) {
    try {
      await updateRoutine({ ...r, enabled: !r.enabled })
      await refresh()
    } catch (err) {
      error = err.message
    }
  }

  async function remove(r) {
    if (!confirm(`Routine "${r.name}" löschen?`)) return
    try {
      await deleteRoutine(r.id)
      await refresh()
    } catch (err) {
      error = err.message
    }
  }

  async function runNow(r) {
    if (!confirm(`Routine "${r.name}" jetzt ausführen?`)) return
    try {
      await runRoutine(r.id)
      setTimeout(refresh, 1000)
    } catch (err) {
      error = err.message
    }
  }

  function routineName(id) {
    return routines.find((r) => r.id === id)?.name ?? `#${id}`
  }
  function fmtTime(t) {
    return new Date(t).toLocaleString('de-DE')
  }

  // --- Einzeldatei-Restore (Phase 4.4) ---
  let restoreUUID = $state('')
  let restoreBusy = $state(false)
  let restoreResult = $state('')
  let restoreError = $state('')
  let players = $state([])

  async function loadPlayers() {
    try {
      players = await listRestorePlayers()
    } catch {
      players = [] // Endpoint aus (kein Daten-Mount) -> manuelles UUID-Feld
    }
  }
  onMount(loadPlayers)

  function playerLabel(p) {
    const name = p.name || `(unbekannt) ${p.uuid}`
    const seen = p.lastSaved ? new Date(p.lastSaved).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' }) : ''
    return seen ? `${name} — zuletzt ${seen}` : name
  }

  function playerName(uuid) {
    return players.find((p) => p.uuid === uuid)?.name || uuid
  }

  async function doRestore(e) {
    e.preventDefault()
    if (!confirm(`Spielerdaten für ${playerName(restoreUUID)} aus dem letzten Backup wiederherstellen?\nDer Spieler muss offline sein!`)) return
    restoreBusy = true
    restoreResult = ''
    restoreError = ''
    try {
      const res = await restorePlayer(restoreUUID.trim())
      restoreResult = res.message
      restoreUUID = ''
    } catch (err) {
      restoreError = err.message
    } finally {
      restoreBusy = false
    }
  }
</script>

<div class="panel wide">
  <h2>
    Routinen
    <button class="add" onclick={() => (showForm = !showForm)}>{showForm ? 'Abbrechen' : '+ Neu'}</button>
  </h2>

  {#if error}<div class="err-msg">{error}</div>{/if}

  {#if showForm}
    <form class="routine-form" onsubmit={submit}>
      <label>Name <input bind:value={form.name} required placeholder="z. B. Nachtneustart" /></label>
      <label>Cron <input bind:value={form.cron} required placeholder="30 4 * * *" /></label>
      <label>Typ
        <select bind:value={form.kind}>
          {#each Object.entries(kindLabel) as [k, l]}<option value={k}>{l}</option>{/each}
        </select>
      </label>
      <label>{form.kind === 'rcon' ? 'Befehl' : 'Container'}
        <input bind:value={form.payload} required placeholder={form.kind === 'rcon' ? 'save-all' : 'mc-fabric'} />
      </label>
      {#if form.kind === 'announce-restart' || form.kind === 'backup'}
        <label>Vorwarnung (Min.) <input type="number" bind:value={form.warnMinutes} min="0" max="60" /></label>
        <label>Watchdog (Min., 0 = aus) <input type="number" bind:value={form.watchdogMinutes} min="0" max="30" /></label>
        <div class="conditions">
          <label class="check"><input type="checkbox" bind:checked={form.skipIfPlayersOnline} /> Überspringen, wenn Spieler online</label>
          <label class="check"><input type="checkbox" bind:checked={form.waitForEmpty} /> Auf leeren Server warten</label>
          {#if form.waitForEmpty}
            <label>höchstens bis (HH:MM) <input bind:value={form.waitDeadline} placeholder="06:00" pattern="[0-2][0-9]:[0-5][0-9]" /></label>
          {/if}
          <label class="check"><input type="checkbox" bind:checked={form.applyStaged} /> Gestagte Mod-Updates beim Neustart einspielen</label>
        </div>
      {/if}
      <button type="submit">Anlegen</button>
    </form>
  {/if}

  {#if routines.length === 0}
    <div class="empty">Noch keine Routinen. Beispiel: täglicher Neustart um 04:30 mit 5 Minuten Vorwarnung.</div>
  {:else}
    <table class="rt-table">
      <thead>
        <tr><th>Name</th><th>Cron</th><th>Typ</th><th>Ziel/Befehl</th><th></th></tr>
      </thead>
      <tbody>
        {#each routines as r (r.id)}
          <tr class={r.enabled ? '' : 'disabled'}>
            <td>{r.name}</td>
            <td class="mono">{r.cron}</td>
            <td>{kindLabel[r.kind] ?? r.kind}</td>
            <td class="mono">
              {r.payload}{r.kind === 'announce-restart' ? ` (${r.warnMinutes} min)` : ''}
              {#each optionBadges(r) as b}<span class="badge">{b}</span>{/each}
            </td>
            <td class="rt-actions">
              <button onclick={() => toggle(r)}>{r.enabled ? 'Deaktivieren' : 'Aktivieren'}</button>
              <button onclick={() => runNow(r)}>Jetzt</button>
              <button class="danger" onclick={() => remove(r)}>Löschen</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  <h2 style="margin-top: 1.2rem">Spielerdaten wiederherstellen</h2>
  <form class="routine-form" onsubmit={doRestore}>
    {#if players.length > 0}
      <label>Spieler
        <select bind:value={restoreUUID} required style="min-width: 18rem">
          <option value="" disabled selected>— Spieler wählen —</option>
          {#each players as p (p.uuid)}
            <option value={p.uuid}>{playerLabel(p)}</option>
          {/each}
        </select>
      </label>
    {:else}
      <label>Spieler-UUID (mit Bindestrichen)
        <input bind:value={restoreUUID} required placeholder="069a79f4-44e9-4726-a5be-fca90e38aaf5"
          pattern="[0-9a-fA-F]{'{'}8{'}'}-[0-9a-fA-F]{'{'}4{'}'}-[0-9a-fA-F]{'{'}4{'}'}-[0-9a-fA-F]{'{'}4{'}'}-[0-9a-fA-F]{'{'}12{'}'}"
          style="min-width: 21rem" />
      </label>
    {/if}
    <button type="submit" disabled={restoreBusy || !restoreUUID}>{restoreBusy ? 'Läuft…' : 'Aus letztem Backup wiederherstellen'}</button>
    <span class="dim">Spieler muss offline sein. Aktuelle Datei wird als .pre-restore-* gesichert.</span>
  </form>
  {#if restoreResult}<div class="ok-msg">{restoreResult}</div>{/if}
  {#if restoreError}<div class="err-msg">{restoreError}</div>{/if}

  <h2 style="margin-top: 1.2rem">Letzte Ausführungen</h2>
  {#if runs.length === 0}
    <div class="empty">Noch keine Ausführungen</div>
  {:else}
    <table class="rt-table">
      <tbody>
        {#each runs as run (run.id)}
          <tr>
            <td class="mono">{fmtTime(run.time)}</td>
            <td>{routineName(run.routineId)}</td>
            <td><span class={run.ok ? 'ok' : 'bad'}>{run.ok ? 'OK' : 'FEHLER'}</span></td>
            <td class="dim">{run.message}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<style>
  .add {
    background: var(--panel-border); border: none; border-radius: 5px;
    color: var(--text); padding: 0.25rem 0.7rem; cursor: pointer;
    font-size: 0.75rem; margin-left: 0.6rem; text-transform: none; letter-spacing: 0;
  }
  .routine-form {
    display: flex; flex-wrap: wrap; gap: 0.8rem; align-items: end;
    background: #0a0e12; border-radius: 6px; padding: 0.8rem; margin-bottom: 1rem;
  }
  .routine-form label {
    display: flex; flex-direction: column; gap: 0.25rem;
    font-size: 0.75rem; color: var(--text-dim);
  }
  .routine-form input, .routine-form select {
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
  .dim { color: var(--text-dim); font-size: 0.8rem; }
  .ok { color: var(--accent); font-weight: 600; }
  .bad { color: var(--err); font-weight: 600; }
  .rt-actions button {
    background: var(--panel-border); border: none; border-radius: 5px;
    color: var(--text); padding: 0.25rem 0.6rem; cursor: pointer;
    font-size: 0.75rem; margin-right: 0.3rem;
  }
  .rt-actions button.danger:hover { background: var(--err); color: #0f1419; }
  .err-msg { color: var(--err); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .ok-msg { color: var(--accent); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .conditions {
    display: flex; flex-wrap: wrap; gap: 0.6rem; align-items: center;
    width: 100%; padding-top: 0.2rem; border-top: 1px dashed var(--panel-border);
  }
  .conditions label.check {
    flex-direction: row; align-items: center; gap: 0.35rem; font-size: 0.78rem;
  }
  .badge {
    display: inline-block; background: var(--panel-border); border-radius: 4px;
    font-size: 0.68rem; padding: 0.1rem 0.4rem; margin-left: 0.35rem;
    color: var(--text-dim); font-family: inherit;
  }
</style>
