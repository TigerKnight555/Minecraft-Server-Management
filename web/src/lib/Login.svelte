<script>
  import { login } from './stream.js'

  let password = $state('')
  let error = $state('')
  let busy = $state(false)

  async function submit(e) {
    e.preventDefault()
    if (busy) return
    busy = true
    error = ''
    try {
      await login(password)
    } catch (err) {
      error = err.message
      password = ''
    } finally {
      busy = false
    }
  }
</script>

<div class="login-wrap">
  <form class="login-box" onsubmit={submit}>
    <h1>MSM</h1>
    <p>Anmeldung erforderlich</p>
    <!-- svelte-ignore a11y_autofocus -->
    <input
      type="password"
      bind:value={password}
      placeholder="Passwort"
      autofocus
      disabled={busy}
    />
    {#if error}<div class="login-error">{error}</div>{/if}
    <button type="submit" disabled={busy || !password}>Anmelden</button>
  </form>
</div>

<style>
  .login-wrap {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .login-box {
    background: var(--panel);
    border: 1px solid var(--panel-border);
    border-radius: 10px;
    padding: 2rem 2.5rem;
    display: flex;
    flex-direction: column;
    gap: 0.8rem;
    width: min(320px, 90vw);
  }
  .login-box h1 { margin: 0; text-align: center; }
  .login-box p { margin: 0 0 0.5rem; color: var(--text-dim); text-align: center; font-size: 0.85rem; }
  .login-box input {
    background: #0a0e12;
    border: 1px solid var(--panel-border);
    border-radius: 6px;
    color: var(--text);
    padding: 0.6rem 0.8rem;
    font-size: 1rem;
  }
  .login-box button {
    background: var(--accent);
    color: #0f1419;
    border: none;
    border-radius: 6px;
    padding: 0.6rem;
    font-size: 0.95rem;
    font-weight: 600;
    cursor: pointer;
  }
  .login-box button:disabled { opacity: 0.5; cursor: default; }
  .login-error { color: var(--err); font-size: 0.85rem; text-align: center; }
</style>
