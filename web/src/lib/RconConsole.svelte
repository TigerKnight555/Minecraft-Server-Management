<script>
  import { sendRcon } from './stream.js'

  let command = $state('')
  let output = $state([])
  let busy = $state(false)
  let outEl

  async function submit(e) {
    e.preventDefault()
    const cmd = command.trim()
    if (!cmd || busy) return
    busy = true
    output.push(`> ${cmd}`)
    command = ''
    try {
      const resp = await sendRcon(cmd)
      output.push(resp || '(keine Ausgabe)')
    } catch (err) {
      output.push(`Fehler: ${err.message}`)
    } finally {
      busy = false
      queueMicrotask(() => outEl && (outEl.scrollTop = outEl.scrollHeight))
    }
  }
</script>

<div class="panel">
  <h2>RCON-Konsole</h2>
  <div class="log-view" style="height: 200px" bind:this={outEl}>{output.join('\n')}</div>
  <form class="rcon-input" onsubmit={submit}>
    <input
      bind:value={command}
      placeholder="Befehl, z. B. list"
      disabled={busy}
      spellcheck="false"
      autocomplete="off"
    />
    <button type="submit" disabled={busy}>Senden</button>
  </form>
</div>
