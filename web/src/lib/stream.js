// SSE client: one EventSource feeding Svelte stores. Reconnects with backoff.
import { writable } from 'svelte/store'
import { toChart } from './chartdata.js'

export const containers = writable([])
export const stats = writable({})   // name -> ContainerStats
export const host = writable(null)
export const mc = writable(null)
export const wan = writable(null)
export const connected = writable(false)
export const perf = writable(null)  // Leistungs-Stand (/api/perf, SSE "perf")

// ring buffers for live charts: { seriesKey: {t:[], v:[]} }
export const history = writable({})
const MAX_POINTS = 720 // ~1h at 5s

function pushPoint(key, time, value) {
  history.update((h) => {
    const s = h[key] ?? { t: [], v: [] }
    s.t.push(Math.floor(new Date(time).getTime() / 1000))
    s.v.push(value)
    if (s.t.length > MAX_POINTS) { s.t.shift(); s.v.shift() }
    return { ...h, [key]: s }
  })
}

// Verlauf aus der Datenbank (/api/history) vorladen: die Graphen zeigen beim
// Öffnen sofort die letzte Stunde statt leer zu starten. Speicherwerte liegen
// dort in Bytes, live kommen sie als GiB.
const tracked = new Set()

async function preload(keys) {
  await Promise.all(keys.map(async (key) => {
    try {
      const resp = await fetch(`/api/history?series=${encodeURIComponent(key)}&hours=1`)
      if (!resp.ok) return
      const rows = (await resp.json()) ?? []
      const pts = rows.map((r) => [Math.floor(new Date(r.time).getTime() / 1000), toChart(key, r.value)])
      history.update((h) => {
        const cur = h[key] ?? { t: [], v: [] }
        // zusammenführen, nach Zeit sortieren, gleiche Sekunde nur einmal
        // (Live-Punkt gewinnt)
        const byT = new Map(pts)
        cur.t.forEach((t, i) => byT.set(t, cur.v[i]))
        const t = [...byT.keys()].sort((a, b) => a - b).slice(-MAX_POINTS)
        return { ...h, [key]: { t, v: t.map((x) => byT.get(x)) } }
      })
    } catch {
      // ohne Verlauf zeigt der Graph eben nur die Live-Punkte
    }
  }))
}

// fetchRange lädt einen festen Zeitraum (Unix-Sekunden) für die Graphen im
// Leistung-Tab (6 h / 24 h / Lag-Zeitraum) — unabhängig vom Live-Verlauf.
export async function fetchRange(keys, from, to) {
  const out = {}
  await Promise.all(keys.map(async (key) => {
    try {
      const resp = await fetch(`/api/history?series=${encodeURIComponent(key)}&from=${from}&to=${to}`)
      if (!resp.ok) return
      const rows = (await resp.json()) ?? []
      out[key] = {
        t: rows.map((r) => Math.floor(new Date(r.time).getTime() / 1000)),
        v: rows.map((r) => toChart(key, r.value)),
      }
    } catch {
      // Serie bleibt leer
    }
  }))
  return out
}

// track registers chart series and loads their recent history.
export function track(keys) {
  if (tracked.size === 0 && typeof document !== 'undefined') {
    // Tab war im Hintergrund (Browser drosseln den Stream): Lücke nachladen
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible' && tracked.size) preload([...tracked])
    })
  }
  const fresh = keys.filter((k) => !tracked.has(k))
  fresh.forEach((k) => tracked.add(k))
  if (fresh.length) preload(fresh)
}

let es = null
let retryMs = 1000
let wasDown = false

export function connect() {
  if (es) es.close()
  es = new EventSource('/api/stream/stats')

  es.onopen = () => {
    connected.set(true)
    retryMs = 1000
    // nach einer Unterbrechung (Standby, Netz) die Lücke aus der DB füllen
    if (wasDown && tracked.size) preload([...tracked])
    wasDown = false
  }
  es.onerror = () => {
    connected.set(false)
    wasDown = true
    es.close()
    setTimeout(connect, retryMs)
    retryMs = Math.min(retryMs * 2, 30000)
  }

  es.addEventListener('snapshot', (e) => {
    const snap = JSON.parse(e.data)
    containers.set(snap.containers ?? [])
    stats.set(snap.stats ?? {})
    if (snap.host?.time) host.set(snap.host)
    if (snap.mc?.time) mc.set(snap.mc)
    if (snap.wan?.time) wan.set(snap.wan)
  })

  es.addEventListener('container', (e) => {
    const st = JSON.parse(e.data)
    stats.update((s) => ({ ...s, [st.name]: st }))
    pushPoint(`container.${st.name}.cpu`, st.time, st.cpuPercent)
    pushPoint(`container.${st.name}.mem`, st.time, st.memUsage / 1024 / 1024 / 1024)
  })

  es.addEventListener('host', (e) => {
    const h = JSON.parse(e.data)
    host.set(h)
    pushPoint('host.cpu', h.time, h.cpuPercent)
    pushPoint('host.mem', h.time, h.memUsed / 1024 / 1024 / 1024)
  })

  es.addEventListener('mc', (e) => {
    const m = JSON.parse(e.data)
    mc.set(m)
    pushPoint('mc.tps', m.time, m.tps)
    pushPoint('mc.players', m.time, m.playersOnline)
    if (m.mspt) pushPoint('mc.mspt', m.time, m.mspt)
  })

  es.addEventListener('wan', (e) => {
    const w = JSON.parse(e.data)
    wan.set(w)
    for (const t of w.targets ?? []) {
      pushPoint(`wan.${t.target}.rtt`, w.time, t.reached ? t.rttMs : null)
    }
  })

  es.addEventListener('perf', (e) => {
    const p = JSON.parse(e.data)
    perf.set(p)
    if (p.serverThreadCpu != null) pushPoint('jvm.thread.server.cpu', p.time, p.serverThreadCpu)
    if (p.javaCpu != null) pushPoint('jvm.cpu.total', p.time, p.javaCpu)
    if (p.heapAfterGc != null) pushPoint('jvm.heap.after_gc', p.time, toChart('jvm.heap.after_gc', p.heapAfterGc))
    if (p.heapTotal != null) pushPoint('jvm.heap.total', p.time, toChart('jvm.heap.total', p.heapTotal))
    if (p.gcPauseMax != null) pushPoint('jvm.gc.pause_max', p.time, p.gcPauseMax)
  })
}

async function apiFetch(path, opts = {}) {
  const resp = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  })
  if (resp.status === 401 && path !== '/api/login') {
    authenticated.set(false)
    throw new Error('nicht angemeldet')
  }
  const data = await resp.json().catch(() => ({}))
  if (!resp.ok) throw Object.assign(new Error(data.error ?? resp.statusText), { status: resp.status })
  return data
}

export async function sendRcon(command) {
  const data = await apiFetch('/api/rcon', {
    method: 'POST',
    body: JSON.stringify({ command }),
  })
  return data.response
}

// --- auth ---
export const authRequired = writable(false)
export const authenticated = writable(true)

export async function checkAuth() {
  const data = await apiFetch('/api/auth')
  authRequired.set(data.required)
  authenticated.set(data.authenticated)
  return data.authenticated
}

export async function login(password) {
  await apiFetch('/api/login', { method: 'POST', body: JSON.stringify({ password }) })
  authenticated.set(true)
}

export async function logout() {
  await apiFetch('/api/logout', { method: 'POST' })
  authenticated.set(false)
}

// --- container actions ---
export function containerAction(name, action) {
  return apiFetch(`/api/containers/${encodeURIComponent(name)}/${action}`, { method: 'POST' })
}

// --- routines ---
export const listRoutines = () => apiFetch('/api/routines')
export const createRoutine = (r) => apiFetch('/api/routines', { method: 'POST', body: JSON.stringify(r) })
export const updateRoutine = (r) => apiFetch(`/api/routines/${r.id}`, { method: 'PUT', body: JSON.stringify(r) })
export const deleteRoutine = (id) => apiFetch(`/api/routines/${id}`, { method: 'DELETE' })
export const runRoutine = (id) => apiFetch(`/api/routines/${id}/run`, { method: 'POST' })
export const recentRuns = () => apiFetch('/api/routine-runs')
export const auditLog = () => apiFetch('/api/audit')

// --- mods (Phase 3) ---
export const listMods = (profile) => apiFetch(`/api/mods?profile=${encodeURIComponent(profile)}`)
export const checkMods = (profile) => apiFetch('/api/mods/check', { method: 'POST', body: JSON.stringify({ profile }) })
export const stageMods = (profile, filenames) =>
  apiFetch('/api/mods/stage', { method: 'POST', body: JSON.stringify({ profile, filenames }) })
export const applyMods = (profile, restart) =>
  apiFetch('/api/mods/apply', { method: 'POST', body: JSON.stringify({ profile, restart }) })
export const rollbackMods = (profile) =>
  apiFetch('/api/mods/rollback', { method: 'POST', body: JSON.stringify({ profile }) })
export const versionWatch = () => apiFetch('/api/version-watch')
export const versionWatchCheck = () => apiFetch('/api/version-watch/check', { method: 'POST' })

// --- backup/restore (Phase 4.4) ---
export const restorePlayer = (uuid) =>
  apiFetch('/api/backup/restore-player', { method: 'POST', body: JSON.stringify({ uuid }) })
export const listRestorePlayers = () => apiFetch('/api/backup/players')

// --- Wartungsfenster (Phase 4.6) ---
export const listMaintenance = () => apiFetch('/api/maintenance')
export const createMaintenance = (w) => apiFetch('/api/maintenance', { method: 'POST', body: JSON.stringify(w) })
export const endMaintenance = (id) => apiFetch(`/api/maintenance/${id}/end`, { method: 'POST' })
export const deleteMaintenance = (id) => apiFetch(`/api/maintenance/${id}`, { method: 'DELETE' })

// --- Client-Pack-Veröffentlichung (Phase 4.8) ---
export const publishClientPack = (channel) =>
  apiFetch(`/api/mods/publish${channel ? `?channel=${encodeURIComponent(channel)}` : ''}`, { method: 'POST' })
export const modpackStatus = () => apiFetch('/api/modpack')
export const testModrinth = () => apiFetch('/api/settings/modrinth/test', { method: 'POST' })

// --- Mods hinzufügen/entfernen ---
export const searchMods = (q) => apiFetch(`/api/mods/search?q=${encodeURIComponent(q)}`)
export const planMod = (project) => apiFetch(`/api/mods/plan?project=${encodeURIComponent(project)}`)
export const addMod = (projectId, profiles) =>
  apiFetch('/api/mods/add', { method: 'POST', body: JSON.stringify({ projectId, profiles }) })
export const removeInfo = (profile, file) =>
  apiFetch(`/api/mods/remove-info?profile=${encodeURIComponent(profile)}&file=${encodeURIComponent(file)}`)
export const removeMod = (profile, filename) =>
  apiFetch('/api/mods/remove', { method: 'POST', body: JSON.stringify({ profile, filename }) })
export const discardMod = (profile, filename) =>
  apiFetch('/api/mods/discard', { method: 'POST', body: JSON.stringify({ profile, filename }) })
export const applyInfo = () => apiFetch('/api/mods/apply-info')

// Upload: multipart, deshalb ohne apiFetch (das setzt JSON-Content-Type)
export async function uploadMod(file, profiles, category) {
  // Reihenfolge wichtig: der Server streamt die Datei und braucht Profil
  // und Kategorie vorher — 'file' immer zuletzt anhängen
  const fd = new FormData()
  profiles.forEach((p) => fd.append('profile', p))
  fd.append('category', category)
  fd.append('file', file)
  const resp = await fetch('/api/mods/upload', { method: 'POST', body: fd })
  if (resp.status === 401) {
    authenticated.set(false)
    throw new Error('nicht angemeldet')
  }
  const data = await resp.json().catch(() => ({}))
  if (!resp.ok) throw new Error(data.error ?? resp.statusText)
  return data
}

// --- Ein-Klick-MC-Upgrade ---
export const startVersionUpgrade = (version) =>
  apiFetch('/api/version-upgrade', { method: 'POST', body: JSON.stringify({ version }) })
export const versionUpgradeStatus = () => apiFetch('/api/version-upgrade/status')

// --- Einstellungen ---
export const getSettings = () => apiFetch('/api/settings')
export const saveSettings = (s) => apiFetch('/api/settings', { method: 'PUT', body: JSON.stringify(s) })
export const testDiscord = () => apiFetch('/api/settings/discord/test', { method: 'POST' })

// --- MSM-Selbst-Update ---
export const selfUpdateStatus = (check) => apiFetch(`/api/system/update${check ? '?check=1' : ''}`)
export const selfUpdateApply = (tag) =>
  apiFetch('/api/system/update', { method: 'POST', body: JSON.stringify({ tag }) })

// --- Basis-Modpack ---
export const basepackStatus = () => apiFetch('/api/basepack')
export const basepackSearch = (q) => apiFetch(`/api/basepack/search?q=${encodeURIComponent(q)}`)
export const basepackPreview = (project) => apiFetch(`/api/basepack/preview?project=${encodeURIComponent(project)}`)
export const basepackApply = (projectId, versionId, configs) =>
  apiFetch('/api/basepack/apply', { method: 'POST', body: JSON.stringify({ projectId, versionId, configs }) })
export const basepackToggle = (key, on) => apiFetch('/api/basepack/toggle', { method: 'POST', body: JSON.stringify({ key, on }) })
export const basepackConfigs = (configs) => apiFetch('/api/basepack/configs', { method: 'POST', body: JSON.stringify({ configs }) })
export const basepackRemove = () => apiFetch('/api/basepack', { method: 'DELETE' })

// --- Leistung ---
export const perfStatus = () => apiFetch('/api/perf')
export const perfLags = (days = 7) => apiFetch(`/api/perf/lags?days=${days}`)
