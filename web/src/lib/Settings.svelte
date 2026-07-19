<script>
  import { onMount } from 'svelte'
  import { getSettings, saveSettings, testDiscord } from './stream.js'

  let view = $state(null)
  let error = $state('')
  let info = $state('')
  // Eingabefelder: leer lassen = unverändert; zum Löschen "löschen" klicken
  let discordWebhook = $state('')
  let dropboxKey = $state('')
  let dropboxSecret = $state('')
  let dropboxToken = $state('')

  async function refresh() {
    try {
      view = await getSettings()
      error = ''
    } catch (err) {
      error = err.message
    }
  }
  onMount(refresh)

  async function save() {
    error = ''
    info = ''
    const payload = {}
    if (discordWebhook.trim()) payload.discordWebhook = discordWebhook.trim()
    if (dropboxKey.trim()) payload.dropboxKey = dropboxKey.trim()
    if (dropboxSecret.trim()) payload.dropboxSecret = dropboxSecret.trim()
    if (dropboxToken.trim()) payload.dropboxToken = dropboxToken.trim()
    if (Object.keys(payload).length === 0) {
      info = 'Nichts eingegeben — nichts geändert.'
      return
    }
    try {
      view = await saveSettings(payload)
      discordWebhook = dropboxKey = dropboxSecret = dropboxToken = ''
      info = 'Gespeichert — wirkt sofort, kein Neustart nötig.'
    } catch (err) {
      error = err.message
    }
  }

  async function clearField(field) {
    if (!confirm('Wert löschen? Danach gilt wieder der Wert aus der .env (falls vorhanden).')) return
    try {
      view = await saveSettings({ [field]: '' })
      info = 'Gelöscht.'
    } catch (err) {
      error = err.message
    }
  }

  async function doTest() {
    error = ''
    try {
      const res = await testDiscord()
      info = res.message
    } catch (err) {
      error = err.message
    }
  }

  function status(m) {
    if (!m?.set) return 'nicht gesetzt'
    return `gesetzt (${m.source === 'env' ? '.env' : 'Dashboard'}, endet auf ${m.hint})`
  }
</script>

<div class="panel wide">
  <h2>Grundeinstellungen</h2>
  <p class="dim">
    Zugangsdaten für Integrationen — gespeichert auf dem Server (SQLite), wirksam ohne Neustart.
    Werte werden nie im Klartext angezeigt. Leere Felder bleiben unverändert.
  </p>
  {#if error}<div class="err-msg">{error}</div>{/if}
  {#if info}<div class="ok-msg">{info}</div>{/if}

  {#if view}
    <h3>Discord-Benachrichtigungen</h3>
    <p class="dim">
      Webhook erstellen: Discord-Channel → Einstellungen → Integrationen → Webhooks → „Neuer Webhook" → URL kopieren.
      Status: <strong>{status(view.discordWebhook)}</strong>
    </p>
    <div class="row">
      <input type="password" bind:value={discordWebhook} placeholder="https://discord.com/api/webhooks/…" style="min-width: 26rem" />
      <button class="btn" onclick={doTest} disabled={!view.discordWebhook?.set}>Testnachricht senden</button>
      {#if view.discordWebhook?.source === 'dashboard'}
        <button class="btn danger" onclick={() => clearField('discordWebhook')}>löschen</button>
      {/if}
    </div>

    <h3>Dropbox (Client-Paket-Verteilung)</h3>
    <p class="dim">
      Einmalig: Scoped App auf dropbox.com/developers/apps anlegen (Scopes: files.content.write, files.content.read,
      sharing.write), dann Refresh-Token holen — Schritt-für-Schritt-Anleitung im README des Repos.
      Status: <strong>{view.dropboxReady ? '✔ einsatzbereit' : 'unvollständig'}</strong>
    </p>
    <div class="grid">
      <label>App-Key <span class="dim">({status(view.dropboxKey)})</span>
        <input type="password" bind:value={dropboxKey} placeholder="neuer Wert" /></label>
      <label>App-Secret <span class="dim">({status(view.dropboxSecret)})</span>
        <input type="password" bind:value={dropboxSecret} placeholder="neuer Wert" /></label>
      <label>Refresh-Token <span class="dim">({status(view.dropboxToken)})</span>
        <input type="password" bind:value={dropboxToken} placeholder="neuer Wert" /></label>
    </div>

    <button class="btn accent" onclick={save}>Speichern</button>
  {:else}
    <div class="empty">Lade …</div>
  {/if}
</div>

<style>
  h3 { margin: 1.1rem 0 0.3rem; font-size: 0.95rem; }
  .dim { color: var(--text-dim); font-size: 0.8rem; }
  .row { display: flex; gap: 0.6rem; align-items: center; flex-wrap: wrap; }
  .grid { display: flex; flex-direction: column; gap: 0.6rem; max-width: 30rem; margin-bottom: 0.9rem; }
  .grid label { display: flex; flex-direction: column; gap: 0.2rem; font-size: 0.8rem; color: var(--text-dim); }
  input {
    background: var(--panel); border: 1px solid var(--panel-border);
    border-radius: 5px; color: var(--text); padding: 0.4rem 0.6rem; font-size: 0.85rem;
  }
  .btn {
    background: var(--panel-border); border: none; border-radius: 5px;
    color: var(--text); padding: 0.4rem 0.9rem; cursor: pointer; font-size: 0.8rem;
  }
  .btn.accent { background: var(--accent); color: #0f1419; font-weight: 600; }
  .btn.danger:hover { background: var(--err); color: #0f1419; }
  .btn:disabled { opacity: 0.4; cursor: default; }
  .err-msg { color: var(--err); font-size: 0.85rem; margin: 0.4rem 0; }
  .ok-msg { color: var(--accent); font-size: 0.85rem; margin: 0.4rem 0; }
</style>
