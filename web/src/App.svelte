<script>
  import { onMount } from 'svelte'
  import {
    connect, connected, containers, stats, host, mc, wan,
    authRequired, authenticated, checkAuth, logout,
  } from './lib/stream.js'
  import Chart from './lib/Chart.svelte'
  import LogViewer from './lib/LogViewer.svelte'
  import RconConsole from './lib/RconConsole.svelte'
  import Login from './lib/Login.svelte'
  import Containers from './lib/Containers.svelte'
  import Routines from './lib/Routines.svelte'
  import Audit from './lib/Audit.svelte'

  let authChecked = $state(false)
  let tab = $state('dashboard')

  onMount(async () => {
    try {
      await checkAuth()
    } catch {
      // backend without auth endpoints (old build) — run open
    }
    authChecked = true
  })

  // (re)connect the SSE stream whenever we are authenticated
  $effect(() => {
    if (authChecked && $authenticated) connect()
  })

  const GiB = 1024 * 1024 * 1024

  function fmtGiB(bytes) {
    return bytes != null ? (bytes / GiB).toFixed(1) + ' GiB' : '—'
  }
  function fmtUptime(sec) {
    if (sec == null) return '—'
    const d = Math.floor(sec / 86400)
    const h = Math.floor((sec % 86400) / 3600)
    return d > 0 ? `${d}d ${h}h` : `${h}h ${Math.floor((sec % 3600) / 60)}m`
  }

  let mcStats = $derived($stats['mc-fabric'])
  let wanWorst = $derived(
    $wan?.targets?.filter((t) => t.target !== gateway($wan))
      .reduce((worst, t) => (!t.reached || t.rttMs > (worst?.rttMs ?? -1) ? t : worst), null)
  )
  function gateway(w) {
    // last target is the auto-appended gateway when present
    const priv = w?.targets?.find((t) => t.target.startsWith('192.168.') || t.target.startsWith('10.'))
    return priv?.target
  }

  let tpsClass = $derived($mc?.tps >= 19 ? 'ok' : $mc?.tps >= 15 ? 'warn' : 'err')
  let wanClass = $derived(
    !wanWorst || !wanWorst.reached ? 'err' : wanWorst.lossPct > 0 || wanWorst.rttMs > 80 ? 'warn' : 'ok'
  )
</script>

{#if !authChecked}
  <div class="container"><p class="empty">Lade …</p></div>
{:else if $authRequired && !$authenticated}
  <Login />
{:else}
<div class="container">
  <header>
    <h1>MSM</h1>
    <span class="sub">Minecraft Server Management</span>
    <nav class="tabs">
      <button class={tab === 'dashboard' ? 'active' : ''} onclick={() => (tab = 'dashboard')}>Dashboard</button>
      <button class={tab === 'routines' ? 'active' : ''} onclick={() => (tab = 'routines')}>Routinen</button>
      <button class={tab === 'audit' ? 'active' : ''} onclick={() => (tab = 'audit')}>Audit</button>
    </nav>
    <span class="conn-status {$connected ? 'live' : 'dead'}">
      {$connected ? 'live' : 'getrennt'}
    </span>
    {#if $authRequired}
      <button class="logout" onclick={logout}>Abmelden</button>
    {/if}
  </header>

  {#if tab === 'routines'}
    <div class="panels"><Routines /></div>
  {:else if tab === 'audit'}
    <div class="panels"><Audit /></div>
  {:else}

  <div class="tiles">
    <div class="tile {$mc?.online ? 'ok' : 'err'}">
      <div class="label">Minecraft</div>
      <div class="value">{$mc?.online ? 'Online' : 'Offline'}</div>
      <div class="detail">{$mc?.version ?? ''} {$mc?.motd ? '· ' + $mc.motd : ''}</div>
    </div>

    <div class="tile">
      <div class="label">Spieler</div>
      <div class="value">{$mc?.playersOnline ?? '—'}<span style="font-size:0.8rem;color:var(--text-dim)">/{$mc?.playersMax ?? '—'}</span></div>
    </div>

    <div class="tile {tpsClass}">
      <div class="label">TPS</div>
      <div class="value">{$mc?.tps ? $mc.tps.toFixed(1) : '—'}</div>
      <div class="detail">{$mc?.mspt ? $mc.mspt.toFixed(1) + ' ms/Tick' : ''}</div>
    </div>

    <div class="tile">
      <div class="label">RAM Minecraft</div>
      <div class="value">{fmtGiB(mcStats?.memUsage)}</div>
      <div class="detail">von {fmtGiB(mcStats?.memLimit)}</div>
    </div>

    <div class="tile">
      <div class="label">RAM Host</div>
      <div class="value">{fmtGiB($host?.memUsed)}</div>
      <div class="detail">von {fmtGiB($host?.memTotal)}</div>
    </div>

    <div class="tile">
      <div class="label">CPU Host</div>
      <div class="value">{$host?.cpuPercent != null ? $host.cpuPercent.toFixed(0) + '%' : '—'}</div>
      <div class="detail">Load {$host?.load1?.toFixed(2) ?? '—'}</div>
    </div>

    <div class="tile {wanClass}">
      <div class="label">Internet</div>
      <div class="value">{wanWorst?.reached ? wanWorst.rttMs.toFixed(0) + ' ms' : 'gestört'}</div>
      <div class="detail">{wanWorst ? wanWorst.target + (wanWorst.lossPct ? ` · ${wanWorst.lossPct.toFixed(0)}% Verlust` : '') : ''}</div>
    </div>

    <div class="tile {$host?.nasOnline ? 'ok' : 'err'}">
      <div class="label">NAS</div>
      <div class="value">{$host?.nasOnline ? 'erreichbar' : 'offline'}</div>
    </div>

    <div class="tile">
      <div class="label">Host-Uptime</div>
      <div class="value" style="font-size:1.1rem">{fmtUptime($host?.uptimeSec)}</div>
    </div>

    <div class="tile">
      <div class="label">Container</div>
      <div class="value">{$containers.filter((c) => c.state === 'running').length}/{$containers.length}</div>
      <div class="detail">laufen</div>
    </div>
  </div>

  <div class="panels">
    <div class="panel">
      <h2>CPU (%)</h2>
      <Chart
        unit="%"
        series={[
          { key: 'host.cpu', label: 'Host', color: '#60a5fa' },
          { key: 'container.mc-fabric.cpu', label: 'mc-fabric', color: '#4ade80' },
        ]}
      />
    </div>

    <div class="panel">
      <h2>RAM (GiB)</h2>
      <Chart
        unit="GiB"
        series={[
          { key: 'host.mem', label: 'Host', color: '#60a5fa' },
          { key: 'container.mc-fabric.mem', label: 'mc-fabric', color: '#4ade80' },
        ]}
      />
    </div>

    <div class="panel">
      <h2>TPS</h2>
      <Chart
        unit="TPS"
        series={[{ key: 'mc.tps', label: 'TPS', color: '#4ade80' }]}
      />
    </div>

    <div class="panel">
      <h2>Internet-Latenz (ms)</h2>
      <Chart
        unit="ms"
        series={[
          { key: 'wan.1.1.1.1.rtt', label: '1.1.1.1', color: '#f59e0b' },
          { key: 'wan.9.9.9.9.rtt', label: '9.9.9.9', color: '#a78bfa' },
        ]}
      />
    </div>

    <div class="panel">
      <h2>Spieler online</h2>
      {#if $mc?.players?.length}
        <ul class="player-list">
          {#each $mc.players as p (p)}
            <li>{p}</li>
          {/each}
        </ul>
      {:else}
        <div class="empty">Niemand online</div>
      {/if}
    </div>

    <RconConsole />

    <Containers />

    <LogViewer />
  </div>
  {/if}
</div>
{/if}
