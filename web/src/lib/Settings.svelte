<script>
  import { onMount } from 'svelte'
  import { getSettings, saveSettings, testDiscord, revealSetting } from './stream.js'

  let view = $state(null)
  let error = $state('')
  let info = $state('')
  // Eingabefelder: leer lassen = unverändert; „Anzeigen" holt den aktuellen
  // Wert ins Feld (zum Prüfen/Kopieren/Ändern)
  let fields = $state({ discordWebhook: '', dropboxKey: '', dropboxSecret: '', dropboxToken: '' })
  let shown = $state({}) // field -> true, wenn der echte Wert im Feld steht

  async function refresh() {
    try {
      view = await getSettings()
      error = ''
    } catch (err) {
      error = err.message
    }
  }
  onMount(refresh)

  async function reveal(field) {
    error = ''
    try {
      const res = await revealSetting(field)
      fields[field] = res.value
      shown[field] = true
    } catch (err) {
      error = err.message
    }
  }

  async function save() {
    error = ''
    info = ''
    const payload = {}
    for (const [k, v] of Object.entries(fields)) {
      if (v.trim()) payload[k] = v.trim()
    }
    if (Object.keys(payload).length === 0) {
      info = 'Nichts eingegeben — nichts geändert.'
      return
    }
    try {
      view = await saveSettings(payload)
      fields = { discordWebhook: '', dropboxKey: '', dropboxSecret: '', dropboxToken: '' }
      shown = {}
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
    Zugangsdaten für Integrationen — gespeichert auf dem Server, wirksam ohne Neustart. Anzeige standardmäßig
    maskiert; „Anzeigen" holt den aktuellen Wert ins Feld (wird im Audit-Log vermerkt). Leere Felder bleiben unverändert.
  </p>
  {#if error}<div class="err-msg">{error}</div>{/if}
  {#if info}<div class="ok-msg">{info}</div>{/if}

  {#if view}
    <h3>Discord-Benachrichtigungen</h3>
    <p class="dim">
      Webhook erstellen: Discord-Channel → ⚙️ → Integrationen → Webhooks → „Neuer Webhook" → URL kopieren.
      Status: <strong>{status(view.discordWebhook)}</strong>
    </p>
    <div class="row">
      <input type={shown.discordWebhook ? 'text' : 'password'} bind:value={fields.discordWebhook}
        placeholder="https://discord.com/api/webhooks/…" style="min-width: 26rem" />
      {#if view.discordWebhook?.set}
        <button class="btn" onclick={() => reveal('discordWebhook')}>👁 Anzeigen</button>
        <button class="btn" onclick={doTest}>Testnachricht senden</button>
      {/if}
      {#if view.discordWebhook?.source === 'dashboard'}
        <button class="btn danger" onclick={() => clearField('discordWebhook')}>löschen</button>
      {/if}
    </div>

    <h3>Dropbox (Client-Paket-Verteilung)</h3>
    <p class="dim">Status: <strong>{view.dropboxReady ? '✔ einsatzbereit' : 'unvollständig — Anleitung unten'}</strong></p>
    <div class="grid">
      {#each [['dropboxKey', 'App-Key', view.dropboxKey], ['dropboxSecret', 'App-Secret', view.dropboxSecret], ['dropboxToken', 'Refresh-Token', view.dropboxToken]] as [key, label, m] (key)}
        <label>{label} <span class="dim">({status(m)})</span>
          <span class="row">
            <input type={shown[key] ? 'text' : 'password'} bind:value={fields[key]} placeholder="neuer Wert" style="flex:1" />
            {#if m?.set}<button class="btn" onclick={() => reveal(key)}>👁</button>{/if}
            {#if m?.source === 'dashboard'}<button class="btn danger" onclick={() => clearField(key)}>löschen</button>{/if}
          </span>
        </label>
      {/each}
    </div>

    <button class="btn accent" onclick={save}>Speichern</button>

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
  .row { display: flex; gap: 0.6rem; align-items: center; flex-wrap: wrap; }
  .grid { display: flex; flex-direction: column; gap: 0.6rem; max-width: 34rem; margin-bottom: 0.9rem; }
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
