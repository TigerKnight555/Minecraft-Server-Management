<script>
  import { onMount } from 'svelte'
  import { getSettings, saveSettings, testDiscord, selfUpdateStatus, selfUpdateApply } from './stream.js'

  let view = $state(null)
  let error = $state('')
  let info = $state('')
  // Felder zeigen immer die aktuell wirksamen Werte (Nutzer-Entscheidung:
  // konfigurierte Werte sind sichtbar). Leeren + Speichern = löschen.
  let fields = $state({ discordWebhook: '', dropboxKey: '', dropboxSecret: '', dropboxToken: '' })

  function apply(res) {
    view = res.view
    fields = { ...fields, ...res.values }
  }

  async function refresh() {
    try {
      apply(await getSettings())
      error = ''
    } catch (err) {
      error = err.message
    }
  }
  onMount(refresh)

  async function save() {
    error = ''
    info = ''
    // alle Felder senden: Wert = setzen, leer = löschen (.env-Fallback greift)
    try {
      apply(await saveSettings({
        discordWebhook: fields.discordWebhook.trim(),
        dropboxKey: fields.dropboxKey.trim(),
        dropboxSecret: fields.dropboxSecret.trim(),
        dropboxToken: fields.dropboxToken.trim(),
      }))
      info = 'Gespeichert — wirkt sofort, kein Neustart nötig.'
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
    return `Quelle: ${m.source === 'env' ? '.env' : 'Dashboard'}`
  }

  // --- MSM-Selbst-Update ---
  let upd = $state(null)
  let updBusy = $state(false)

  async function loadUpdate(force) {
    try {
      upd = await selfUpdateStatus(force)
    } catch {
      upd = null
    }
  }
  onMount(() => loadUpdate(false))

  async function applyUpdate() {
    if (!upd?.latest) return
    if (!confirm(`MSM auf ${upd.latest} aktualisieren?

Das Dashboard ist während des Neubaus (~1-2 min) kurz nicht erreichbar. Der Minecraft-Server läuft normal weiter.`)) return
    updBusy = true
    try {
      const res = await selfUpdateApply(upd.latest)
      info = res.message
    } catch (err) {
      error = err.message
    } finally {
      updBusy = false
    }
  }
</script>

<div class="panel wide">
  <h2>Grundeinstellungen</h2>
  <p class="dim">
    Zugangsdaten für Integrationen — gespeichert auf dem Server, wirksam sofort ohne Neustart.
    Feld leeren + Speichern löscht den Wert (dann gilt wieder die .env, falls dort einer steht).
  </p>
  {#if error}<div class="err-msg">{error}</div>{/if}
  {#if info}<div class="ok-msg">{info}</div>{/if}

  {#if view}
    <h3>Discord-Benachrichtigungen</h3>
    <p class="dim">
      Webhook erstellen: Discord-Channel → ⚙️ → Integrationen → Webhooks → „Neuer Webhook" → URL kopieren.
      <strong>{status(view.discordWebhook)}</strong>
    </p>
    <div class="row">
      <input type="text" bind:value={fields.discordWebhook}
        placeholder="https://discord.com/api/webhooks/…" style="min-width: 28rem" />
      {#if view.discordWebhook?.set}
        <button class="btn" onclick={doTest}>Testnachricht senden</button>
      {/if}
    </div>

    <h3>Dropbox (Client-Paket-Verteilung)</h3>
    <p class="dim">Status: <strong>{view.dropboxReady ? '✔ einsatzbereit' : 'unvollständig — Anleitung unten'}</strong></p>
    <div class="grid">
      <label>App-Key <span class="dim">({status(view.dropboxKey)})</span>
        <input type="text" bind:value={fields.dropboxKey} /></label>
      <label>App-Secret <span class="dim">({status(view.dropboxSecret)})</span>
        <input type="text" bind:value={fields.dropboxSecret} /></label>
      <label>Refresh-Token <span class="dim">({status(view.dropboxToken)})</span>
        <input type="text" bind:value={fields.dropboxToken} /></label>
    </div>

    <button class="btn accent" onclick={save}>Speichern</button>

    <h3>MSM-Version (Selbst-Update)</h3>
    {#if upd}
      <p class="dim">
        Läuft: <strong>{upd.current}</strong>
        {#if upd.latest} · Neuester Release-Tag: <strong>{upd.latest}</strong>{/if}
        {#if upd.error} · <span style="color:var(--err)">{upd.error}</span>{/if}
      </p>
      <div class="row">
        {#if upd.newer}
          <button class="btn accent" onclick={applyUpdate} disabled={updBusy}>Auf {upd.latest} aktualisieren</button>
        {:else if upd.latest}
          <span class="dim">✔ aktuell</span>
        {/if}
        <button class="btn" onclick={() => loadUpdate(true)}>Jetzt prüfen</button>
      </div>
    {:else}
      <p class="dim">Versions-Check nicht verfügbar.</p>
    {/if}

    <details class="howto">
      <summary>📖 Anleitung: Dropbox-Zugangsdaten beschaffen (einmalig, ~5 Minuten)</summary>
      <ol>
        <li><a href="https://www.dropbox.com/developers/apps" target="_blank" rel="noreferrer">dropbox.com/developers/apps</a> öffnen (mit deinem Dropbox-Konto anmelden) → <strong>„Create app"</strong></li>
        <li>Auswahl: <strong>„Scoped access"</strong> → <strong>„App folder"</strong> (die App sieht nur ihren eigenen Ordner — die Pakete landen in <code>Apps/&lt;App-Name&gt;/MSM/</code>) → Name vergeben (muss Dropbox-weit eindeutig sein, z. B. <code>msm-meinserver</code>) → „Create app"</li>
        <li>Auf der App-Seite: Reiter <strong>„Permissions"</strong> → Haken bei <code>files.content.write</code>, <code>files.content.read</code>, <code>sharing.write</code> → unten <strong>„Submit"</strong>. <em>Wichtig: VOR dem nächsten Schritt — sonst gilt der Token ohne diese Rechte und alles muss wiederholt werden.</em></li>
        <li>Reiter <strong>„Settings"</strong>: dort stehen <strong>App key</strong> und <strong>App secret</strong> („Show" klicken) → beide oben eintragen</li>
        <li>Autorisierungs-Code holen — diese URL im Browser öffnen, <code>APP_KEY</code> ersetzen:<br />
          <code>https://www.dropbox.com/oauth2/authorize?client_id=APP_KEY&response_type=code&token_access_type=offline</code><br />
          → „Zulassen" → den angezeigten Code kopieren (einmalig gültig, wenige Minuten)</li>
        <li>Refresh-Token tauschen — im Terminal (PC oder Server), <code>CODE/APP_KEY/APP_SECRET</code> ersetzen:<br />
          <code>curl https://api.dropbox.com/oauth2/token -d code=CODE -d grant_type=authorization_code -u APP_KEY:APP_SECRET</code><br />
          → aus der JSON-Antwort den Wert von <code>"refresh_token"</code> kopieren (NICHT den <code>access_token</code> — der läuft nach 4 h ab) → oben eintragen</li>
        <li><strong>Speichern</strong> → Status muss auf „✔ einsatzbereit" springen → Probelauf: Mods-Tab → Profil „Client-Paket" → „Client-Paket veröffentlichen"</li>
      </ol>
      <p class="dim">Der Refresh-Token läuft nicht ab; MSM holt sich damit selbstständig kurzlebige Zugriffstoken. Bei Fehler „invalid_grant": Code war abgelaufen/verbraucht — Schritt 5+6 wiederholen.</p>
    </details>
  {:else}
    <div class="empty">Lade …</div>
  {/if}
</div>

<style>
  h3 { margin: 1.1rem 0 0.3rem; font-size: 0.95rem; }
  .dim { color: var(--text-dim); font-size: 0.8rem; }
  .row { display: flex; gap: 0.6rem; align-items: center; flex-wrap: wrap; margin-bottom: 0.4rem; }
  .grid { display: flex; flex-direction: column; gap: 0.6rem; max-width: 34rem; margin-bottom: 0.9rem; }
  .grid label { display: flex; flex-direction: column; gap: 0.2rem; font-size: 0.8rem; color: var(--text-dim); }
  input {
    background: var(--panel); border: 1px solid var(--panel-border);
    border-radius: 5px; color: var(--text); padding: 0.4rem 0.6rem; font-size: 0.85rem;
    font-family: ui-monospace, monospace;
  }
  .btn {
    background: var(--panel-border); border: none; border-radius: 5px;
    color: var(--text); padding: 0.4rem 0.9rem; cursor: pointer; font-size: 0.8rem;
  }
  .btn.accent { background: var(--accent); color: #0f1419; font-weight: 600; }
  .err-msg { color: var(--err); font-size: 0.85rem; margin: 0.4rem 0; }
  .ok-msg { color: var(--accent); font-size: 0.85rem; margin: 0.4rem 0; }
  .howto { margin-top: 1.2rem; font-size: 0.85rem; }
  .howto summary { cursor: pointer; color: var(--text); }
  .howto ol { margin: 0.6rem 0 0 1.2rem; display: flex; flex-direction: column; gap: 0.5rem; }
  .howto code {
    background: #0a0e12; border-radius: 4px; padding: 0.1rem 0.35rem;
    font-size: 0.78rem; word-break: break-all;
  }
  .howto a { color: var(--accent); }
</style>
