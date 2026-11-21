<script>
  import { onMount } from 'svelte'
  import { basepackStatus, basepackSearch, basepackPreview, basepackApply, basepackConfigs, basepackRemove } from './stream.js'

  let { onchange } = $props()
  let enabled = $state(true)
  let status = $state(null)
  let query = $state('')
  let hits = $state([])
  let searched = $state(false)
  let preview = $state(null)
  let configs = $state({})
  let cfgEdit = $state(null) // Config-Auswahl der aktuellen Version ändern (Pfad -> an)
  let busy = $state(false)
  let blocked = $state(false) // 409: MC-Upgrade läuft -> nur ändernde Aktionen gesperrt
  const BLOCKED_HINT = 'MC-Update läuft — Basis-Pack-Aktionen sind gesperrt'
  let error = $state('')
  let info = $state('')

  // Gibt true zurück, wenn der Status geladen werden konnte.
  async function load() {
    try {
      status = await basepackStatus()
      blocked = false
      error = ''
      return true
    } catch (err) {
      // Nur "Feature nicht vorhanden" (Route nicht registriert) blendet den Bereich aus;
      // 503 & Co. sind vorübergehend -> Fehler + "Erneut versuchen"
      if (err.status === 404) enabled = false
      else error = err.message
      return false
    }
  }
  onMount(load)
  export const reload = load

  async function guard(fn) {
    if (busy) return
    busy = true
    error = ''
    info = ''
    try {
      await fn()
    } catch (err) {
      error = err.message
      if (err.status === 409) blocked = true
    } finally {
      busy = false
    }
  }

  const doSearch = (ev) => {
    ev.preventDefault()
    guard(async () => {
      hits = (await basepackSearch(query.trim())).hits ?? []
      searched = true
    })
  }
  const openPreview = (projectId) => guard(async () => {
    cfgEdit = null
    preview = await basepackPreview(projectId)
    // Neue Configs sind vorab aus: der Admin entscheidet bewusst
    configs = Object.fromEntries((preview.configs ?? []).map((c) => [c.path, c.new ? false : !!c.on]))
  })
  // Genau die in der Vorschau gezeigte Version anwenden (preview.versionId)
  const doApply = () => guard(async () => {
    const res = await basepackApply(preview.projectId, preview.versionId, configs)
    info = res.message
    preview = null
    hits = []
    searched = false
    await load()
    onchange?.()
  })
  const currentConfigs = () => Object.keys(status?.state?.configs ?? {}).sort()
  const openConfigs = () => {
    preview = null
    cfgEdit = { ...(status?.state?.configs ?? {}) }
  }
  // Auswahl für die AKTUELLE Pack-Version (kein Versionswechsel)
  const doSaveConfigs = () => guard(async () => {
    await basepackConfigs(cfgEdit)
    info = 'Config-Auswahl gespeichert — wird beim nächsten Neustart an die Spieler verteilt.'
    cfgEdit = null
    await load()
    onchange?.()
  })
  const doRemove = () => {
    if (busy) return
    if (!confirm(`Basis-Modpack „${status.state.title}" entfernen? Pack-Mods und Pack-Configs gehen raus, eigene Mods bleiben.`)) return
    guard(async () => {
      await basepackRemove()
      info = 'Basis-Modpack entfernt.'
      await load()
      onchange?.()
    })
  }
  const names = (list) => list.map((f) => f.name || f.filename).join(', ')
</script>

{#if enabled}
<div class="panel wide">
  <h2>Basis-Modpack</h2>
  {#if blocked}
    <div class="warn-msg" role="status">
      {BLOCKED_HINT}
      <button class="btn sm" onclick={load} disabled={busy}>Erneut prüfen</button>
    </div>
  {/if}
  {#if error}
    <div class="err-msg" role="alert">
      {error}
      {#if !status && !blocked}<button class="btn sm" onclick={load} disabled={busy}>Erneut versuchen</button>{/if}
    </div>
  {/if}
  {#if info}<div class="info-msg" role="status">{info}</div>{/if}

  {#if status?.state}
    <p>
      <strong>{status.state.title}</strong> {status.state.versionNumber}
      <span class="dim-sm">(MC {status.state.mc} · {status.state.files?.length ?? 0} Dateien · {status.state.disabled?.length ?? 0} abgeschaltet)</span>
    </p>
    <div class="row">
      {#if status.checkError}
        <span class="warn-msg">Update-Prüfung fehlgeschlagen — Modrinth nicht erreichbar</span>
      {:else if status.available}
        <button class="btn accent" onclick={() => openPreview(status.state.projectId)} disabled={busy || blocked}>
          {status.available.versionNumber} verfügbar — Vorschau
        </button>
      {:else}
        <span class="dim-sm">✔ aktuell</span>
      {/if}
      {#if currentConfigs().length}
        <button class="btn" onclick={openConfigs} disabled={busy || blocked}>Configs ändern</button>
      {/if}
      {#if !status.available}
        <button class="btn" onclick={() => openPreview(status.state.projectId)} disabled={busy || blocked}>Neu laden</button>
      {/if}
      <button class="btn danger" onclick={doRemove} disabled={busy || blocked}>Entfernen</button>
    </div>
  {:else}
    <p class="dim-sm">Ein fertiges Modrinth-Modpack (z. B. Fabulously Optimized) als Grundlage des Client-Pakets: Mods kommen als getestete Kombination, eigene Mods bleiben, einzelne Pack-Mods lassen sich abschalten.</p>
    <form class="row" onsubmit={doSearch}>
      <input type="text" bind:value={query} aria-label="Modpack suchen" placeholder="Modpack auf Modrinth suchen, z. B. Fabulously Optimized" />
      <button class="btn" type="submit" disabled={busy || query.trim().length < 2}>Suchen</button>
    </form>
    {#if searched && hits.length === 0}<p class="dim-sm">Keine Treffer für die aktuelle Minecraft-Version.</p>{/if}
    {#each hits as h (h.project_id)}
      <div class="row">
        <strong>{h.title}</strong> <span class="dim-sm">{(h.downloads ?? 0).toLocaleString('de-DE')} Downloads</span>
        <button class="btn sm" onclick={() => openPreview(h.project_id)} disabled={busy}>Vorschau</button>
      </div>
    {/each}
  {/if}

  {#if cfgEdit}
    <div class="dialog">
      <h3>Configs von {status?.state?.title} {status?.state?.versionNumber}</h3>
      <p class="dim-sm">Gilt für die aktuelle Pack-Version. Mitgelieferte Configs überschreiben bei jedem Paket-Update die Spieler-Datei.</p>
      {#each currentConfigs() as path (path)}
        <label class="cfg"><input type="checkbox" bind:checked={cfgEdit[path]} disabled={busy} /> <code>{path}</code></label>
      {/each}
      {#if busy}<p class="dim-sm" role="status">Speichere Auswahl und schreibe Configs …</p>{/if}
      <div class="row">
        <button class="btn accent" onclick={doSaveConfigs} disabled={busy || blocked}>Speichern</button>
        <button class="btn" onclick={() => (cfgEdit = null)} disabled={busy}>Abbrechen</button>
      </div>
    </div>
  {/if}

  {#if busy && !preview && !cfgEdit}<p class="dim-sm" role="status">Lade Modpack und prüfe Dateien …</p>{/if}

  {#if preview}
    <div class="dialog">
      <h3>{preview.title} {preview.versionNumber} <span class="dim-sm">(MC {preview.mc}, {preview.total} Dateien)</span></h3>
      {#if preview.plan.add?.length}<p><strong>Neu:</strong> {names(preview.plan.add)}</p>{/if}
      {#if preview.plan.remove?.length}<p><strong>Entfernt:</strong> {names(preview.plan.remove)}</p>{/if}
      {#if preview.plan.update?.length}<p><strong>Aktualisiert:</strong> {preview.plan.update.length} Dateien</p>{/if}
      {#if preview.plan.absorbOwn?.length}<p class="warn-msg">Übernimmt deine eigenen Mods: {names(preview.plan.absorbOwn)} (eigene Kopie fällt weg)</p>{/if}
      {#if preview.plan.dropAbsorbed?.length}<p class="warn-msg">Entfällt (war zuvor deine eigene Mod; bei Bedarf neu hinzufügen): {preview.plan.dropAbsorbed.join(', ')}</p>{/if}
      {#if preview.ignored?.length}<p class="dim-sm">Ignoriert (unbekannte Ordner): {preview.ignored.join(', ')}</p>{/if}
      {#if preview.configs?.length}
        <h4>Configs</h4>
        <p class="dim-sm">Mitgelieferte Configs überschreiben bei jedem Paket-Update die Spieler-Datei. Neue Configs sind vorab aus.</p>
        {#each preview.configs as c (c.path)}
          <label class="cfg"><input type="checkbox" bind:checked={configs[c.path]} /> <code>{c.path}</code>{#if c.new} <span class="badge">neu</span>{/if}</label>
        {/each}
      {/if}
      <div class="row">
        <button class="btn accent" onclick={doApply} disabled={busy || blocked}>Übernehmen</button>
        <button class="btn" onclick={() => (preview = null)} disabled={busy}>Abbrechen</button>
      </div>
    </div>
  {/if}
</div>
{/if}

<style>
  .row { display: flex; gap: 0.6rem; align-items: center; flex-wrap: wrap; margin-bottom: 0.5rem; }
  input[type='text'] { min-width: min(24rem, 100%); }
  .btn { background: var(--panel-border); border: none; border-radius: 5px; color: var(--text); padding: 0.4rem 0.9rem; cursor: pointer; font-size: 0.85rem; }
  .btn:disabled { opacity: 0.4; cursor: default; }
  .btn.accent { background: var(--accent); color: #0f1419; font-weight: 600; }
  .btn.danger { background: #5a2a2a; }
  .btn.sm { padding: 0.2rem 0.6rem; font-size: 0.75rem; }
  .dialog { border: 1px solid var(--accent); border-radius: 6px; padding: 0.6rem 0.8rem; margin: 0.6rem 0; }
  .cfg { display: block; font-size: 0.8rem; margin: 0.15rem 0; }
  .badge { font-size: 0.7rem; background: var(--warn); color: #0f1419; border-radius: 3px; padding: 0 0.3rem; }
  .dim-sm { font-size: 0.75rem; color: var(--text-dim); }
  .err-msg { color: var(--err); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .info-msg { color: var(--accent); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .warn-msg { color: var(--warn); font-size: 0.85rem; }
</style>
