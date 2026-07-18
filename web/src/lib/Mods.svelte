<script>
  import { onMount } from 'svelte'
  import {
    listMods, checkMods, stageMods, applyMods, rollbackMods,
    versionWatch, versionWatchCheck,
  } from './stream.js'

  let profile = $state('server')
  let entries = $state([])
  let watch = $state(null)
  let busy = $state(false)
  let error = $state('')
  let info = $state('')

  let updatable = $derived(entries.filter((e) => e.updateVersion))
  let stagedCount = $derived(entries.filter((e) => e.staged).length)

  async function load() {
    try {
      entries = await listMods(profile)
      const w = await versionWatch()
      watch = w?.checked ? w : null
    } catch (err) {
      error = err.message
    }
  }

  onMount(load)
  $effect(() => { profile; load() })

  async function run(fn, okMsg) {
    busy = true
    error = ''
    info = ''
    try {
      const result = await fn()
      info = okMsg(result)
      entries = await listMods(profile)
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }

  const doCheck = () => run(() => checkMods(profile).then((e) => (entries = e)),
    () => 'Update-Check abgeschlossen')
  const doStageAll = () => run(() => stageMods(profile, []),
    (r) => `${r.staged} Update(s) heruntergeladen und verifiziert (Staging)`)
  const doStageOne = (f) => run(() => stageMods(profile, [f]),
    (r) => `${r.staged} Update gestaged`)
  const doRollback = () => {
    if (!confirm('Letztes Mod-Backup wiederherstellen? Danach Server neu starten!')) return
    run(() => rollbackMods(profile), (r) => `${r.restored} Datei(en) zurückgerollt — Neustart nötig`)
  }
  const doApply = () => {
    const restart = profile === 'server'
    const msg = restart
      ? 'Gestagte Updates einsetzen und den Minecraft-Server NEU STARTEN? Spieler werden getrennt.'
      : 'Gestagte Updates ins Client-Paket einsetzen?'
    if (!confirm(msg)) return
    run(() => applyMods(profile, restart),
      (r) => `${r.applied} Update(s) eingesetzt (Backup ${r.backup})${r.restarted ? ', Server startet neu' : ''}`)
  }
  const doWatchCheck = () => run(async () => (watch = await versionWatchCheck()),
    () => 'Versions-Check abgeschlossen')
</script>

<div class="panel wide">
  <h2>Minecraft-Version</h2>
  {#if watch}
    <div class="watch">
      {#if watch.newerAvailable}
        <div class="watch-line">
          <strong>{watch.latestVersion} verfügbar</strong> (aktuell {watch.currentVersion}) —
          Loader {watch.loaderReady ? '✓' : '✗'}
          {#each watch.profiles as p (p.profile)}
            · {p.profile}-Mods {p.ready}/{p.total}
          {/each}
        </div>
        {#if Object.keys(watch.stragglers ?? {}).length}
          <div class="stragglers">
            Nachzügler: {Object.entries(watch.stragglers).map(([m, p]) => `${m} (${p})`).join(', ')}
          </div>
        {/if}
      {:else}
        <div class="watch-line ok-text">Aktuell — {watch.currentVersion} ist die neueste Release.</div>
      {/if}
      <div class="dim-sm">Geprüft: {new Date(watch.checked).toLocaleString('de-DE')}</div>
    </div>
  {:else}
    <div class="empty">Noch kein Versions-Check gelaufen.</div>
  {/if}
  <button class="btn" onclick={doWatchCheck} disabled={busy}>Jetzt prüfen</button>
</div>

<div class="panel wide">
  <h2>
    Mod-Profile
    <select bind:value={profile} style="margin-left: 0.8rem">
      <option value="server">Server</option>
      <option value="client">Client-Paket</option>
    </select>
  </h2>

  {#if error}<div class="err-msg">{error}</div>{/if}
  {#if info}<div class="info-msg">{info}</div>{/if}

  <div class="mod-actions">
    <button class="btn" onclick={doCheck} disabled={busy}>Updates prüfen</button>
    <button class="btn" onclick={doStageAll} disabled={busy || updatable.length === 0}>
      Alle stagen ({updatable.length})
    </button>
    <button class="btn accent" onclick={doApply} disabled={busy || stagedCount === 0}>
      {profile === 'server' ? 'Anwenden + Neustart' : 'Anwenden'} ({stagedCount})
    </button>
    <button class="btn danger" onclick={doRollback} disabled={busy}>Rollback</button>
  </div>

  {#if entries.length === 0}
    <div class="empty">Keine Daten — „Updates prüfen" ausführen.</div>
  {:else}
    <table class="mod-table">
      <thead>
        <tr><th>Datei</th><th>Mod</th><th>Version</th><th>Update</th><th></th></tr>
      </thead>
      <tbody>
        {#each entries as e (e.category + '/' + e.filename)}
          <tr class={e.managed ? '' : 'unmanaged'}>
            <td class="mono">{e.filename}<span class="dim-sm"> {e.category !== 'mods' ? '· ' + e.category : ''}</span></td>
            <td>{e.name || '—'}</td>
            <td class="mono">{e.version || '?'}</td>
            <td>
              {#if e.staged}
                <span class="badge staged">gestaged</span>
              {:else if e.updateVersion}
                <span class="badge update">→ {e.updateVersion}</span>
              {:else if e.managed}
                <span class="badge ok">aktuell</span>
              {:else}
                <span class="badge unmanaged" title="Nicht auf Modrinth gefunden — wird nie angefasst">unverwaltet</span>
              {/if}
            </td>
            <td>
              {#if e.updateVersion && !e.staged}
                <button class="btn sm" onclick={() => doStageOne(e.filename)} disabled={busy}>Stagen</button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<style>
  .watch-line { font-size: 0.95rem; margin-bottom: 0.3rem; }
  .ok-text { color: var(--accent); }
  .stragglers { font-size: 0.8rem; color: var(--warn); margin-bottom: 0.3rem; }
  .dim-sm { font-size: 0.75rem; color: var(--text-dim); }
  .mod-actions { display: flex; gap: 0.5rem; margin-bottom: 0.8rem; flex-wrap: wrap; }
  .btn {
    background: var(--panel-border); border: none; border-radius: 5px;
    color: var(--text); padding: 0.4rem 0.9rem; cursor: pointer; font-size: 0.85rem;
    margin-top: 0.4rem;
  }
  .btn:hover { background: #3a4552; }
  .btn:disabled { opacity: 0.4; cursor: default; }
  .btn.accent { background: var(--accent); color: #0f1419; font-weight: 600; }
  .btn.danger:hover { background: var(--err); color: #0f1419; }
  .btn.sm { padding: 0.2rem 0.6rem; font-size: 0.75rem; margin: 0; }
  .mod-table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
  .mod-table th {
    text-align: left; color: var(--text-dim); font-weight: 500;
    padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--panel-border);
  }
  .mod-table td { padding: 0.45rem 0.6rem; border-bottom: 1px solid var(--panel-border); }
  .mod-table tr.unmanaged td { opacity: 0.55; }
  .mono { font-family: ui-monospace, monospace; font-size: 0.8rem; }
  .badge {
    display: inline-block; padding: 0.1rem 0.5rem; border-radius: 10px;
    font-size: 0.72rem; font-weight: 600;
  }
  .badge.ok { background: #1c3829; color: var(--accent); }
  .badge.update { background: #3a2f14; color: var(--warn); }
  .badge.staged { background: #14293a; color: #60a5fa; }
  .badge.unmanaged { background: var(--panel-border); color: var(--text-dim); }
  .err-msg { color: var(--err); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .info-msg { color: var(--accent); font-size: 0.85rem; margin-bottom: 0.6rem; }
</style>
