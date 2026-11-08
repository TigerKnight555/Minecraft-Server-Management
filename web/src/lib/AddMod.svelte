<script>
  import { searchMods, planMod, addMod, uploadMod, applyInfo } from './stream.js'

  let { onchange } = $props()

  let query = $state('')
  let hits = $state([])
  let mcVersion = $state('')
  let searched = $state(false)
  let busy = $state(false)
  let error = $state('')
  let info = $state('')

  let plan = $state(null)
  let wantServer = $state(false)
  let wantClient = $state(false)
  let apply = $state(null)

  let upFile = $state(null)
  let upServer = $state(true)
  let upClient = $state(false)
  let upCategory = $state('mods')
  $effect(() => { if (upServer) upCategory = 'mods' })

  const sideLabel = { required: 'nötig', optional: 'optional', unsupported: 'nicht nötig', unknown: 'unbekannt' }

  const missing = (profile) => (plan?.deps ?? []).filter((d) => !d.present?.[profile])
  let depNames = $derived.by(() => {
    if (!plan) return []
    const names = new Set()
    if (wantServer) missing('server').forEach((d) => names.add(d.name))
    if (wantClient) missing('client').forEach((d) => names.add(d.name))
    return [...names]
  })
  let canAdd = $derived(plan && !plan.blocked && (wantServer || wantClient) && !busy)

  async function guard(fn) {
    busy = true
    error = ''
    info = ''
    try {
      await fn()
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }

  function doSearch(ev) {
    ev.preventDefault()
    if (query.trim().length < 2) return
    guard(async () => {
      const res = await searchMods(query.trim())
      hits = res.hits
      mcVersion = res.mcVersion
      searched = true
    })
  }

  const openPlan = (hit) => guard(async () => {
    plan = await planMod(hit.project_id)
    wantServer = plan.server && !plan.main.present?.server
    wantClient = plan.client && !plan.main.present?.client
    apply = await applyInfo().catch(() => null)
  })

  const confirmAdd = () => guard(async () => {
    const profiles = []
    if (wantServer) profiles.push('server')
    if (wantClient) profiles.push('client')
    const res = await addMod(plan.main.projectId, profiles)
    info = 'Hinzugefügt — ' + Object.entries(res.added)
      .map(([p, names]) => `${p === 'server' ? 'Server (vorgemerkt)' : 'Client-Paket'}: ${names.join(', ')}`)
      .join(' · ')
    plan = null
    onchange?.()
  })

  const doUpload = () => guard(async () => {
    const profiles = []
    if (upServer) profiles.push('server')
    if (upClient) profiles.push('client')
    const res = await uploadMod(upFile, profiles, upCategory)
    info = `Hochgeladen: ${res.name}` + (res.managed
      ? ' (auf Modrinth erkannt — bekommt Updates)'
      : ' (unverwaltet — keine automatischen Updates)')
    upFile = null
    onchange?.()
  })

  function applyText() {
    if (!apply?.routine) {
      return '⚠️ Keine Routine übernimmt Vorgemerktes — „Anwenden + Neustart" nutzen oder in einer Routine „Vorgemerkte Updates anwenden" aktivieren.'
    }
    return `Wird aktiv: ${new Date(apply.next).toLocaleString('de-DE')} (Routine „${apply.routine}") — oder sofort per „Anwenden + Neustart".`
  }
</script>

<div class="panel wide">
  <h2>Mod hinzufügen</h2>
  {#if error}<div class="err-msg">{error}</div>{/if}
  {#if info}<div class="info-msg">{info}</div>{/if}

  <form class="row" onsubmit={doSearch}>
    <input type="text" bind:value={query} placeholder="Auf Modrinth suchen, z. B. Jade" />
    <button class="btn" type="submit" disabled={busy || query.trim().length < 2}>Suchen</button>
  </form>

  {#if plan}
    <div class="dialog">
      <h3>{plan.main.name} <span class="dim-sm">{plan.main.versionNumber}</span></h3>
      {#if plan.blocked}
        <div class="err-msg">{plan.blocked}</div>
      {:else}
        <div class="row">
          <label>
            <input type="checkbox" bind:checked={wantServer} disabled={plan.main.present?.server} />
            Server
            <span class="dim-sm">({plan.main.present?.server ? 'schon vorhanden' : 'Modrinth: ' + (sideLabel[plan.main.serverSide] ?? plan.main.serverSide)})</span>
          </label>
          <label>
            <input type="checkbox" bind:checked={wantClient} disabled={plan.main.present?.client} />
            Client-Paket
            <span class="dim-sm">({plan.main.present?.client ? 'schon vorhanden' : 'Modrinth: ' + (sideLabel[plan.main.clientSide] ?? plan.main.clientSide)})</span>
          </label>
        </div>
        {#if depNames.length}
          <p>Wird mit installiert: <strong>{depNames.join(', ')}</strong></p>
        {/if}
        {#each plan.warnings as w}
          <div class="warn-msg">⚠️ {w}</div>
        {/each}
        {#if wantServer}<p class="dim-sm">{applyText()}</p>{/if}
        {#if wantClient}<p class="dim-sm">Client-Paket: sofort drin, wird beim nächsten Neustart automatisch an die Spieler verteilt.</p>{/if}
      {/if}
      <div class="row">
        <button class="btn accent" onclick={confirmAdd} disabled={!canAdd}>Hinzufügen</button>
        <button class="btn" onclick={() => (plan = null)} disabled={busy}>Abbrechen</button>
      </div>
    </div>
  {/if}

  {#if hits.length}
    <ul class="hits">
      {#each hits as h (h.project_id)}
        <li>
          {#if h.icon_url}<img src={h.icon_url} alt="" width="32" height="32" />{:else}<span class="noicon"></span>{/if}
          <div class="hit-text">
            <strong>{h.title}</strong>
            <span class="dim-sm">{(h.downloads ?? 0).toLocaleString('de-DE')} Downloads</span>
            <div class="dim-sm">{h.description}</div>
          </div>
          <button class="btn sm" onclick={() => openPlan(h)} disabled={busy}>Auswählen</button>
        </li>
      {/each}
    </ul>
  {:else if searched}
    <div class="empty">Keine Treffer mit Build für Minecraft {mcVersion}.</div>
  {/if}

  <h3>Datei hochladen <span class="dim-sm">(Notlösung, z. B. Mods von CurseForge)</span></h3>
  <div class="row">
    <input type="file" accept=".jar,.zip" onchange={(e) => (upFile = e.currentTarget.files?.[0] ?? null)} />
    <label><input type="checkbox" bind:checked={upServer} /> Server</label>
    <label><input type="checkbox" bind:checked={upClient} /> Client-Paket</label>
    {#if upClient && !upServer}
      <select bind:value={upCategory}>
        <option value="mods">Mod (.jar)</option>
        <option value="shaderpacks">Shader (.zip)</option>
        <option value="resourcepacks">Resourcepack (.zip)</option>
      </select>
    {/if}
    <button class="btn" onclick={doUpload} disabled={busy || !upFile || (!upServer && !upClient)}>Hochladen</button>
  </div>
</div>

<style>
  .row { display: flex; gap: 0.6rem; align-items: center; flex-wrap: wrap; margin-bottom: 0.5rem; }
  input[type='text'] { min-width: min(22rem, 100%); }
  .btn {
    background: var(--panel-border); border: none; border-radius: 5px;
    color: var(--text); padding: 0.4rem 0.9rem; cursor: pointer; font-size: 0.85rem;
  }
  .btn:hover { background: #3a4552; }
  .btn:disabled { opacity: 0.4; cursor: default; }
  .btn.accent { background: var(--accent); color: #0f1419; font-weight: 600; }
  .btn.sm { padding: 0.2rem 0.6rem; font-size: 0.75rem; }
  .dialog { border: 1px solid var(--accent); border-radius: 6px; padding: 0.6rem 0.8rem; margin: 0.6rem 0; }
  .dialog h3 { margin: 0 0 0.5rem; }
  .hits { list-style: none; padding: 0; margin: 0.4rem 0 0.8rem; }
  .hits li { display: flex; gap: 0.6rem; align-items: center; padding: 0.4rem 0; border-bottom: 1px solid var(--panel-border); }
  .hits img, .noicon { width: 32px; height: 32px; border-radius: 4px; flex: none; background: var(--panel-border); }
  .hit-text { flex: 1; min-width: 0; }
  .dim-sm { font-size: 0.75rem; color: var(--text-dim); }
  .err-msg { color: var(--err); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .info-msg { color: var(--accent); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .warn-msg { color: var(--warn); font-size: 0.85rem; margin-bottom: 0.3rem; }
</style>
